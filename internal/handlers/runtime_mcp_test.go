package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	mcpgw "github.com/felinics/memoh/internal/mcp"
)

func TestRuntimeMCPRouteAuthenticatesWithoutAccountJWT(t *testing.T) {
	for _, tc := range []struct {
		name, bot, runtime, token string
		live                      bool
		want                      int
	}{
		{"valid", "bot-1", "runtime-1", "scoped-token", true, 200},
		{"anonymous", "bot-1", "", "", true, 404},
		{"missing token", "bot-1", "runtime-1", "", true, 404},
		{"forged token", "bot-1", "runtime-1", "wrong", true, 404},
		{"foreign bot", "bot-2", "runtime-1", "scoped-token", true, 404},
		{"foreign runtime", "bot-1", "runtime-2", "scoped-token", true, 404},
		{"exited runtime", "bot-1", "runtime-1", "scoped-token", false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &mcpToolsTestExecutor{}
			h := &ContainerdHandler{
				logger:      slog.Default(),
				toolGateway: mcpgw.NewToolGatewayService(slog.Default(), []mcpgw.ToolSource{executor}),
				acpRuntimes: mcpToolsRuntimeResolver{session: mcpgw.ToolSessionContext{
					BotID: "bot-1", RuntimeID: "runtime-1", RuntimeToken: "scoped-token",
					ChannelIdentityID: "trusted-user", RuntimeActive: true,
				}, ok: tc.live},
			}
			e := echo.New()
			e.POST("/bots/:bot_id/runtime-tools", h.HandleRuntimeMCPTools)
			req := httptest.NewRequest(http.MethodPost, "/bots/"+tc.bot+"/runtime-tools", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo_tool","arguments":{"input":"hello"}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(mcpgw.ToolHeaderRuntimeID, tc.runtime)
			req.Header.Set(mcpgw.ToolHeaderRuntimeToken, tc.token)
			req.Header.Set(mcpgw.ToolHeaderChannelIdentityID, "spoofed-user")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == 200 && executor.lastSession.ChannelIdentityID != "trusted-user" {
				t.Fatal("trusted runtime identity was not used")
			}
		})
	}
}
