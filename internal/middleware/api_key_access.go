package middleware

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

var errTenantAPIKeyScopeForbidden = stderrors.New("tenant api key scope forbidden")

// isTenantAPIKeyManagementPath reports tenant API-key lifecycle endpoints that
// no scoped key may call, regardless of role.
func isTenantAPIKeyManagementPath(path string) bool {
	for _, marker := range []string{"/api-keys", "/api-principal"} {
		if strings.Contains(path, marker) {
			return true
		}
	}
	// Legacy reset endpoint: POST /tenants/:id/api-key (singular).
	return strings.HasSuffix(path, "/api-key")
}

// rejectTenantAPIKeyManagementPath is a defense-in-depth guard in auth: API keys
// must never reach key-management routes even if a route forgets APIKeyDeny().
// Role and KB checks are enforced by per-route APIKey* guards and types helpers.
func rejectTenantAPIKeyManagementPath(ctx context.Context, path string) error {
	if _, ok := types.TenantAPIKeyScopeFromContext(ctx); !ok {
		return nil
	}
	if isTenantAPIKeyManagementPath(path) {
		return errTenantAPIKeyScopeForbidden
	}
	return nil
}

// RequireAPIKeyDeny rejects all X-API-Key callers. JWT sessions pass through.
func RequireAPIKeyDeny() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := types.TenantAPIKeyScopeFromContext(c.Request.Context()); ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: API key scope does not allow this operation",
			})
			return
		}
		c.Next()
	}
}

// RequireAPIKeyMinRole enforces minRole for X-API-Key callers on every HTTP
// method. JWT sessions pass through.
func RequireAPIKeyMinRole(minRole types.TenantRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := requireTenantAPIKeyMinRole(c.Request.Context(), minRole); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: API key scope does not allow this operation",
			})
			return
		}
		c.Next()
	}
}

// RequireAPIKeyMinRoleForUnsafe enforces minRole for X-API-Key callers on
// POST/PUT/PATCH/DELETE only. Safe reads (GET/HEAD/OPTIONS) pass through so
// group-level guards can cover mixed read/write route trees.
func RequireAPIKeyMinRoleForUnsafe(minRole types.TenantRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeHTTPMethod(c.Request.Method) {
			c.Next()
			return
		}
		if err := requireTenantAPIKeyMinRole(c.Request.Context(), minRole); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: API key scope does not allow this operation",
			})
			return
		}
		c.Next()
	}
}

func requireTenantAPIKeyMinRole(ctx context.Context, minRole types.TenantRole) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok {
		return nil
	}
	role := scope.Role
	if role == "" {
		role = types.TenantRoleViewer
	}
	if !role.HasPermission(minRole) {
		return errTenantAPIKeyScopeForbidden
	}
	return nil
}

func isSafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
