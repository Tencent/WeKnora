package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// TestAPIKeyGateDeniesTenantKeyManagementPaths guards the default-deny
// contract: tenant lifecycle, key management and API-principal config routes
// are registered without apiKeyRoute/apiKeyGroup, so even an Owner-role API
// key must receive 403 from the gate before JWT-only handlers run.
func TestAPIKeyGateDeniesTenantKeyManagementPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	gate := middleware.NewAPIKeyRouteAuthorizer()
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		scope := types.TenantAPIKeyScope{Role: types.TenantRoleOwner}
		c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), scope))
		c.Next()
	})
	engine.Use(gate.Middleware())

	v1 := engine.Group("/api/v1")
	tenantByID := v1.Group("/tenants/:id")
	{
		tenantByID.POST("/api-key", reachedOK)
		tenantByID.GET("/api-keys", reachedOK)
		tenantByID.POST("/api-keys", reachedOK)
		tenantByID.DELETE("/api-keys/:key_id", reachedOK)
		tenantByID.GET("/api-principal-config", reachedOK)
		tenantByID.PUT("/api-principal-config", reachedOK)
		tenantByID.POST("/api-principal-test-token", reachedOK)
	}
	tenantRoutes := v1.Group("/tenants")
	{
		tenantRoutes.POST("", reachedOK)
	}

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/tenants/42/api-key"},
		{http.MethodGet, "/api/v1/tenants/42/api-keys"},
		{http.MethodPost, "/api/v1/tenants/42/api-keys"},
		{http.MethodDelete, "/api/v1/tenants/42/api-keys/7"},
		{http.MethodGet, "/api/v1/tenants/42/api-principal-config"},
		{http.MethodPut, "/api/v1/tenants/42/api-principal-config"},
		{http.MethodPost, "/api/v1/tenants/42/api-principal-test-token"},
		{http.MethodPost, "/api/v1/tenants"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAPIKeyGateAllowsDeclaredTenantKVRead confirms the gate still permits
// routes that are explicitly declared for Owner API keys (sanity check).
func TestAPIKeyGateAllowsDeclaredTenantKVRead(t *testing.T) {
	gin.SetMode(gin.TestMode)

	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")
	g.apiKeyRoute(v1.Group("/tenants"), http.MethodGet, "/kv/:key", apiKeyOwner(), reachedOK)

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		scope := types.TenantAPIKeyScope{Role: types.TenantRoleOwner}
		c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), scope))
		c.Next()
	})
	engine.Use(g.apiKeyAuthorizer.Middleware())
	engine.GET("/api/v1/tenants/kv/:key", reachedOK)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/kv/my-key", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("declared owner route status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
}

func reachedOK(c *gin.Context) {
	c.Status(http.StatusOK)
}
