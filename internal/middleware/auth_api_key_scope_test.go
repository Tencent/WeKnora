package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// TestAuthenticateAPIKeyScopeUsesRequestContextAfterAttach verifies the
// authorizeTenantAPIKeyAccess call in authenticateAPIKeyRequest reads scope
// from c.Request.Context() after attachAPIKeyAuthContext, not the pre-attach ctx.
func TestAuthenticateAPIKeyScopeUsesRequestContextAfterAttach(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/knowledge-bases/kb-1", nil)

	preAttach := c.Request.Context()
	c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(preAttach, types.TenantAPIKeyScope{
		KeyID:  1,
		Role:   types.TenantRoleViewer,
	}))

	if err := authorizeTenantAPIKeyAccess(preAttach, c.Request.Method, c.Request.URL.Path); err != nil {
		t.Fatalf("pre-attach ctx should not carry scope and must not block: %v", err)
	}
	if err := authorizeTenantAPIKeyAccess(c.Request.Context(), c.Request.Method, c.Request.URL.Path); err != nil {
		t.Fatalf("baseline defers unsafe DELETE to route guards: %v", err)
	}
	if err := requireTenantAPIKeyMinRole(c.Request.Context(), types.TenantRoleContributor); err == nil {
		t.Fatal("viewer key must not satisfy contributor route guard on DELETE")
	}
}
