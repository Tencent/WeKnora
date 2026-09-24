package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// stubIntentPolicyRepo 是 IntentPolicyRepository 的内存桩：路由层测试只
// 关心鉴权与路由可达性，不验证持久化语义（那在 handler 测试里用 sqlite 验）。
type stubIntentPolicyRepo struct{}

func (stubIntentPolicyRepo) Create(_ context.Context, _ *types.IntentPolicy) error { return nil }

func (stubIntentPolicyRepo) GetByID(_ context.Context, _ uint64, id string) (*types.IntentPolicy, error) {
	return &types.IntentPolicy{ID: id, Version: 1}, nil
}

func (stubIntentPolicyRepo) ListByTenant(_ context.Context, _ uint64) ([]*types.IntentPolicy, error) {
	return nil, nil
}

func (stubIntentPolicyRepo) SetEnabled(_ context.Context, _ uint64, _ string, _ bool) error {
	return nil
}

func (stubIntentPolicyRepo) Delete(_ context.Context, _ uint64, _ string) error {
	return apprepo.ErrIntentPolicyNotFound
}

// newIntentPolicyRouteTestEngine 构造指定角色的路由测试引擎（RBAC 开启）。
func newIntentPolicyRouteTestEngine(t *testing.T, role types.TenantRole) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	enabled := true
	cfg := &config.Config{
		Tenant: &config.TenantConfig{EnableRBAC: &enabled},
	}
	guards := &rbacGuards{cfg: cfg}
	guards.ensureAPIKeyAuthorizer()

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})

	h := handler.NewIntentPolicyHandler(stubIntentPolicyRepo{}, nil)
	RegisterIntentPolicyRoutes(r.Group("/api/v1"), h, guards)
	return r
}

func intentPolicyRoutePaths() []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodPost, "/api/v1/intent-policies"},
		{http.MethodGet, "/api/v1/intent-policies"},
		{http.MethodGet, "/api/v1/intent-policies/p1"},
		{http.MethodPut, "/api/v1/intent-policies/p1"},
		{http.MethodPost, "/api/v1/intent-policies/p1/disable"},
		{http.MethodPost, "/api/v1/intent-policies/p1/enable"},
	}
}

func TestIntentPolicyRoutesRequireAdmin(t *testing.T) {
	engine := newIntentPolicyRouteTestEngine(t, types.TenantRoleViewer)
	for _, tc := range intentPolicyRoutePaths() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code,
				"viewer 不得访问策略 CRUD, body=%s", rec.Body.String())
		})
	}
}

func TestIntentPolicyRoutesReachableByAdmin(t *testing.T) {
	engine := newIntentPolicyRouteTestEngine(t, types.TenantRoleAdmin)
	for _, tc := range intentPolicyRoutePaths() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)
			// Admin 必须能穿过 RBAC 与 API-key gate 到达 handler；
			// 业务结果（400/200）由 handler 测试覆盖，这里只断言不是 403/404/405。
			require.NotContains(t,
				[]int{http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed},
				rec.Code, "body=%s", rec.Body.String())
		})
	}
}
