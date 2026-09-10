package handlers

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/felinics/memoh/internal/auth"
	mcpgw "github.com/felinics/memoh/internal/mcp"
)

const (
	headerBotID             = mcpgw.ToolHeaderBotID
	headerChatID            = mcpgw.ToolHeaderChatID
	headerSessionID         = mcpgw.ToolHeaderSessionID
	headerRunID             = mcpgw.ToolHeaderRunID
	headerSessionType       = mcpgw.ToolHeaderSessionType
	headerRouteID           = mcpgw.ToolHeaderRouteID
	headerChannelIdentityID = mcpgw.ToolHeaderChannelIdentityID
	headerSessionToken      = mcpgw.ToolHeaderSessionToken
	headerCurrentPlatform   = mcpgw.ToolHeaderCurrentPlatform
	headerReplyTarget       = mcpgw.ToolHeaderReplyTarget
	headerConversationType  = mcpgw.ToolHeaderConversationType
	headerIsSubagent        = mcpgw.ToolHeaderIsSubagent
)

func (h *ContainerdHandler) SetToolGatewayService(service *mcpgw.ToolGatewayService) {
	h.toolGateway = service
}

func (h *ContainerdHandler) SetToolSessionContextStore(store *mcpgw.ToolSessionContextStore) {
	h.toolContexts = store
}

// acpRuntimeContextResolver resolves the trusted tool context of a live ACP
// runtime from its stable, server-generated runtime ID.
type acpRuntimeContextResolver interface {
	ResolveRuntimeToolContext(botID, runtimeID, toolToken string) (mcpgw.ToolSessionContext, bool)
}

func (h *ContainerdHandler) SetACPRuntimeResolver(resolver acpRuntimeContextResolver) {
	h.acpRuntimes = resolver
}

// HandleMCPTools godoc
// @Summary Unified MCP tools gateway
// @Description MCP endpoint for tool discovery and invocation.
// @Tags containerd
// @Param bot_id path string true "Bot ID"
// @Param payload body object true "JSON-RPC request"
// @Success 200 {object} object "JSON-RPC response: {jsonrpc,id,result|error}"
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /bots/{bot_id}/tools [post].
func (h *ContainerdHandler) HandleMCPTools(c echo.Context) error {
	if h.toolGateway == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "tool gateway not configured")
	}
	botID, err := h.requireBotAccessWithGuest(c)
	if err != nil {
		return err
	}
	return h.handleMCPToolsWithBotID(c, botID)
}

func (h *ContainerdHandler) handleMCPToolsWithBotID(c echo.Context, botID string) error {
	// ACP runtimes reference themselves by stable, server-generated runtime
	// identity; their per-prompt context resolves from the live handle and
	// the bot in the path must own the runtime. Fails closed: a dead or
	// foreign runtime never falls back to header-supplied identity.
	if runtimeID := strings.TrimSpace(c.Request().Header.Get(mcpgw.ToolHeaderRuntimeID)); runtimeID != "" {
		return h.serveRuntimeMCPTools(c, botID)
	}
	session := h.buildToolSessionContext(c, botID)
	mcpgw.ServeToolMCPHTTP(c.Response().Writer, c.Request(), h.logger, h.toolGateway, h.toolContexts, session)
	return nil
}

// HandleRuntimeMCPTools authenticates ACP requests using the live runtime's
// scoped token. This dedicated route does not require an account JWT and must
// never fall back to header-supplied identity or the public tool context.
func (h *ContainerdHandler) HandleRuntimeMCPTools(c echo.Context) error {
	return h.serveRuntimeMCPTools(c, strings.TrimSpace(c.Param("bot_id")))
}

func (h *ContainerdHandler) serveRuntimeMCPTools(c echo.Context, botID string) error {
	runtimeID := strings.TrimSpace(c.Request().Header.Get(mcpgw.ToolHeaderRuntimeID))
	token := strings.TrimSpace(c.Request().Header.Get(mcpgw.ToolHeaderRuntimeToken))
	if botID == "" || runtimeID == "" || token == "" || h.acpRuntimes == nil {
		return echo.NewHTTPError(http.StatusNotFound, "runtime not found")
	}
	session, ok := h.acpRuntimes.ResolveRuntimeToolContext(botID, runtimeID, token)
	if !ok || session.BotID != botID || session.RuntimeID != runtimeID {
		return echo.NewHTTPError(http.StatusNotFound, "runtime not found")
	}
	mcpgw.ServeToolMCPHTTP(c.Response().Writer, c.Request(), h.logger, h.toolGateway, h.toolContexts, session)
	return nil
}

func buildToolCallPayloadFromRaw(params *sdkmcp.CallToolParamsRaw) (mcpgw.ToolCallPayload, error) {
	return mcpgw.BuildToolCallPayloadFromRaw(params)
}

func (*ContainerdHandler) buildToolSessionContext(c echo.Context, botID string) mcpgw.ToolSessionContext {
	botID = strings.TrimSpace(botID)
	session := mcpgw.ToolSessionContextFromHTTP(c.Request(), botID)
	session.BotID = botID
	session.RuntimeToken = ""
	session.ChannelIdentityID = ""
	session.SessionToken = ""
	// Public MCP clients cannot prove model-native image transport support.
	// Keep this capability limited to server-resolved runtime contexts until
	// the native MCP image path is wired end-to-end.
	session.SupportsImageInput = false
	if ctxIdentityID, err := auth.UserIDFromContext(c); err == nil {
		session.ChannelIdentityID = strings.TrimSpace(ctxIdentityID)
	}
	return session
}
