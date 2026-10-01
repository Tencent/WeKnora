package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failedMCPTestService struct {
	interfaces.MCPServiceService
	tenant uint64
	id     string
}

func (s *failedMCPTestService) TestMCPService(
	_ context.Context, tenant uint64, id string,
) (*types.MCPTestResult, error) {
	s.tenant, s.id = tenant, id
	return &types.MCPTestResult{Success: false, Message: "Tool discovery failed: directory unavailable"}, nil
}

func TestMCPServiceTestReturnsFailedDiscovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &failedMCPTestService{}
	handler := &MCPServiceHandler{mcpServiceService: svc}
	router := gin.New()
	router.POST("/mcp-services/:id/test", func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		handler.TestMCPService(c)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp-services/probe/test", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, uint64(7), svc.tenant)
	require.Equal(t, "probe", svc.id)
	var body struct {
		Success bool                `json:"success"`
		Data    types.MCPTestResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success, "the request successfully returned a test result")
	require.False(t, body.Data.Success, "tool discovery failed")
	require.Equal(t, "Tool discovery failed: directory unavailable", body.Data.Message)
	require.Empty(t, body.Data.Tools)
}
