package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type responseMCPService struct {
	interfaces.MCPServiceService
	metadataErr error
}

func (s *responseMCPService) ListMCPServices(context.Context, uint64) ([]*types.MCPService, error) {
	return []*types.MCPService{{ID: "svc", TenantID: 7, Name: "Service"}}, nil
}

func (s *responseMCPService) ListMCPMetadataSummaries(
	context.Context, uint64, []*types.MCPService,
) (map[string]*types.MCPMetadataSummary, error) {
	return nil, s.metadataErr
}

type responseCategories struct {
	interfaces.ToolboxCategoryService
	err error
}

func (s *responseCategories) ListByResourceIDs(
	context.Context, uint64, string, []string,
) (map[string][]types.ToolboxCategory, error) {
	return map[string][]types.ToolboxCategory{"svc": {{ID: "contracts", Name: "Contracts"}}}, s.err
}

func responseRouter(service *responseMCPService, categories *responseCategories) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	h := &MCPServiceHandler{mcpServiceService: service, categories: categories}
	router.GET("/mcp-services", h.ListMCPServices)
	return router
}

func TestMCPServiceResponseDoesNotSilentlyOmitCategories(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		metadataErr, categoryErr error
		status                   int
	}{
		{name: "complete response", status: http.StatusOK},
		{
			name:        "metadata unavailable still includes tags",
			metadataErr: errors.New("metadata unavailable"), status: http.StatusOK,
		},
		{name: "tags unavailable", categoryErr: errors.New("tags unavailable"), status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := responseRouter(
				&responseMCPService{metadataErr: tc.metadataErr}, &responseCategories{err: tc.categoryErr},
			)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mcp-services", nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusOK {
				require.Contains(t, w.Body.String(), `"categories":[{"id":"contracts"`)
			} else {
				require.NotContains(t, w.Body.String(), `"success":true`)
			}
		})
	}
}
