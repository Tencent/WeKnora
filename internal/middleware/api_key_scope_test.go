package middleware

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestTenantAPIKeyScopeReadOnlyAllowsSafeMethods(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{types.TenantAPIKeyScopeRead},
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodGet, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("read scoped key should allow GET: %v", err)
	}
	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodHead, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("read scoped key should allow HEAD: %v", err)
	}
}

func TestTenantAPIKeyScopeReadOnlyRejectsUnsafeMethods(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{types.TenantAPIKeyScopeRead},
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodPost, "/api/v1/knowledge-bases/kb-1/knowledge/file"); !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("read scoped key POST error = %v, want errTenantAPIKeyScopeForbidden", err)
	}
	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodDelete, "/api/v1/knowledge-bases/kb-1"); !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("read scoped key DELETE error = %v, want errTenantAPIKeyScopeForbidden", err)
	}
}

func TestTenantAPIKeyScopeReadOnlyAllowsSemanticReadPost(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{types.TenantAPIKeyScopeRead},
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodPost, "/api/v1/knowledge-search"); err != nil {
		t.Fatalf("read scoped key should allow semantic read POST: %v", err)
	}
}

func TestTenantAPIKeyScopeAllowsScopedKnowledgeBase(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})

	if err := authorizeTenantAPIKeyKnowledgeBase(ctx, "kb-1"); err != nil {
		t.Fatalf("key scoped to kb-1 should allow kb-1: %v", err)
	}
	if err := authorizeTenantAPIKeyKnowledgeBase(ctx, "kb-2"); !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("key scoped to kb-1 should reject kb-2, got %v", err)
	}
}

func TestTenantAPIKeyRouteRejectsAPIKeyManagement(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{types.TenantAPIKeyScopeAdmin},
	})

	err := authorizeTenantAPIKeyAccess(ctx, http.MethodPost, "/api/v1/tenants/1/api-keys")
	if !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("api key management error = %v, want errTenantAPIKeyScopeForbidden", err)
	}
}

func TestTenantAPIKeyRouteRejectsTenantWriteForKnowledgeRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes:           types.StringArray{types.TenantAPIKeyScopeWrite},
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})

	err := authorizeTenantAPIKeyAccess(ctx, http.MethodPut, "/api/v1/tenants/kv/theme")
	if !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("tenant write error = %v, want errTenantAPIKeyScopeForbidden", err)
	}
}

func TestTenantAPIKeyRouteRejectsTenantWriteForWriteScope(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{types.TenantAPIKeyScopeWrite},
	})

	err := authorizeTenantAPIKeyAccess(ctx, http.MethodPut, "/api/v1/tenants/1")
	if !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("tenant write error = %v, want errTenantAPIKeyScopeForbidden", err)
	}
}

func TestTenantAPIKeyRouteAllowsTenantWriteForAdminScope(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{types.TenantAPIKeyScopeAdmin},
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodPut, "/api/v1/tenants/kv/theme"); err != nil {
		t.Fatalf("admin scoped key should allow tenant management route: %v", err)
	}
}

func TestTenantAPIKeyRouteAllowsKnowledgeWriteForKnowledgeRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes:           types.StringArray{types.TenantAPIKeyScopeWrite},
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodPut, "/api/v1/knowledge-bases/kb-1"); err != nil {
		t.Fatalf("scoped knowledge write should pass route layer: %v", err)
	}
}

func TestTenantAPIKeyRouteRejectsCrossKnowledgeBatchWriteForKnowledgeRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes:           types.StringArray{types.TenantAPIKeyScopeWrite},
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})

	err := authorizeTenantAPIKeyAccess(ctx, http.MethodPost, "/api/v1/knowledge/batch-delete")
	if !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("batch write error = %v, want errTenantAPIKeyScopeForbidden", err)
	}
}

func TestTenantAPIKeyScopeEmptyScopesDefaultToReadOnly(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Scopes: types.StringArray{},
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodPut, "/api/v1/tenants/kv/theme"); !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("empty scopes should default to read-only, got %v", err)
	}
	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodGet, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("empty scopes should allow GET: %v", err)
	}
}
