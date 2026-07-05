package middleware

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var errTenantAPIKeyScopeForbidden = stderrors.New("tenant api key scope forbidden")

func authorizeTenantAPIKeyOperation(ctx context.Context, method string) error {
	if _, ok := types.TenantAPIKeyScopeFromContext(ctx); !ok {
		return nil
	}
	return nil
}

func authorizeTenantAPIKeyRoute(ctx context.Context, method, path string) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok {
		return nil
	}
	if strings.Contains(path, "/api-keys") {
		return errTenantAPIKeyScopeForbidden
	}
	if isSafeHTTPMethod(method) {
		if allowsTenantAPIKeyRead(scope) {
			return nil
		}
		return errTenantAPIKeyScopeForbidden
	}
	if isTenantAPIKeyReadUnsafePath(method, path) && allowsTenantAPIKeyRead(scope) {
		return nil
	}
	if isKnowledgeScopedUnsafePath(path) && allowsTenantAPIKeyWrite(scope) {
		return nil
	}
	if allowsTenantAPIKeyAdmin(scope) {
		return nil
	}
	return errTenantAPIKeyScopeForbidden
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

func isKnowledgeScopedUnsafePath(path string) bool {
	denyPrefixes := []string{
		"/api/v1/knowledge-bases/copy",
		"/api/v1/knowledge/tags",
		"/api/v1/knowledge/batch-reparse",
		"/api/v1/knowledge/batch-delete",
		"/api/v1/knowledge/move",
	}
	for _, prefix := range denyPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}

	if path == "/api/v1/sessions" {
		return true
	}
	allowPrefixes := []string{
		"/api/v1/knowledge-bases/",
		"/api/v1/knowledge/",
		"/api/v1/chunks/",
		"/api/v1/knowledgebase/",
		"/api/v1/knowledge-search",
		"/api/v1/knowledge-chat/",
	}
	for _, prefix := range allowPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func allowsTenantAPIKeyRead(scope types.TenantAPIKeyScope) bool {
	scope = scope.Normalize()
	return len(scope.Scopes) == 0 ||
		scope.HasScope(types.TenantAPIKeyScopeRead) ||
		scope.HasScope(types.TenantAPIKeyScopeWrite) ||
		scope.HasScope(types.TenantAPIKeyScopeAdmin)
}

func allowsTenantAPIKeyWrite(scope types.TenantAPIKeyScope) bool {
	scope = scope.Normalize()
	return len(scope.Scopes) == 0 ||
		scope.HasScope(types.TenantAPIKeyScopeWrite) ||
		scope.HasScope(types.TenantAPIKeyScopeAdmin)
}

func allowsTenantAPIKeyAdmin(scope types.TenantAPIKeyScope) bool {
	scope = scope.Normalize()
	return len(scope.Scopes) == 0 || scope.HasScope(types.TenantAPIKeyScopeAdmin)
}

func isTenantAPIKeyReadUnsafePath(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	if path == "/api/v1/sessions" ||
		path == "/api/v1/knowledge-search" ||
		path == "/api/v1/messages/search" ||
		path == "/api/v1/chunker/preview" {
		return true
	}
	readPrefixes := []string{
		"/api/v1/knowledge-chat/",
		"/api/v1/agent-chat/",
	}
	for _, prefix := range readPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	readSuffixes := []string{
		"/hybrid-search",
		"/faq/search",
	}
	for _, suffix := range readSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}
