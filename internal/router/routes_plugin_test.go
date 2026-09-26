package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
)

func TestPluginInstallationRequiresSystemAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{cfg: &config.Config{}}
	r := gin.New()
	RegisterPluginAdminRoutes(r.Group("/api/v1"), &handler.PluginAdminHandler{}, g)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system/admin/plugins"},
		{http.MethodPost, "/api/v1/system/admin/plugins"},
		{http.MethodPost, "/api/v1/system/admin/plugins/inspect"},
		{http.MethodGet, "/api/v1/system/admin/plugins/acme.kit"},
		{http.MethodGet, "/api/v1/system/admin/plugins/acme.kit/instances"},
		{http.MethodDelete, "/api/v1/system/admin/plugins/acme.kit"},
		{http.MethodPut, "/api/v1/system/admin/plugins/acme.kit/enabled"},
		{http.MethodPut, "/api/v1/system/admin/plugins/acme.kit/active-version"},
		{http.MethodGet, "/api/v1/system/admin/plugins/acme.kit/config"},
		{http.MethodPut, "/api/v1/system/admin/plugins/acme.kit/config"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code)
			if g.apiKeyAuthorizer != nil {
				_, declared := g.apiKeyAuthorizer.Lookup(tc.method, tc.path)
				require.False(t, declared, "API keys must remain default-denied")
			}
		})
	}
}
