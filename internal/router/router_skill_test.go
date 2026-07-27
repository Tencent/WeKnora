package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type skillRouteMemberRepository struct {
	interfaces.TenantMemberRepository
	member *types.TenantMember
}

func (repository *skillRouteMemberRepository) Get(context.Context, string, uint64) (*types.TenantMember, error) {
	return repository.member, nil
}

func TestSkillWriteRoutesAlwaysUseDedicatedJWTManagerGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewSkillHandler(service.NewSkillService())
	members := &skillRouteMemberRepository{member: &types.TenantMember{
		UserID: "viewer", TenantID: 7, Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive,
	}}
	engine := gin.New()
	engine.Use(skillRouteContext(false, false))
	group := engine.Group("/api/v1")
	RegisterSkillRoutes(group, h, members, &rbacGuards{cfg: enforcedRBACConfig()})

	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/skills/upload"},
		{http.MethodPut, "/api/v1/skills/id/package"},
		{http.MethodPatch, "/api/v1/skills/id/status"},
		{http.MethodPatch, "/api/v1/skills/status/batch"},
		{http.MethodDelete, "/api/v1/skills/id"},
	}
	for _, item := range cases {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(item.method, item.path, nil))
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403", item.method, item.path, response.Code)
		}
	}
}

func TestSkillWriteRoutesRejectAPIKeysEvenWithFullAccess(t *testing.T) {
	h := handler.NewSkillHandler(service.NewSkillService())
	members := &skillRouteMemberRepository{}
	engine := gin.New()
	engine.Use(skillRouteContext(true, false))
	RegisterSkillRoutes(engine.Group("/api/v1"), h, members, &rbacGuards{cfg: enforcedRBACConfig()})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/skills/id", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("API key write = %d, want 403", response.Code)
	}
}

func TestTenantSkillAuditRouteIsSystemAdminReadOnly(t *testing.T) {
	h := handler.NewSkillHandler(service.NewSkillService())
	for _, item := range []struct {
		admin  bool
		status int
	}{{false, http.StatusForbidden}, {true, http.StatusOK}} {
		engine := gin.New()
		engine.Use(skillRouteContext(false, item.admin))
		RegisterTenantSkillSystemAdminRoutes(engine.Group("/api/v1"), h, &rbacGuards{cfg: enforcedRBACConfig()})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/tenant-skills?tenant_id=7", nil))
		if response.Code != item.status {
			t.Fatalf("admin=%v status=%d want=%d", item.admin, response.Code, item.status)
		}
	}
	engine := gin.New()
	RegisterTenantSkillSystemAdminRoutes(engine.Group("/api/v1"), h, &rbacGuards{cfg: enforcedRBACConfig()})
	if response := httptest.NewRecorder(); func() bool {
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/system/admin/tenant-skills?tenant_id=7", nil))
		return response.Code == http.StatusNotFound
	}() == false {
		t.Fatal("system audit endpoint must be read-only")
	}
}

func skillRouteContext(apiKey, systemAdmin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "viewer")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
		ctx = context.WithValue(ctx, types.SystemAdminContextKey, systemAdmin)
		if apiKey {
			ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{FullAccess: true})
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func enforcedRBACConfig() *config.Config {
	enabled := true
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
}
