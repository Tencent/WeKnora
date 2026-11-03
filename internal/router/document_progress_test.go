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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDocumentProgressRequiresSystemAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, admin := range []bool{false, true} {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), types.SystemAdminContextKey, admin)
			c.Request = c.Request.WithContext(ctx)
		})
		RegisterSystemAdminRoutes(r.Group("/api/v1"), &handler.SystemHandler{}, nil,
			&rbacGuards{cfg: &config.Config{}})
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/system/admin/runtime/documents"},
			{http.MethodGet, "/api/v1/system/admin/runtime/documents/doc"},
			{http.MethodPost, "/api/v1/system/admin/runtime/documents/wiki-start"},
		} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"count":1}`)))
			want := http.StatusForbidden
			if admin {
				want = http.StatusServiceUnavailable
			}
			require.Equal(t, want, w.Code, tc.method+tc.path)
		}
	}
}
