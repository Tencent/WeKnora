// Package router registers the HTTP API, including background-work routes.
package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	sessionhandler "github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// registerRoutesForTest builds the smallest router that exercises session
// registration. Tenant is set so a nil config does not log a startup error.
func registerRoutesForTest(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{}}}
	engine := gin.New()
	RegisterSessionRoutes(engine.Group("/api/v1"), &sessionhandler.Handler{}, &handler.MessageSuggestionHandler{}, g)
	return engine
}

// Gin 同一 verb 的通配符名必须一致：GET 树已用 :id，POST 树用 :session_id。
func TestBackgroundWorkRoutesRegisterWithoutWildcardConflict(t *testing.T) {
	var engine *gin.Engine
	require.NotPanics(t, func() {
		engine = registerRoutesForTest(t) // 构建最小 RouterGroup 并调用 RegisterSessionRoutes；
		// 覆盖点是“注册不 panic”。参数解析由 handler 测试负责。
	})
	found := map[string]bool{}
	for _, route := range engine.Routes() {
		found[route.Method+" "+route.Path] = true
	}
	require.True(t, found["GET /api/v1/sessions/:id/background-work"])
	require.True(t, found["GET /api/v1/sessions/:id/background-work/:work_id"])
	require.True(t, found["POST /api/v1/sessions/:session_id/background-work/:work_id/cancel"])
	require.False(t, found["GET /api/v1/sessions/:session_id/background-work"])
}
