package middleware

import (
	"context"
	stderrors "errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
)

var errTenantAPIKeyScopeForbidden = stderrors.New("tenant api key scope forbidden")

// authorizeTenantAPIKeyAccess enforces baseline rules for X-API-Key callers
// before route-level guards run: block key-management paths and require
// Viewer+ for safe reads. Unsafe methods defer to per-route APIKey* guards.
func authorizeTenantAPIKeyAccess(ctx context.Context, method, path string) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok {
		return nil
	}
	role := scope.Role
	if role == "" {
		role = types.TenantRoleViewer
	}
	if isTenantAPIKeyManagementPath(path) {
		return errTenantAPIKeyScopeForbidden
	}
	if isSafeHTTPMethod(method) {
		if role.HasPermission(types.TenantRoleViewer) {
			return nil
		}
		return errTenantAPIKeyScopeForbidden
	}
	return nil
}

func authorizeTenantAPIKeyKnowledgeBase(ctx context.Context, kbID string) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok {
		return nil
	}
	if scope.AllowsKnowledgeBase(kbID) {
		return nil
	}
	return errTenantAPIKeyScopeForbidden
}

func isSafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
