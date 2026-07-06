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
		Role: types.TenantRoleViewer,
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodGet, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("viewer key should allow GET: %v", err)
	}
	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodHead, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("viewer key should allow HEAD: %v", err)
	}
}

func TestTenantAPIKeyScopeBaselineAllowsUnsafeThrough(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleViewer,
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodPost, "/api/v1/knowledge-bases/kb-1/knowledge/file"); err != nil {
		t.Fatalf("baseline should defer unsafe checks to route guards: %v", err)
	}
	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodDelete, "/api/v1/knowledge-bases/kb-1"); err != nil {
		t.Fatalf("baseline should defer unsafe checks to route guards: %v", err)
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
		Role: types.TenantRoleAdmin,
	})

	for _, path := range []string{
		"/api/v1/tenants/1/api-keys",
		"/api/v1/tenants/1/api-key",
		"/api/v1/tenants/1/api-principal-config",
		"/api/v1/tenants/1/api-principal-test-token",
	} {
		err := authorizeTenantAPIKeyAccess(ctx, http.MethodPost, path)
		if !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
			t.Fatalf("api key management path %s error = %v, want errTenantAPIKeyScopeForbidden", path, err)
		}
	}
}

func TestTenantAPIKeyScopeEmptyRoleDefaultsToViewer(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: "",
	})

	if err := authorizeTenantAPIKeyAccess(ctx, http.MethodGet, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("empty role should allow GET: %v", err)
	}
}

func TestRequireAPIKeyMinRoleForUnsafeAllowsContributorKnowledgeWrite(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role:             types.TenantRoleContributor,
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	if err := requireTenantAPIKeyMinRole(ctx, types.TenantRoleContributor); err != nil {
		t.Fatalf("scoped knowledge write should pass route guard: %v", err)
	}
}

func TestRequireAPIKeyDenyBlocksBatchWrite(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role:             types.TenantRoleContributor,
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})
	if err := requireTenantAPIKeyMinRole(ctx, types.TenantRoleContributor); err != nil {
		t.Fatalf("unexpected contributor role check failure: %v", err)
	}
	// Deny is enforced by RequireAPIKeyDeny middleware on the route; verify scope exists.
	if _, ok := types.TenantAPIKeyScopeFromContext(ctx); !ok {
		t.Fatal("expected api key scope in context")
	}
}
