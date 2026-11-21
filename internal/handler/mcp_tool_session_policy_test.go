package handler

import (
	"bytes"
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

type httpSessionPolicyService struct {
	interfaces.MCPToolSessionPolicyService
	tenant  uint64
	service string
	tool    string
	rules   []types.MCPToolSessionRule
	calls   int
	err     error
}

func (s *httpSessionPolicyService) SetPolicy(
	_ context.Context, tenant uint64, service, tool string, rules []types.MCPToolSessionRule,
) error {
	s.tenant, s.service, s.tool, s.rules = tenant, service, tool, rules
	s.calls++
	return s.err
}

func TestSetMCPToolSessionPolicyHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		err    error
		status int
		calls  int
	}{
		{"missing", `{}`, nil, 400, 0},
		{"null", `{"allowed_sessions":null}`, nil, 400, 0},
		{"wrong type", `{"allowed_sessions":"all"}`, nil, 400, 0},
		{"empty denies all", `{"allowed_sessions":[]}`, nil, 200, 1},
		{"allow group", `{"allowed_sessions":[
 {"platform":"feishu","chat_type":"group","chat_id":"ops"}]}`, nil, 200, 1},
		{"invalid rule", `{"allowed_sessions":[{}]}`, types.ErrInvalidMCPToolSessionPolicy, 400, 1},
		{"storage failure", `{"allowed_sessions":[]}`, errors.New("private database diagnostics"), 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &httpSessionPolicyService{err: tc.err}
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				c.Set(types.TenantIDContextKey.String(), uint64(7))
			})
			h := (&MCPServiceHandler{}).WithToolSessionPolicies(svc)
			r.PUT("/mcp-services/:id/tool-session-policies/:tool_name", h.SetMCPToolSessionPolicy)
			req := httptest.NewRequest(http.MethodPut,
				"/mcp-services/svc/tool-session-policies/change_ticket", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, tc.calls, svc.calls)
			require.NotContains(t, w.Body.String(), "private database diagnostics")
			if svc.calls > 0 {
				require.Equal(t, uint64(7), svc.tenant)
				require.Equal(t, "svc", svc.service)
				require.Equal(t, "change_ticket", svc.tool)
			}
		})
	}
}
