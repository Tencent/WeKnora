package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type createMCPServiceStub struct {
	interfaces.MCPServiceService
	created *types.MCPService
}

func (s *createMCPServiceStub) CreateMCPService(_ context.Context, service *types.MCPService) error {
	s.created = service
	return nil
}

func TestMCPServiceCreateEnabledDefaultAndExplicitValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		field   string
		enabled bool
	}{
		{name: "omitted", enabled: true},
		{name: "disabled", field: `,"enabled":false`, enabled: false},
		{name: "enabled", field: `,"enabled":true`, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &createMCPServiceStub{}
			router := gin.New()
			router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				c.Set(types.TenantIDContextKey.String(), uint64(7))
				c.Next()
			})
			h := &MCPServiceHandler{mcpServiceService: service}
			router.POST("/mcp-services", h.CreateMCPService)
			body := `{"name":"Service","transport_type":"sse"` + tc.field + `}`
			request := httptest.NewRequest(http.MethodPost, "/mcp-services", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotNil(t, service.created)
			require.Equal(t, tc.enabled, service.created.Enabled)
		})
	}
}
