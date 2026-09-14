package router

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type learningRouteService struct {
	interfaces.LearningService
	calls int
}

func (s *learningRouteService) GetSettings(context.Context) (*types.LearningSettings, error) {
	s.calls++
	return &types.LearningSettings{Enabled: false, AlgorithmVersion: "bkt-v1"}, nil
}

func TestLearningRoutesRejectAllNonWebPrincipalsAndFullAccessKeys(t *testing.T) {
	for _, test := range []struct {
		name, kind        string
		machine, borrowed bool
		status            int
	}{
		{"web", types.PrincipalWebUser, false, false, 200},
		{"shared execution", types.PrincipalWebUser, false, true, 403},
		{"full access", types.PrincipalWebUser, true, false, 403},
		{"platform", types.PrincipalAPIPlatform, true, false, 403},
		{"external user", types.PrincipalAPIExternalUser, true, false, 403},
		{"im", types.PrincipalIMUser, false, false, 403},
		{"embed", types.PrincipalEmbedVisitor, false, false, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := &learningRouteService{}
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			g := &rbacGuards{}
			r.Use(func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
				ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "alice", Role: types.TenantRoleViewer})
				ctx = types.WithPrincipal(ctx, types.Principal{Type: test.kind, ID: "alice"})
				if test.machine {
					ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{FullAccess: true})
				}
				if test.borrowed {
					ctx = types.WithExecutionTenant(ctx, 8)
				}
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			r.Use(g.ensureAPIKeyAuthorizer().Middleware())
			RegisterLearningRoutes(r.Group("/api/v1"), handler.NewLearningHandler(svc), g)
			require.Len(t, r.Routes(), 12)
			for _, route := range r.Routes() {
				_, exists := g.apiKeyAuthorizer.Lookup(route.Method, route.Path)
				require.False(t, exists, "learning must not declare API-key access")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/learning/settings", nil))
			require.Equal(t, test.status, w.Code, w.Body.String())
			if test.status == 403 {
				require.Zero(t, svc.calls)
			} else {
				require.Equal(t, 1, svc.calls)
			}
		})
	}
}
