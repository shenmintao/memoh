package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/felinics/memoh/internal/channel"
	"github.com/felinics/memoh/internal/db"
	"github.com/felinics/memoh/internal/db/postgres/sqlc"
	dbstore "github.com/felinics/memoh/internal/db/store"
	"github.com/felinics/memoh/internal/push"
)

type pushTestStore struct {
	dbstore.Queries
	mu       sync.Mutex
	endpoint sqlc.PushEndpoint
	receipts map[string]sqlc.PushDelivery
}

func (s *pushTestStore) GetPushEndpoint(_ context.Context, id pgtype.UUID) (sqlc.PushEndpoint, error) {
	if id != s.endpoint.ID {
		return sqlc.PushEndpoint{}, pgx.ErrNoRows
	}
	return s.endpoint, nil
}
func (*pushTestStore) PrunePushDeliveries(context.Context, pgtype.UUID) error { return nil }
func (s *pushTestStore) ClaimPushDelivery(_ context.Context, p sqlc.ClaimPushDeliveryParams) (sqlc.PushDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.receipts[p.DedupeKey]
	if ok && (old.Status != "failed" || old.PayloadHash != p.PayloadHash) {
		return sqlc.PushDelivery{}, pgx.ErrNoRows
	}
	row := sqlc.PushDelivery{ID: s.endpoint.ID, EndpointID: p.EndpointID, DedupeKey: p.DedupeKey, PayloadHash: p.PayloadHash, Status: "sending", Attempts: old.Attempts + 1}
	s.receipts[p.DedupeKey] = row
	return row, nil
}

func (s *pushTestStore) GetPushDelivery(_ context.Context, p sqlc.GetPushDeliveryParams) (sqlc.PushDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receipts[p.DedupeKey], nil
}

func (s *pushTestStore) FinishPushDelivery(_ context.Context, p sqlc.FinishPushDeliveryParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, row := range s.receipts {
		if row.ID == p.ID {
			row.Status, row.ErrorCode = p.Status, p.ErrorCode
			s.receipts[key] = row
		}
	}
	return nil
}

func TestPushScopedAuthDeduplicationAndFailure(t *testing.T) {
	id, _ := db.ParseUUID("d74b5339-1586-4ac3-adf0-fb1e58baf600")
	key := newPushKey()
	store := &pushTestStore{endpoint: sqlc.PushEndpoint{ID: id, BotID: id, Enabled: true, TokenHash: push.Hash(key)}, receipts: make(map[string]sqlc.PushDelivery)}
	sends := 0
	allowed, fail := true, false
	h := &PushHandler{queries: store, log: slog.Default(), rates: make(map[string]pushRateEntry)}
	h.authorize = func(context.Context, sqlc.PushEndpoint) (channel.ChannelType, string, error) {
		if !allowed {
			return "", "", echo.ErrForbidden
		}
		return "weixin", "bound-recipient", nil
	}
	h.send = func(_ context.Context, bot string, platform channel.ChannelType, req channel.SendRequest) error {
		if bot != id.String() || platform != "weixin" || req.Target != "bound-recipient" || req.ChannelIdentityID != "" {
			t.Fatal("caller changed destination")
		}
		sends++
		if fail {
			return errors.New("private channel failure")
		}
		return nil
	}
	e := echo.New()
	h.Register(e)
	request := func(secret, event, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/push/"+id.String()+"?key="+secret, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", event)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	for _, secret := range []string{"", "mpk_invalid"} {
		if r := request(secret, "1", "{\"text\":\"hello\"}"); r.Code != 401 {
			t.Fatal(r.Code)
		}
	}
	body := "{\"text\":\"hello\",\"target\":\"attacker\",\"bot_id\":\"attacker\"}"
	if r := request(key, "event1", body); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if r := request(key, "event1", body); r.Code != 200 || !strings.Contains(r.Body.String(), "\"duplicate\":true") || sends != 1 {
		t.Fatalf("duplicate sent again: %s", r.Body)
	}
	if r := request(key, "event1", "{\"text\":\"different\"}"); r.Code != 409 {
		t.Fatal("event ID content collision accepted")
	}
	allowed = false
	if r := request(key, "event2", body); r.Code != 403 || sends != 1 {
		t.Fatal("revoked binding still sent")
	}
	allowed = true
	store.endpoint.Enabled = false
	if r := request(key, "event2", body); r.Code != 403 {
		t.Fatal("paused endpoint accepted")
	}
	store.endpoint.Enabled = true
	fail = true
	if r := request(key, "event2", body); r.Code != 502 || strings.Contains(r.Body.String(), "private channel") {
		t.Fatalf("false success or leaked error: %s", r.Body)
	}
	fail = false
	if r := request(key, "event2", body); r.Code != 200 {
		t.Fatalf("failed delivery cannot retry: %s", r.Body)
	}
	store.endpoint.TokenHash = push.Hash(newPushKey())
	if r := request(key, "event3", body); r.Code != 401 {
		t.Fatal("rotated key still works")
	}
}
