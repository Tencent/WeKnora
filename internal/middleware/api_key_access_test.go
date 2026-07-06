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
