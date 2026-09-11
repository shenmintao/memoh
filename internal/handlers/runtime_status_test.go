package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/felinics/memoh/internal/db"
	dbstore "github.com/felinics/memoh/internal/db/store"
	"github.com/felinics/memoh/internal/userruntime"
	"github.com/felinics/memoh/internal/workspace/bridge"
)

type runtimeStatusErrorStore struct {
	dbstore.UserRuntimeStore
	err error
}

func (s *runtimeStatusErrorStore) GetUserRuntimeByAPIToken(context.Context, string) (dbstore.UserRuntimeRecord, error) {
	return dbstore.UserRuntimeRecord{}, s.err
}

func TestRuntimeStatusDistinguishesRevokedKeyFromUnavailableStore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"revoked", db.ErrNotFound, http.StatusUnauthorized},
		{"unavailable", errors.New("database unavailable"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			service := userruntime.NewService(&runtimeStatusErrorStore{err: tc.err}, userruntime.NewHub(nil))
			NewRuntimeConnectHandler(nil, service, nil).Register(e)
			req := httptest.NewRequest(http.MethodGet, "/runtimes/status", nil)
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+runtimeConnectTestKey)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestRuntimeStatusRequiresOwnKeyAndTracksHub(t *testing.T) {
	t.Parallel()
	store := newRuntimeConnectTestStore()
	hub := userruntime.NewHub(nil)
	service := userruntime.NewService(store, hub)
	e := echo.New()
	NewRuntimeConnectHandler(nil, service, nil).Register(e)
	request := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/runtimes/status?runtime_id=another-device", nil)
		if key != "" {
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	for _, key := range []string{"", "invalid", "mrk_" + strings.Repeat("3", 64)} {
		if rec := request(key); rec.Code != http.StatusUnauthorized {
			t.Fatalf("invalid credential status = %d", rec.Code)
		}
	}
	assertStatus := func(online bool) {
		t.Helper()
		rec := request(runtimeConnectTestKey)
		if rec.Code != http.StatusOK || rec.Header().Get(echo.HeaderCacheControl) != "no-store" {
			t.Fatalf("status = %d, headers = %v", rec.Code, rec.Header())
		}
		var result RuntimeConnectionStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ID != runtimeConnectTestID || result.Online != online || result.CheckedAt.IsZero() {
			t.Fatalf("unexpected response: %+v", result)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 3 || strings.Contains(rec.Body.String(), runtimeConnectTestKey) {
			t.Fatal("status response contains unexpected fields or credentials")
		}
	}
	assertStatus(false)
	conn, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client := bridge.NewClientFromConn(conn)
	t.Cleanup(func() { _ = client.Close() })
	connection := &userruntime.Connection{ConnectionID: "status-test", Client: client}
	err = service.ActivateConnection(context.Background(), runtimeConnectTestKey, runtimeConnectTestID,
		userruntime.HandshakeInfo{}, connection, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(true)
	service.DeactivateConnection(runtimeConnectTestID, connection, "test disconnected")
	assertStatus(false)
}
