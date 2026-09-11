package weixin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felinics/memoh/internal/channel"
)

func TestContextSurvivesRestartWithoutCrossingAccounts(t *testing.T) {
	cfg := channel.ChannelConfig{ID: "config-a", Credentials: map[string]any{"token": "account-a"}, Routing: map[string]any{}}
	first := NewWeixinAdapter(nil)
	first.SetContextTokenSaver(func(_ context.Context, id, target, token, hash, account string) error {
		if id != cfg.ID || account != "account-a" {
			t.Fatal("wrong persistence scope")
		}
		cfg.Routing["_weixin_contexts"] = map[string]any{target: map[string]any{"token": token, "account_hash": hash}}
		return nil
	})
	first.rememberContext(context.Background(), cfg, "recipient", "context-a")
	restarted := NewWeixinAdapter(nil)
	if token, ok := restarted.resolveContext(cfg, "recipient"); !ok || token != "context-a" {
		t.Fatal("lost token on restart")
	}
	if _, ok := restarted.resolveContext(cfg, "other-recipient"); ok {
		t.Fatal("crossed recipients")
	}
	cfg.Credentials["token"] = "account-b"
	if _, ok := restarted.resolveContext(cfg, "recipient"); ok {
		t.Fatal("reused context after changing WeChat login")
	}
	other := channel.ChannelConfig{ID: "config-b", Credentials: map[string]any{"token": "account-a"}}
	if _, ok := restarted.resolveContext(other, "recipient"); ok {
		t.Fatal("crossed bot configs")
	}
}

func TestSendMessageChecksBusinessResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		success    bool
	}{
		{"success", "{\"ret\":0}", true},
		{"message id", "{\"message_id\":\"sent-id\"}", true},
		{"numeric message id", "{\"message_id\":1234}", true},
		{"rejected", "{\"ret\":-2,\"errmsg\":\"private payload must not leak\"}", false},
		{"expired", "{\"ret\":0,\"errcode\":-14}", false},
		{"malformed", "<html>bad gateway</html>", false},
		{"empty", "", false},
		{"empty object", "{}", false},
		{"null", "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			err := NewClient(nil).SendMessage(context.Background(), adapterConfig{BaseURL: srv.URL, Token: "account"}, SendMessageRequest{})
			if (err == nil) != tc.success {
				t.Fatalf("err=%v success=%v", err, tc.success)
			}
			if err != nil && strings.Contains(err.Error(), "private payload") {
				t.Fatal("leaked business response")
			}
		})
	}
}
