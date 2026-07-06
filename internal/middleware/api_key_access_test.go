package middleware

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestRejectTenantAPIKeyManagementPath(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleAdmin,
	})

	for _, path := range []string{
		"/api/v1/tenants/1/api-keys",
		"/api/v1/tenants/1/api-key",
		"/api/v1/tenants/1/api-principal-config",
		"/api/v1/tenants/1/api-principal-test-token",
	} {
		err := rejectTenantAPIKeyManagementPath(ctx, path)
		if !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
			t.Fatalf("management path %s error = %v, want errTenantAPIKeyScopeForbidden", path, err)
		}
	}

	if err := rejectTenantAPIKeyManagementPath(ctx, "/api/v1/knowledge-bases"); err != nil {
		t.Fatalf("non-management path should pass: %v", err)
	}
	if err := rejectTenantAPIKeyManagementPath(context.Background(), "/api/v1/tenants/1/api-keys"); err != nil {
		t.Fatalf("non-api-key principal should pass: %v", err)
	}
}

func TestRequireAPIKeyDenyBlocksAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleAdmin,
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-bases", nil)
	c.Request = c.Request.WithContext(ctx)

	RequireAPIKeyDeny()(c)
	if !c.IsAborted() {
		t.Fatal("expected API key request to be aborted")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRequireAPIKeyDenyAllowsJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-bases", nil)

	RequireAPIKeyDeny()(c)
	if c.IsAborted() {
		t.Fatal("expected JWT request to pass RequireAPIKeyDeny")
	}
}

func TestRequireAPIKeyMinRoleForUnsafeSkipsSafeMethods(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleViewer,
	})
	if err := requireTenantAPIKeyMinRole(ctx, types.TenantRoleAdmin); !stderrors.Is(err, errTenantAPIKeyScopeForbidden) {
		t.Fatalf("viewer should not satisfy admin min role: %v", err)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	c.Request = c.Request.WithContext(ctx)

	RequireAPIKeyMinRoleForUnsafe(types.TenantRoleAdmin)(c)
	if c.IsAborted() {
		t.Fatal("safe GET should skip unsafe min-role guard")
	}
}

func TestRequireAPIKeyMinRoleEnforcesOnPost(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleViewer,
	})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-search", nil)
	c.Request = c.Request.WithContext(ctx)

	RequireAPIKeyMinRole(types.TenantRoleViewer)(c)
	if c.IsAborted() {
		t.Fatal("viewer key should pass viewer min role on POST")
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
