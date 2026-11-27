package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type routeSessionPolicies struct {
	interfaces.MCPToolSessionPolicyService
	calls int
}

func (s *routeSessionPolicies) SetPolicy(
	context.Context, uint64, string, string, []types.MCPToolSessionRule,
) error {
	s.calls++
	return nil
}

func (s *routeSessionPolicies) DeletePolicy(context.Context, uint64, string, string) error {
	s.calls++
	return nil
}

func TestMCPToolSessionPolicyWritesRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enforce := true
	g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}}
	for _, role := range []types.TenantRole{
		types.TenantRoleViewer, types.TenantRoleContributor, types.TenantRoleAdmin, types.TenantRoleOwner,
	} {
		t.Run(string(role), func(t *testing.T) {
			svc := &routeSessionPolicies{}
			h := (&handler.MCPServiceHandler{}).WithToolSessionPolicies(svc)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(types.TenantIDContextKey.String(), uint64(7))
				c.Request = c.Request.WithContext(types.WithCaller(c.Request.Context(), types.Caller{
					TenantID: 7, UserID: "admin-test", Role: role,
				}))
			})
			RegisterMCPServiceRoutes(r.Group("/api/v1"), h,
				&handler.MCPCredentialsHandler{}, &handler.MCPOAuthHandler{}, g)
			for _, method := range []string{http.MethodPut, http.MethodDelete} {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(method, "/api/v1/mcp-services/svc/tool-session-policies/tool",
					strings.NewReader(`{"allowed_sessions":[]}`))
				req.Header.Set("Content-Type", "application/json")
				r.ServeHTTP(w, req)
				if role == types.TenantRoleAdmin || role == types.TenantRoleOwner {
					require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				} else {
					require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
					require.Zero(t, svc.calls)
				}
			}
		})
	}
}
