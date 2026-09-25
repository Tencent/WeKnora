package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubMCPToolApprovalService struct {
	interfaces.MCPToolApprovalService
	requireApproval *bool
	enabled         *bool
	err             error
}

func (s *stubMCPToolApprovalService) SetPolicy(
	_ context.Context, _ uint64, _, _ string, requireApproval, enabled *bool,
) error {
	s.requireApproval = requireApproval
	s.enabled = enabled
	return s.err
}

func newMCPToolApprovalRouter(svc interfaces.MCPToolApprovalService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	h := &MCPServiceHandler{mcpToolApprovalService: svc}
	r.PUT("/mcp-services/:id/tool-approvals/:tool_name", h.SetMCPToolApproval)
	return r
}

func TestSetMCPToolApprovalRejectsEmptyBody(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/mcp-services/svc-1/tool-approvals/get_forecast", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	newMCPToolApprovalRouter(&stubMCPToolApprovalService{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetMCPToolApprovalPartialEnabled(t *testing.T) {
	stub := &stubMCPToolApprovalService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/mcp-services/svc-1/tool-approvals/get_forecast",
		bytes.NewBufferString(`{"enabled":false}`),
	)
	req.Header.Set("Content-Type", "application/json")
	newMCPToolApprovalRouter(stub).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Nil(t, stub.requireApproval)
	require.NotNil(t, stub.enabled)
	require.False(t, *stub.enabled)
}

func TestSetMCPToolApprovalPartialRequireApproval(t *testing.T) {
	stub := &stubMCPToolApprovalService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/mcp-services/svc-1/tool-approvals/get_forecast",
		bytes.NewBufferString(`{"require_approval":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	newMCPToolApprovalRouter(stub).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, stub.requireApproval)
	require.True(t, *stub.requireApproval)
	require.Nil(t, stub.enabled)
}

func TestSetMCPToolApprovalCombinedFields(t *testing.T) {
	stub := &stubMCPToolApprovalService{}
	body, err := json.Marshal(map[string]bool{"require_approval": true, "enabled": false})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/mcp-services/svc-1/tool-approvals/get_forecast", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	newMCPToolApprovalRouter(stub).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, stub.requireApproval)
	require.True(t, *stub.requireApproval)
	require.NotNil(t, stub.enabled)
	require.False(t, *stub.enabled)
}
