package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/felinics/memoh/internal/db"
	"github.com/felinics/memoh/internal/userruntime"
)

// RuntimeConnectionStatus exposes only the authenticated device's connection.
type RuntimeConnectionStatus struct {
	ID        string    `json:"id"`
	Online    bool      `json:"online"`
	CheckedAt time.Time `json:"checked_at"`
}

// Status godoc
// @Summary Get this Remote Runtime's server-side connection status
// @Description Authenticate with this device's Runtime Key. Returns the same live connection state used by the computer list, without credentials or other devices.
// @Tags user-runtimes
// @Produce json
// @Param Authorization header string true "Bearer Runtime Key"
// @Success 200 {object} RuntimeConnectionStatus
// @Failure 401 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /runtimes/status [get].
func (h *RuntimeConnectHandler) Status(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	key, err := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "runtime key is required")
	}
	runtime, err := h.service.AuthenticateKey(c.Request().Context(), key)
	if err != nil {
		if errors.Is(err, userruntime.ErrInvalidKey) || errors.Is(err, db.ErrNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid runtime key")
		}
		return echo.NewHTTPError(http.StatusServiceUnavailable, "runtime status is temporarily unavailable")
	}
	return c.JSON(http.StatusOK, RuntimeConnectionStatus{
		ID: runtime.ID, Online: runtime.Online, CheckedAt: time.Now().UTC(),
	})
}
