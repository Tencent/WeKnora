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

type fakeMCPOwnerUserService struct {
	interfaces.UserService
	users map[string]*types.User
}

func (f *fakeMCPOwnerUserService) GetUserByID(_ context.Context, id string) (*types.User, error) {
	return f.users[id], nil
}

func newMCPAuthTestEngine(svc *fakeMCPEndpointService, capture *map[string]any) *gin.Engine {
	return newMCPAuthTestEngineWithOwners(svc, newFakeMemberService(), &fakeMCPOwnerUserService{}, capture)
}

func newMCPAuthTestEngineWithOwners(
	svc *fakeMCPEndpointService,
	members *fakeMemberService,
	users *fakeMCPOwnerUserService,
	capture *map[string]any,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.ContextWithFallback = true
	tenantSvc := &fakeTenantService{tenant: &types.Tenant{ID: 42, Status: "active"}}
	r.POST("/mcp/:endpoint_id", MCPEndpointAuth(svc, tenantSvc, members, users), func(c *gin.Context) {
		ctx := c.Request.Context()
		ep, _ := MCPEndpointFromContext(ctx)
		scope, _ := types.TenantAPIKeyScopeFromContext(ctx)
		tenantID, _ := types.TenantIDFromContext(ctx)
		principal, _ := types.PrincipalFromContext(ctx)
		*capture = map[string]any{
			"endpoint":   ep,
			"scope":      scope,
			"tenant":     tenantID,
			"principal":  principal,
			"authSource": types.PrincipalAuthSourceFromContext(ctx),
			"caller":     types.CallerFromContext(ctx),
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
	if got, _ := captured["authSource"].(string); got != types.AuthSourceMCPEndpoint {
		t.Fatalf("auth source = %q", got)
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

func TestMCPEndpointAuthSpaceEndpointActsAsViewerMachineUser(t *testing.T) {
	ep := &types.MCPEndpoint{ID: "ep-1", TenantID: 42, Enabled: true, Kind: types.MCPEndpointKindSpace}
	var captured map[string]any
	r := newMCPAuthTestEngine(&fakeMCPEndpointService{ep: ep, token: "mcp_secret"}, &captured)

	if code := serveMCPAuth(r, "/mcp/ep-1", "mcp_secret"); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	caller := captured["caller"].(types.Caller)
	if caller.UserID != "mcp-ep-1" || caller.Role != types.TenantRoleViewer {
		t.Fatalf("space endpoint caller = %+v", caller)
	}
}

func TestMCPEndpointAuthPersonalEndpointActsAsOwner(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID: "ep-p", TenantID: 42, Enabled: true,
		Kind: types.MCPEndpointKindPersonal, UserID: "user-1",
	}
	members := newFakeMemberService()
	members.seedActive("user-1", 42, types.TenantRoleContributor)
	users := &fakeMCPOwnerUserService{users: map[string]*types.User{
		"user-1": {ID: "user-1", TenantID: 42, IsActive: true},
	}}
	var captured map[string]any
	r := newMCPAuthTestEngineWithOwners(&fakeMCPEndpointService{ep: ep, token: "mcp_secret"}, members, users, &captured)

	if code := serveMCPAuth(r, "/mcp/ep-p", "mcp_secret"); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	caller := captured["caller"].(types.Caller)
	if caller.UserID != "user-1" || caller.Role != types.TenantRoleContributor || caller.TenantID != 42 {
		t.Fatalf("personal endpoint caller = %+v", caller)
	}
	if scope := captured["scope"].(types.TenantAPIKeyScope); scope.FullAccess {
		t.Fatalf("personal endpoint must keep the endpoint scope: %+v", scope)
	}
}

func TestMCPEndpointAuthPersonalEndpointRejectsFormerMembers(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID: "ep-p", TenantID: 42, Enabled: true,
		Kind: types.MCPEndpointKindPersonal, UserID: "user-1",
	}
	activeUser := map[string]*types.User{"user-1": {ID: "user-1", IsActive: true}}
	cases := []struct {
		name    string
		members func() *fakeMemberService
		users   map[string]*types.User
	}{
		{"no membership", newFakeMemberService, activeUser},
		{"suspended membership", func() *fakeMemberService {
			m := newFakeMemberService()
			m.seedActive("user-1", 42, types.TenantRoleAdmin)
			m.members[memberKey("user-1", 42)].Status = types.TenantMemberStatusSuspended
			return m
		}, activeUser},
		{"inactive user", func() *fakeMemberService {
			m := newFakeMemberService()
			m.seedActive("user-1", 42, types.TenantRoleAdmin)
			return m
		}, map[string]*types.User{"user-1": {ID: "user-1", IsActive: false}}},
	}
	for _, tc := range cases {
		var captured map[string]any
		r := newMCPAuthTestEngineWithOwners(
			&fakeMCPEndpointService{ep: ep, token: "mcp_secret"},
			tc.members(), &fakeMCPOwnerUserService{users: tc.users}, &captured,
		)
		if code := serveMCPAuth(r, "/mcp/ep-p", "mcp_secret"); code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", tc.name, code)
		}
	}
}

func serveMCPAuth(r *gin.Engine, path, token string) int {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}
