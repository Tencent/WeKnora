package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestTenantAPIKeyRouteScopeManifest(t *testing.T) {
	readCtx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleViewer,
	})
	writeCtx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleContributor,
	})
	adminCtx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Role: types.TenantRoleAdmin,
	})

	baselineCases := []struct {
		name    string
		ctx     context.Context
		method  string
		path    string
		allowed bool
	}{
		{name: "read get kb list", ctx: readCtx, method: http.MethodGet, path: "/api/v1/knowledge-bases", allowed: true},
		{name: "read get wiki page", ctx: readCtx, method: http.MethodGet, path: "/api/v1/knowledgebase/kb-1/wiki/pages", allowed: true},
		{name: "admin reset api key blocked", ctx: adminCtx, method: http.MethodPost, path: "/api/v1/tenants/1/api-key", allowed: false},
		{name: "admin api principal blocked", ctx: adminCtx, method: http.MethodGet, path: "/api/v1/tenants/1/api-principal-config", allowed: false},
	}
	for _, tc := range baselineCases {
		t.Run("baseline/"+tc.name, func(t *testing.T) {
			err := authorizeTenantAPIKeyAccess(tc.ctx, tc.method, tc.path)
			got := err == nil
			if got != tc.allowed {
				t.Fatalf("authorizeTenantAPIKeyAccess(%s %s) allowed=%v, want %v (err=%v)",
					tc.method, tc.path, got, tc.allowed, err)
			}
		})
	}

	guardCases := []struct {
		name    string
		ctx     context.Context
		method  string
		guard   gin.HandlerFunc
		allowed bool
	}{
		{name: "read delete kb", ctx: readCtx, method: http.MethodDelete, guard: RequireAPIKeyMinRoleForUnsafe(types.TenantRoleContributor), allowed: false},
		{name: "write put kb", ctx: writeCtx, method: http.MethodPut, guard: RequireAPIKeyMinRoleForUnsafe(types.TenantRoleContributor), allowed: true},
		{name: "write create kb", ctx: writeCtx, method: http.MethodPost, guard: RequireAPIKeyDeny(), allowed: false},
		{name: "write batch delete", ctx: writeCtx, method: http.MethodPost, guard: RequireAPIKeyDeny(), allowed: false},
		{name: "write faq upsert", ctx: writeCtx, method: http.MethodPost, guard: RequireAPIKeyMinRoleForUnsafe(types.TenantRoleContributor), allowed: true},
		{name: "write semantic search post", ctx: writeCtx, method: http.MethodPost, guard: RequireAPIKeyMinRole(types.TenantRoleViewer), allowed: true},
		{name: "read semantic search post", ctx: readCtx, method: http.MethodPost, guard: RequireAPIKeyMinRole(types.TenantRoleViewer), allowed: true},
		{name: "read knowledge chat post", ctx: readCtx, method: http.MethodPost, guard: RequireAPIKeyMinRole(types.TenantRoleViewer), allowed: true},
		{name: "admin tenant kv", ctx: adminCtx, method: http.MethodPut, guard: RequireAPIKeyMinRoleForUnsafe(types.TenantRoleAdmin), allowed: true},
		{name: "contributor tenant kv", ctx: writeCtx, method: http.MethodPut, guard: RequireAPIKeyMinRoleForUnsafe(types.TenantRoleAdmin), allowed: false},
	}
	for _, tc := range guardCases {
		t.Run("guard/"+tc.name, func(t *testing.T) {
			got := runAPIKeyGuard(t, tc.ctx, tc.method, tc.guard)
			if got != tc.allowed {
				t.Fatalf("guard allowed=%v, want %v", got, tc.allowed)
			}
		})
	}
}

func runAPIKeyGuard(t *testing.T, ctx context.Context, method string, guard gin.HandlerFunc) bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/v1/test", nil)
	c.Request = c.Request.WithContext(ctx)
	guard(c)
	return !c.IsAborted() && w.Code != http.StatusForbidden
}
