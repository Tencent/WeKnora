package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelCatalogRequiresSystemAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{cfg: &config.Config{}}
	r := gin.New()
	RegisterSystemAdminRoutes(r.Group("/api/v1"), &handler.SystemHandler{}, nil, g)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system/admin/model-catalog"},
		{http.MethodPost, "/api/v1/system/admin/model-catalog/preview"},
		{http.MethodPut, "/api/v1/system/admin/model-catalog"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, "gate must hold even when workspace RBAC is disabled")
			_, declared := g.apiKeyAuthorizer.Lookup(tc.method, tc.path)
			require.False(t, declared, "API keys must remain default-denied")
		})
	}
}
