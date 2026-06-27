package handler

import (
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPOAuthHandler exposes the per-user MCP OAuth2 authorization-code flow:
// kicking off authorization (discovery + dynamic client registration + PKCE),
// receiving the provider redirect, reporting authorization status, and
// revoking a user's token.
type MCPOAuthHandler struct {
	oauth      *mcp.OAuthManager
	mcpManager *mcp.MCPManager
	svc        interfaces.MCPServiceService
}

// NewMCPOAuthHandler constructs the handler.
func NewMCPOAuthHandler(
	oauth *mcp.OAuthManager,
	mcpManager *mcp.MCPManager,
	svc interfaces.MCPServiceService,
) *MCPOAuthHandler {
	return &MCPOAuthHandler{oauth: oauth, mcpManager: mcpManager, svc: svc}
}

type mcpOAuthAuthorizeRequest struct {
	// RedirectURI is the absolute backend callback URL registered with the
	// authorization server (e.g. https://host/api/v1/mcp-services/oauth/callback).
	RedirectURI string `json:"redirect_uri"`
	// FrontendRedirect is where the callback bounces the browser when done
	// (e.g. the MCP settings page). Optional; defaults to "/".
	FrontendRedirect string `json:"frontend_redirect"`
}

// AuthorizeURL begins authorization and returns the URL the browser must open.
//
// AuthorizeURL godoc
// @Summary      发起 MCP OAuth 授权
// @Description  对使用 OAuth 的 MCP 服务执行发现与动态客户端注册，返回浏览器应跳转的授权地址（当前用户维度）
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id       path      string                    true  "MCP 服务 ID"
// @Param        request  body      map[string]interface{}    true  "{redirect_uri: string, frontend_redirect?: string}"
// @Success      200      {object}  map[string]interface{}    "{authorization_url: string}"
// @Failure      400      {object}  errors.AppError
// @Security     Bearer
// @Router       /mcp-services/{id}/oauth/authorize-url [post]
func (h *MCPOAuthHandler) AuthorizeURL(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	userID, _ := types.UserIDFromContext(ctx)
	if tenantID == 0 || userID == "" {
		c.Error(errors.NewUnauthorizedError("authentication required"))
		return
	}

	var req mcpOAuthAuthorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	req.RedirectURI = strings.TrimSpace(req.RedirectURI)
	if req.RedirectURI == "" {
		c.Error(errors.NewValidationError("redirect_uri is required"))
		return
	}
	if req.FrontendRedirect == "" {
		req.FrontendRedirect = "/"
	}

	service, err := h.svc.GetMCPServiceByID(ctx, tenantID, serviceID)
	if err != nil || service == nil {
		c.Error(errors.NewNotFoundError("MCP service not found"))
		return
	}
	if !service.AuthConfig.IsOAuth() {
		c.Error(errors.NewValidationError("MCP service is not configured to use OAuth"))
		return
	}

	authURL, err := h.oauth.StartAuthorization(ctx, service, tenantID, userID, req.RedirectURI, req.FrontendRedirect)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"service_id": secutils.SanitizeForLog(serviceID),
		})
		c.Error(errors.NewInternalServerError("failed to start authorization: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"authorization_url": authURL}})
}

// Callback receives the authorization-server redirect. It is registered as a
// public (no-bearer) route; the opaque single-use `state` parameter
// authenticates the request. On completion it redirects the browser back to
// the frontend with the result encoded in the URL fragment.
//
// Callback godoc
// @Summary      MCP OAuth 回调
// @Description  接收授权服务器回调并完成 code 交换，随后重定向回前端
// @Tags         MCP服务
// @Param        code   query  string  false  "授权码"
// @Param        state  query  string  false  "状态参数"
// @Param        error  query  string  false  "授权错误码"
// @Success      302
// @Router       /mcp-services/oauth/callback [get]
func (h *MCPOAuthHandler) Callback(c *gin.Context) {
	ctx := c.Request.Context()
	state := strings.TrimSpace(c.Query("state"))
	code := strings.TrimSpace(c.Query("code"))
	providerErr := strings.TrimSpace(c.Query("error"))

	const fallbackRedirect = "/"

	if providerErr != "" {
		c.Redirect(http.StatusFound, fallbackRedirect+"#mcp_oauth_error="+urlQueryEscape(providerErr))
		return
	}
	if state == "" || code == "" {
		c.Redirect(http.StatusFound, fallbackRedirect+"#mcp_oauth_error="+urlQueryEscape("missing_code_or_state"))
		return
	}

	frontendRedirect, err := h.oauth.CompleteAuthorization(ctx, state, code)
	if frontendRedirect == "" {
		frontendRedirect = fallbackRedirect
	}
	if err != nil {
		logger.Errorf(ctx, "MCP OAuth callback failed: %v", err)
		c.Redirect(http.StatusFound, frontendRedirect+"#mcp_oauth_error="+urlQueryEscape("authorization_failed"))
		return
	}
	c.Redirect(http.StatusFound, frontendRedirect+"#mcp_oauth_result=success")
}

// Status reports whether the current user has authorized this service.
//
// Status godoc
// @Summary      查询 MCP OAuth 授权状态
// @Description  返回当前用户对指定 MCP 服务是否已完成 OAuth 授权
// @Tags         MCP服务
// @Produce      json
// @Param        id   path      string                  true  "MCP 服务 ID"
// @Success      200  {object}  map[string]interface{}  "{authorized: bool}"
// @Security     Bearer
// @Router       /mcp-services/{id}/oauth/status [get]
func (h *MCPOAuthHandler) Status(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	userID, _ := types.UserIDFromContext(ctx)
	if tenantID == 0 || userID == "" {
		c.Error(errors.NewUnauthorizedError("authentication required"))
		return
	}

	authorized, err := h.oauth.IsAuthorized(ctx, tenantID, userID, serviceID)
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to query authorization status: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"authorized": authorized}})
}

// Revoke removes the current user's stored token and recycles connections.
//
// Revoke godoc
// @Summary      撤销 MCP OAuth 授权
// @Description  删除当前用户对指定 MCP 服务的 OAuth 令牌
// @Tags         MCP服务
// @Produce      json
// @Param        id   path  string  true  "MCP 服务 ID"
// @Success      204
// @Security     Bearer
// @Router       /mcp-services/{id}/oauth/token [delete]
func (h *MCPOAuthHandler) Revoke(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	userID, _ := types.UserIDFromContext(ctx)
	if tenantID == 0 || userID == "" {
		c.Error(errors.NewUnauthorizedError("authentication required"))
		return
	}

	if err := h.oauth.Revoke(ctx, tenantID, userID, serviceID); err != nil {
		c.Error(errors.NewInternalServerError("failed to revoke authorization: " + err.Error()))
		return
	}
	// Recycle any cached connections so a subsequent call re-authorizes.
	_ = h.mcpManager.CloseClient(serviceID)
	c.Status(http.StatusNoContent)
}
