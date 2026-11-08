package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// MCPEndpointAuth authenticates calls on the workspace MCP server surface
// (/mcp/:endpoint_id). The client presents the endpoint's bearer token; on
// success the request carries the endpoint's workspace, a synthetic machine
// user, an MCP-endpoint principal and an API-key style scope derived from the
// endpoint's tool allowlist and knowledge-base scope, so every downstream
// service applies the same checks it applies to a scoped API key.
//
// A personal endpoint acts as its owner instead of the synthetic user: the
// owner must still be an active member, and their current role replaces the
// fixed Viewer role, so demoting or removing them takes effect immediately.
func MCPEndpointAuth(
	svc interfaces.MCPEndpointService,
	tenantSvc interfaces.TenantService,
	memberSvc interfaces.TenantMemberService,
	userSvc interfaces.UserService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		endpointID := strings.TrimSpace(c.Param("endpoint_id"))
		token := extractMCPEndpointToken(c)
		if endpointID == "" || token == "" {
			c.Header("WWW-Authenticate", `Bearer realm="weknora-mcp"`)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: missing bearer token"})
			c.Abort()
			return
		}
		ep, err := svc.Authenticate(c.Request.Context(), endpointID, token)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrMCPEndpointDisabled):
				c.JSON(http.StatusForbidden, gin.H{"error": "mcp endpoint disabled"})
			case errors.Is(err, service.ErrMCPEndpointTokenInvalid), errors.Is(err, service.ErrMCPEndpointNotFound):
				c.Header("WWW-Authenticate", `Bearer realm="weknora-mcp"`)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: invalid token"})
			default:
				logger.Warnf(c.Request.Context(), "[mcp-endpoint] auth lookup failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "authentication unavailable"})
			}
			c.Abort()
			return
		}
		tenant, err := tenantSvc.GetTenantByID(c.Request.Context(), ep.TenantID)
		if err != nil || tenant == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "workspace unavailable"})
			c.Abort()
			return
		}
		if strings.EqualFold(tenant.Status, "inactive") || strings.EqualFold(tenant.Status, "disabled") {
			c.JSON(http.StatusForbidden, gin.H{"error": "workspace inactive"})
			c.Abort()
			return
		}

		scope := types.MCPEndpointScope(ep)
		user := &types.User{
			ID:       fmt.Sprintf("mcp-%s", ep.ID),
			Username: fmt.Sprintf("mcp-%s", ep.ID),
			Email:    fmt.Sprintf("mcp-%s@mcp.local", ep.ID),
			TenantID: ep.TenantID,
			IsActive: true,
		}
		role := types.TenantRoleViewer
		if ep.IsPersonal() {
			user, role, err = resolveMCPEndpointOwner(c.Request.Context(), ep, memberSvc, userSvc)
			if err != nil {
				logger.Warnf(c.Request.Context(), "[mcp-endpoint] personal endpoint %s owner rejected: %v", ep.ID, err)
				c.JSON(http.StatusForbidden, gin.H{"error": "mcp endpoint owner is not an active member"})
				c.Abort()
				return
			}
		}
		applyAuthSession(c, authSession{
			User:        user,
			Principal:   types.MCPEndpointPrincipal(ep.TenantID, ep.ID),
			TenantID:    ep.TenantID,
			Tenant:      tenant,
			Role:        role,
			APIKeyScope: &scope,
			Extra:       map[types.ContextKey]any{types.MCPEndpointContextKey: ep},
			AuthSource:  types.AuthSourceMCPEndpoint,
		})
		c.Next()
	}
}

// resolveMCPEndpointOwner loads the member a personal endpoint acts as. Unlike
// the web login path there is no RBAC fail-open or orphan-tenant recovery: an
// endpoint never outlives its owner's active membership.
func resolveMCPEndpointOwner(
	ctx context.Context,
	ep *types.MCPEndpoint,
	memberSvc interfaces.TenantMemberService,
	userSvc interfaces.UserService,
) (*types.User, types.TenantRole, error) {
	if memberSvc == nil || userSvc == nil || ep.UserID == "" {
		return nil, "", errors.New("owner lookup unavailable")
	}
	member, err := memberSvc.GetMembership(ctx, ep.UserID, ep.TenantID)
	if err != nil {
		return nil, "", err
	}
	if member == nil || member.Status != types.TenantMemberStatusActive || !member.Role.IsValid() {
		return nil, "", errors.New("no active membership")
	}
	user, err := userSvc.GetUserByID(ctx, ep.UserID)
	if err != nil {
		return nil, "", err
	}
	if user == nil || !user.IsActive {
		return nil, "", errors.New("user inactive")
	}
	return user, member.Role, nil
}

// extractMCPEndpointToken reads "Authorization: Bearer mcp_..." only. Query
// string tokens are rejected so they never land in access logs.
func extractMCPEndpointToken(c *gin.Context) string {
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// MCPEndpointFromContext returns the endpoint authenticated for this request.
func MCPEndpointFromContext(ctx context.Context) (*types.MCPEndpoint, bool) {
	ep, ok := ctx.Value(types.MCPEndpointContextKey).(*types.MCPEndpoint)
	return ep, ok && ep != nil
}
