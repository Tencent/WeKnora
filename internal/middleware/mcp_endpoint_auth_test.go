package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type fakeMCPEndpointService struct {
	interfaces.MCPEndpointService
	ep    *types.MCPEndpoint
	token string
}

func (f *fakeMCPEndpointService) Authenticate(_ context.Context, endpointID, token string) (*types.MCPEndpoint, error) {
	if f.ep == nil || endpointID != f.ep.ID || token != f.token {
		return nil, service.ErrMCPEndpointTokenInvalid
	}
	if !f.ep.Enabled {
		return nil, service.ErrMCPEndpointDisabled
	}
	return f.ep, nil
}

func newMCPAuthTestEngine(svc *fakeMCPEndpointService, capture *map[string]any) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.ContextWithFallback = true
	tenantSvc := &fakeTenantService{tenant: &types.Tenant{ID: 42, Status: "active"}}
	r.POST("/mcp/:endpoint_id", MCPEndpointAuth(svc, tenantSvc), func(c *gin.Context) {
		ctx := c.Request.Context()
		ep, _ := MCPEndpointFromContext(ctx)
		scope, _ := types.TenantAPIKeyScopeFromContext(ctx)
		tenantID, _ := types.TenantIDFromContext(ctx)
		principal, _ := types.PrincipalFromContext(ctx)
		*capture = map[string]any{
			"endpoint":  ep,
			"scope":     scope,
			"tenant":    tenantID,
			"principal": principal,
		}
		c.Status(http.StatusNoContent)
	})
	return r
}

func TestMCPEndpointAuthAttachesScopeAndPrincipal(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 42, Enabled: true,
		KnowledgeBaseIDs: types.StringArray{"kb-9"},
		Tools:            types.StringArray{types.MCPEndpointToolAsk},
	}
	svc := &fakeMCPEndpointService{ep: ep, token: "mcp_secret"}
	var captured map[string]any
	r := newMCPAuthTestEngine(svc, &captured)

	req := httptest.NewRequest(http.MethodPost, "/mcp/ep-1", nil)
	req.Header.Set("Authorization", "Bearer mcp_secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if got, _ := captured["endpoint"].(*types.MCPEndpoint); got == nil || got.ID != "ep-1" {
		t.Fatalf("endpoint missing from context: %v", captured["endpoint"])
	}
	if got, _ := captured["tenant"].(uint64); got != 42 {
		t.Fatalf("tenant = %v", captured["tenant"])
	}
	scope := captured["scope"].(types.TenantAPIKeyScope)
	if scope.FullAccess || !scope.AllowsKnowledgeBase("kb-9") || scope.AllowsKnowledgeBase("kb-1") {
		t.Fatalf("scope not derived from endpoint: %+v", scope)
	}
	if !scope.HasCapability(types.APIKeyCapabilityChat) {
		t.Fatalf("ask tool must grant chat capability: %+v", scope)
	}
	principal := captured["principal"].(types.Principal)
	if principal.Type != types.PrincipalMCPEndpoint || principal.ID != "42:ep-1" {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestMCPEndpointAuthRejectsBadTokens(t *testing.T) {
	ep := &types.MCPEndpoint{ID: "ep-1", TenantID: 42, Enabled: true}
	svc := &fakeMCPEndpointService{ep: ep, token: "mcp_secret"}
	var captured map[string]any
	r := newMCPAuthTestEngine(svc, &captured)

	cases := []struct {
		name   string
		path   string
		header string
		want   int
	}{
		{"missing header", "/mcp/ep-1", "", http.StatusUnauthorized},
		{"wrong scheme", "/mcp/ep-1", "Embed mcp_secret", http.StatusUnauthorized},
		{"wrong token", "/mcp/ep-1", "Bearer mcp_other", http.StatusUnauthorized},
		{"wrong endpoint", "/mcp/ep-2", "Bearer mcp_secret", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, w.Code, tc.want)
		}
	}

	ep.Enabled = false
	req := httptest.NewRequest(http.MethodPost, "/mcp/ep-1", nil)
	req.Header.Set("Authorization", "Bearer mcp_secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("disabled endpoint: status %d, want 403", w.Code)
	}
}
