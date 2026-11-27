package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeSourceRoutesRequireIngest(t *testing.T) {
	g := &rbacGuards{}
	RegisterKnowledgeRoutes(gin.New().Group("/api/v1"), &handler.KnowledgeHandler{}, g)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		policy := mustLookupAPIKeyPolicy(t, g, method, "/api/v1/knowledge/:id/content")
		require.True(t, policyHasCapability(policy, types.APIKeyCapabilityIngest))
		require.False(t, policyHasCapability(policy, types.APIKeyCapabilityRetrieve))
	}
}

func TestKnowledgeSourceRoutesRejectViewer(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		enabled := true
		guards := &rbacGuards{
			cfg:                &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}},
			knowledgeKBCreator: func(*gin.Context) (string, error) { return "owner", nil },
		}
		engine := gin.New()
		engine.Use(func(c *gin.Context) {
			ctx := types.WithCaller(context.Background(), types.Caller{
				TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer,
			})
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
		engine.Use(middleware.ErrorHandler())
		RegisterKnowledgeRoutes(engine.Group("/api/v1"), &handler.KnowledgeHandler{}, guards)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/knowledge/doc/content", nil))
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	}
}
