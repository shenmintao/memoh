package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"

	"github.com/felinics/memoh/internal/accounts"
	"github.com/felinics/memoh/internal/bots"
	"github.com/felinics/memoh/internal/channel"
	"github.com/felinics/memoh/internal/channelaccess"
	"github.com/felinics/memoh/internal/db"
	"github.com/felinics/memoh/internal/db/postgres/sqlc"
	dbstore "github.com/felinics/memoh/internal/db/store"
	"github.com/felinics/memoh/internal/push"
)

type PushEndpoint struct {
	ID                string    `json:"id"`
	BotID             string    `json:"bot_id"`
	ChannelIdentityID string    `json:"channel_identity_id"`
	Name              string    `json:"name"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	// Key and path appear only once at creation or key rotation.
	Key  string `json:"key,omitempty"`
	Path string `json:"path,omitempty"`
}

type CreatePushEndpointRequest struct {
	Name              string `json:"name"`
	BotID             string `json:"bot_id"`
	ChannelIdentityID string `json:"channel_identity_id"`
}

type UpdatePushEndpointRequest struct {
	Enabled bool `json:"enabled"`
}
type PushEndpointList struct {
	Items []PushEndpoint `json:"items"`
}
type PushReceipt struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Duplicate bool      `json:"duplicate"`
	Attempts  int32     `json:"attempts"`
	ErrorCode string    `json:"error_code,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}
type PushReceiptList struct {
	Items []PushReceipt `json:"items"`
}

type pushRateEntry struct {
	limiter *rate.Limiter
	used    time.Time
}
type PushHandler struct {
	queries   dbstore.Queries
	accounts  *accounts.Service
	bots      *bots.Service
	bindings  *channelaccess.Service
	channels  *channel.Store
	runtime   channel.Runtime
	log       *slog.Logger
	authorize func(context.Context, sqlc.PushEndpoint) (channel.ChannelType, string, error)
	send      func(context.Context, string, channel.ChannelType, channel.SendRequest) error
	mu        sync.Mutex
	rates     map[string]pushRateEntry
}

func NewPushHandler(log *slog.Logger, queries dbstore.Queries, accountService *accounts.Service, botService *bots.Service, bindings *channelaccess.Service, channels *channel.Store, runtime channel.Runtime) *PushHandler {
	h := &PushHandler{queries: queries, accounts: accountService, bots: botService, bindings: bindings, channels: channels, runtime: runtime, log: log, rates: make(map[string]pushRateEntry)}
	h.authorize = h.resolveTarget
	h.send = runtime.Send
	return h
}

func (h *PushHandler) Register(e *echo.Echo) {
	g := e.Group("/users/me/push-endpoints")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.PATCH("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	g.POST("/:id/rotate-key", h.Rotate)
	g.GET("/:id/deliveries", h.Deliveries)
	g.POST("/:id/test", h.Test)
	e.POST("/push/:id", h.Receive)
}

func pushEndpoint(row sqlc.PushEndpoint) PushEndpoint {
	return PushEndpoint{ID: row.ID.String(), BotID: row.BotID.String(), ChannelIdentityID: row.ChannelIdentityID.String(), Name: row.Name, Enabled: row.Enabled, CreatedAt: db.TimeFromPg(row.CreatedAt)}
}

func pushReceipt(row sqlc.PushDelivery, duplicate bool) PushReceipt {
	return PushReceipt{ID: row.ID.String(), Status: row.Status, Duplicate: duplicate, Attempts: row.Attempts, ErrorCode: row.ErrorCode, UpdatedAt: db.TimeFromPg(row.UpdatedAt)}
}

func newPushKey() string {
	return "mpk_" + base64.RawURLEncoding.EncodeToString([]byte(rand.Text()))
}

func withPushKey(row sqlc.PushEndpoint, key string) PushEndpoint {
	item := pushEndpoint(row)
	item.Key = key
	item.Path = "/push/" + item.ID + "?key=" + key
	return item
}

// List godoc
// @Summary List personal notification endpoints
// @Tags push
// @Success 200 {object} PushEndpointList
// @Failure 401 {object} ErrorResponse
// @Router /users/me/push-endpoints [get].
func (h *PushHandler) List(c echo.Context) error {
	user, err := RequireChannelIdentityID(c)
	if err != nil {
		return err
	}
	id, _ := db.ParseUUID(user)
	rows, err := h.queries.ListPushEndpoints(c.Request().Context(), id)
	if err != nil {
		return h.unavailable()
	}
	items := make([]PushEndpoint, 0, len(rows))
	for _, row := range rows {
		items = append(items, pushEndpoint(row))
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, PushEndpointList{Items: items})
}

// Create godoc
// @Summary Create an endpoint bound to a bot and one of your linked accounts
// @Tags push
// @Accept json
// @Param payload body CreatePushEndpointRequest true "Fixed destination"
// @Success 201 {object} PushEndpoint
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Router /users/me/push-endpoints [post].
func (h *PushHandler) Create(c echo.Context) error {
	user, err := RequireChannelIdentityID(c)
	if err != nil {
		return err
	}
	var req CreatePushEndpointRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid endpoint")
	}
	name := strings.TrimSpace(req.Name)
	botID, err := db.ParseUUID(req.BotID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid bot ID")
	}
	identityID, err := db.ParseUUID(req.ChannelIdentityID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid channel identity ID")
	}
	if name == "" || len(name) > 100 || strings.ContainsRune(name, '\x00') {
		return echo.NewHTTPError(http.StatusBadRequest, "name must be 1–100 bytes")
	}
	userID, _ := db.ParseUUID(user)
	candidate := sqlc.PushEndpoint{UserID: userID, BotID: botID, ChannelIdentityID: identityID}
	if _, _, err := h.authorize(c.Request().Context(), candidate); err != nil {
		return err
	}
	existing, err := h.queries.ListPushEndpoints(c.Request().Context(), userID)
	if err != nil {
		return h.unavailable()
	}
	if len(existing) >= 100 {
		return echo.NewHTTPError(http.StatusConflict, "endpoint limit reached")
	}
	key := newPushKey()
	row, err := h.queries.CreatePushEndpoint(c.Request().Context(), sqlc.CreatePushEndpointParams{UserID: userID, BotID: botID, ChannelIdentityID: identityID, Name: name, TokenHash: push.Hash(key)})
	if err != nil {
		return h.unavailable()
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, withPushKey(row, key))
}

func (h *PushHandler) owned(c echo.Context) (sqlc.PushEndpoint, error) {
	user, err := RequireChannelIdentityID(c)
	if err != nil {
		return sqlc.PushEndpoint{}, err
	}
	id, err := db.ParseUUID(c.Param("id"))
	if err != nil {
		return sqlc.PushEndpoint{}, echo.ErrNotFound
	}
	row, err := h.queries.GetPushEndpoint(c.Request().Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, echo.ErrNotFound
	}
	if err != nil {
		return row, h.unavailable()
	}
	if row.UserID.String() != user {
		return sqlc.PushEndpoint{}, echo.ErrNotFound
	}
	return row, nil
}

// Update godoc
// @Summary Enable or pause a notification endpoint
// @Tags push
// @Param id path string true "Endpoint ID"
// @Param payload body UpdatePushEndpointRequest true "Enabled state"
// @Success 200 {object} PushEndpoint
// @Router /users/me/push-endpoints/{id} [patch].
func (h *PushHandler) Update(c echo.Context) error {
	row, err := h.owned(c)
	if err != nil {
		return err
	}
	var req UpdatePushEndpointRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid endpoint")
	}
	row, err = h.queries.UpdatePushEndpoint(c.Request().Context(), sqlc.UpdatePushEndpointParams{ID: row.ID, UserID: row.UserID, Enabled: req.Enabled})
	if err != nil {
		return h.unavailable()
	}
	return c.JSON(http.StatusOK, pushEndpoint(row))
}

// Rotate godoc
// @Summary Rotate endpoint credentials; the previous key stops working immediately
// @Tags push
// @Param id path string true "Endpoint ID"
// @Success 200 {object} PushEndpoint
// @Router /users/me/push-endpoints/{id}/rotate-key [post].
func (h *PushHandler) Rotate(c echo.Context) error {
	row, err := h.owned(c)
	if err != nil {
		return err
	}
	key := newPushKey()
	row, err = h.queries.RotatePushEndpointKey(c.Request().Context(), sqlc.RotatePushEndpointKeyParams{ID: row.ID, UserID: row.UserID, TokenHash: push.Hash(key)})
	if err != nil {
		return h.unavailable()
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, withPushKey(row, key))
}

// Delete godoc
// @Summary Delete a personal notification endpoint
// @Tags push
// @Param id path string true "Endpoint ID"
// @Success 204
// @Router /users/me/push-endpoints/{id} [delete].
func (h *PushHandler) Delete(c echo.Context) error {
	row, err := h.owned(c)
	if err != nil {
		return err
	}
	_, err = h.queries.DeletePushEndpoint(c.Request().Context(), sqlc.DeletePushEndpointParams{ID: row.ID, UserID: row.UserID})
	if err != nil {
		return h.unavailable()
	}
	return c.NoContent(http.StatusNoContent)
}

// Deliveries godoc
// @Summary List the latest 20 delivery receipts; message bodies are never stored
// @Tags push
// @Param id path string true "Endpoint ID"
// @Success 200 {object} PushReceiptList
// @Router /users/me/push-endpoints/{id}/deliveries [get].
func (h *PushHandler) Deliveries(c echo.Context) error {
	row, err := h.owned(c)
	if err != nil {
		return err
	}
	rows, err := h.queries.ListPushDeliveries(c.Request().Context(), row.ID)
	if err != nil {
		return h.unavailable()
	}
	items := make([]PushReceipt, 0, len(rows))
	for _, item := range rows {
		items = append(items, pushReceipt(item, false))
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, PushReceiptList{Items: items})
}

// Test godoc
// @Summary Send a synthetic notification to the configured recipient
// @Tags push
// @Param id path string true "Endpoint ID"
// @Success 200 {object} PushReceipt
// @Failure 502 {object} ErrorResponse
// @Router /users/me/push-endpoints/{id}/test [post].
func (h *PushHandler) Test(c echo.Context) error {
	row, err := h.owned(c)
	if err != nil {
		return err
	}
	text := "Memoh 消息推送测试\n接口：" + row.Name + "\n这是一条测试通知。"
	return h.deliver(c, row, push.Notification{Text: text, PayloadHash: push.Hash(text)})
}

// Receive godoc
// @Summary Forward a notification directly without invoking a model
// @Description Authenticate with an endpoint key in Authorization: Bearer or the key query parameter. Supports text/plain and JSON text/content/message; sms_forwarding fields are optional. 200 means channel acceptance, not a recipient read receipt. An event_id or Idempotency-Key deduplicates retries for 30 days.
// @Tags push
// @Accept json,plain
// @Produce json
// @Param id path string true "Endpoint ID"
// @Param key query string false "Endpoint key (or Bearer header)"
// @Param Idempotency-Key header string false "Stable event identifier"
// @Param payload body push.Payload true "Notification (or raw text/plain)"
// @Success 200 {object} PushReceipt
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 413 {object} ErrorResponse
// @Failure 429 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /push/{id} [post].
func (h *PushHandler) Receive(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	id, err := db.ParseUUID(c.Param("id"))
	if err != nil {
		return echo.ErrUnauthorized
	}
	key := strings.TrimSpace(c.QueryParam("key"))
	if auth := c.Request().Header.Get("Authorization"); auth != "" {
		parts := strings.Fields(auth)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return echo.ErrUnauthorized
		}
		if key != "" && key != parts[1] {
			return echo.ErrUnauthorized
		}
		key = parts[1]
	}
	if !strings.HasPrefix(key, "mpk_") || len(key) > 128 {
		return echo.ErrUnauthorized
	}
	row, err := h.queries.GetPushEndpoint(c.Request().Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.ErrUnauthorized
	}
	if err != nil {
		return h.unavailable()
	}
	if subtle.ConstantTimeCompare([]byte(push.Hash(key)), []byte(row.TokenHash)) != 1 {
		return echo.ErrUnauthorized
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, push.MaxBodyBytes+1))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "could not read body")
	}
	if len(body) > push.MaxBodyBytes {
		return echo.ErrStatusRequestEntityTooLarge
	}
	notification, err := push.Parse(c.Request().Header.Get("Content-Type"), body, c.Request().Header.Get("Idempotency-Key"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return h.deliver(c, row, notification)
}

func (h *PushHandler) resolveTarget(ctx context.Context, row sqlc.PushEndpoint) (channel.ChannelType, string, error) {
	userID, botID := row.UserID.String(), row.BotID.String()
	if err := h.accounts.ValidateSession(ctx, userID); err != nil {
		return "", "", echo.NewHTTPError(http.StatusForbidden, "endpoint owner is unavailable")
	}
	if _, err := AuthorizeBotAccess(ctx, h.bots, h.accounts, userID, botID); err != nil {
		return "", "", err
	}
	bindings, err := h.bindings.ListUserBindings(ctx, userID)
	if err != nil {
		return "", "", h.unavailable()
	}
	for _, binding := range bindings {
		if binding.ChannelIdentityID != row.ChannelIdentityID.String() {
			continue
		}
		typ := channel.ChannelType(binding.ChannelType)
		cfg, err := h.channels.ResolveEffectiveConfig(ctx, botID, typ)
		if err != nil || cfg.Disabled {
			return "", "", echo.NewHTTPError(http.StatusConflict, "bot channel is not enabled")
		}
		// Use the observed private conversation, since a profile /link binding
		// is distinct from the legacy outbound user-channel configuration.
		// This also handles channels whose reply target differs from a user ID.
		configID, err := db.ParseUUID(cfg.ID)
		if err != nil {
			return "", "", echo.NewHTTPError(http.StatusConflict, "channel cannot receive external notifications")
		}
		target, err := h.queries.GetPushRecipientTarget(ctx, sqlc.GetPushRecipientTargetParams{
			BotID: row.BotID, ChannelConfigID: configID, ChannelIdentityID: row.ChannelIdentityID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", echo.NewHTTPError(http.StatusConflict, "send this bot a private message from the linked account first")
		}
		if err != nil {
			return "", "", h.unavailable()
		}
		return typ, target.String, nil
	}
	return "", "", echo.NewHTTPError(http.StatusForbidden, "recipient is no longer linked to endpoint owner")
}

func (h *PushHandler) allow(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if len(h.rates) > 1000 {
		for key, item := range h.rates {
			if now.Sub(item.used) > time.Hour {
				delete(h.rates, key)
			}
		}
	}
	entry, ok := h.rates[id]
	if !ok {
		entry.limiter = rate.NewLimiter(rate.Every(time.Second), 10)
	}
	entry.used = now
	h.rates[id] = entry
	return entry.limiter.Allow()
}

func (h *PushHandler) deliver(c echo.Context, endpoint sqlc.PushEndpoint, n push.Notification) error {
	if !endpoint.Enabled {
		return echo.NewHTTPError(http.StatusForbidden, "endpoint is paused")
	}
	if !h.allow(endpoint.ID.String()) {
		c.Response().Header().Set("Retry-After", "1")
		return echo.ErrTooManyRequests
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 35*time.Second)
	defer cancel()
	typ, target, err := h.authorize(ctx, endpoint)
	if err != nil {
		return err
	}
	if n.EventID == "" {
		n.EventID = uuid.NewString()
	}
	key := push.Hash(n.EventID)
	if err := h.queries.PrunePushDeliveries(ctx, endpoint.ID); err != nil {
		return h.unavailable()
	}
	receipt, err := h.queries.ClaimPushDelivery(ctx, sqlc.ClaimPushDeliveryParams{EndpointID: endpoint.ID, DedupeKey: key, PayloadHash: n.PayloadHash})
	if errors.Is(err, pgx.ErrNoRows) {
		receipt, err = h.queries.GetPushDelivery(ctx, sqlc.GetPushDeliveryParams{EndpointID: endpoint.ID, DedupeKey: key})
		if err != nil {
			return h.unavailable()
		}
		if receipt.PayloadHash != n.PayloadHash {
			return echo.NewHTTPError(http.StatusConflict, "event ID already used for different content")
		}
		if receipt.Status == "sent" {
			return c.JSON(http.StatusOK, pushReceipt(receipt, true))
		}
		// A pending send may still be running or have lost its result in a crash.
		// Retain the receipt so the owner can inspect before replay.
		return echo.NewHTTPError(http.StatusConflict, "delivery is in progress or has an unconfirmed outcome; inspect delivery receipts before replaying")
	}
	if err != nil {
		return h.unavailable()
	}
	sendErr := h.send(ctx, endpoint.BotID.String(), typ, channel.SendRequest{Target: target, Message: channel.Message{Text: n.Text, Format: channel.MessageFormatPlain}})
	status, code := "sent", ""
	if sendErr != nil {
		status, code = "failed", "channel_send_failed"
	}
	// Record the outcome even if the caller's HTTP connection was cancelled.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	if err := h.queries.FinishPushDelivery(saveCtx, sqlc.FinishPushDeliveryParams{ID: receipt.ID, Status: status, ErrorCode: code}); err != nil {
		return h.unavailable()
	}
	if sendErr != nil {
		h.log.Warn("push channel rejected notification", slog.String("endpoint_id", endpoint.ID.String()), slog.String("receipt_id", receipt.ID.String()), slog.String("channel", typ.String()))
		return echo.NewHTTPError(http.StatusBadGateway, "channel did not confirm delivery; inspect channel status and delivery receipts")
	}
	receipt.Status = status
	receipt.UpdatedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	return c.JSON(http.StatusOK, pushReceipt(receipt, false))
}

func (*PushHandler) unavailable() error {
	return echo.NewHTTPError(http.StatusServiceUnavailable, "notification service unavailable")
}
