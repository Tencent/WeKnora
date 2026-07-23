package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func newTestAuthorizer() *APIKeyRouteAuthorizer {
	a := NewAPIKeyRouteAuthorizer()
	a.Register(http.MethodGet, "/api/v1/auth/me", APIKeyRoutePolicy{})
	a.Register(http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file",
		APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(types.APIKeyCapabilityIngest))
	a.Register(http.MethodGet, "/api/v1/models",
		APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(types.APIKeyCapabilityManageModels))
	a.Register(http.MethodPut, "/api/v1/tenants/kv/:key",
		APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(types.APIKeyCapabilityManageTenantSettings))
	return a
}

// runGate exercises the gate middleware with a given scope, method and route
// full-path, returning whether the request was allowed to proceed.
func runGate(t *testing.T, a *APIKeyRouteAuthorizer, scope *types.TenantAPIKeyScope, method, fullPath string) bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if scope != nil {
			c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), *scope))
		}
		c.Next()
	})
	engine.Use(a.Middleware())
	allowed := false
	engine.Handle(method, fullPath, func(c *gin.Context) {
		allowed = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(method, concretePath(fullPath), nil))
	return allowed && w.Code == http.StatusOK
}

// concretePath substitutes gin :params with literals so httptest can hit it.
func concretePath(tmpl string) string {
	switch tmpl {
	case "/api/v1/knowledge-bases/:id":
		return "/api/v1/knowledge-bases/kb-1"
	case "/api/v1/knowledge-bases/:id/knowledge/file":
		return "/api/v1/knowledge-bases/kb-1/knowledge/file"
	case "/api/v1/tenants/kv/:key":
		return "/api/v1/tenants/kv/some-key"
	default:
		return tmpl
	}
}

func TestGateJWTPassesThrough(t *testing.T) {
	a := newTestAuthorizer()
	// No scope => JWT principal => always allowed, even on a full-access route.
	if !runGate(t, a, nil, http.MethodGet, "/api/v1/models") {
		t.Fatal("JWT principal must pass the gate")
	}
}

func TestGateDefaultDeny(t *testing.T) {
	a := newTestAuthorizer()
	full := &types.TenantAPIKeyScope{FullAccess: true}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), *full))
		c.Next()
	})
	engine.Use(a.Middleware())
	engine.POST("/api/v1/agents", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("undeclared route should default-deny even a full-access key: status=%d", w.Code)
	}
}

func TestGateAnyPolicyAllowsScopedKey(t *testing.T) {
	a := newTestAuthorizer()
	scoped := &types.TenantAPIKeyScope{}

	if !runGate(t, a, scoped, http.MethodGet, "/api/v1/auth/me") {
		t.Fatal("scoped key should call a declared route with an empty policy")
	}
}

func TestGateFullAccessAndCapabilityPolicies(t *testing.T) {
	a := newTestAuthorizer()
	scoped := &types.TenantAPIKeyScope{}
	full := &types.TenantAPIKeyScope{FullAccess: true}
	ingest := &types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}}
	models := &types.TenantAPIKeyScope{Capabilities: types.StringArray{"manage_models"}}

	if runGate(t, a, scoped, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("plain scoped key must not write content")
	}
	if !runGate(t, a, full, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("full-access key should write content")
	}
	if !runGate(t, a, ingest, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("ingest capability should write content")
	}
	if runGate(t, a, ingest, http.MethodGet, "/api/v1/models") {
		t.Fatal("ingest capability must not read model management routes")
	}
	if !runGate(t, a, models, http.MethodGet, "/api/v1/models") {
		t.Fatal("manage_models capability should read model management routes")
	}
}

func TestGateAnyOfCapabilities(t *testing.T) {
	a := NewAPIKeyRouteAuthorizer()
	a.Register(http.MethodPost, "/api/v1/sessions",
		APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(types.APIKeyCapabilityChat))
	a.Register(http.MethodGet, "/api/v1/agents",
		APIKeyRoutePolicy{RequireFullAccess: true}.
			WithCapability(types.APIKeyCapabilityChat).
			WithCapability(types.APIKeyCapabilityManageAgents))
	a.Register(http.MethodPost, "/api/v1/agents",
		APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(types.APIKeyCapabilityManageAgents))
	a.Register(http.MethodPut, "/api/v1/knowledge-bases/:id",
		APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(types.APIKeyCapabilityManageKnowledgeBases))

	chat := &types.TenantAPIKeyScope{Capabilities: types.StringArray{"chat"}}
	manage := &types.TenantAPIKeyScope{Capabilities: types.StringArray{"manage_agents"}}
	manageKBs := &types.TenantAPIKeyScope{Capabilities: types.StringArray{"manage_kbs"}}

	// Either capability satisfies the any-of read route.
	if !runGate(t, a, chat, http.MethodGet, "/api/v1/agents") {
		t.Fatal("chat should read agents (any-of)")
	}
	if !runGate(t, a, manage, http.MethodGet, "/api/v1/agents") {
		t.Fatal("manage_agents should read agents (any-of)")
	}
	// Only manage_agents may author agents.
	if runGate(t, a, chat, http.MethodPost, "/api/v1/agents") {
		t.Fatal("chat must not author agents")
	}
	if !runGate(t, a, manage, http.MethodPost, "/api/v1/agents") {
		t.Fatal("manage_agents should author agents")
	}
	// Only manage_kbs may manage KB metadata/config.
	if runGate(t, a, manage, http.MethodPut, "/api/v1/knowledge-bases/:id") {
		t.Fatal("manage_agents must not manage knowledge bases")
	}
	if !runGate(t, a, manageKBs, http.MethodPut, "/api/v1/knowledge-bases/:id") {
		t.Fatal("manage_kbs should manage knowledge bases")
	}
}

func TestGateKBScopeDoesNotBlockDataPlane(t *testing.T) {
	a := newTestAuthorizer()
	// A KB-restricted key is NOT blocked by the gate on data-plane routes;
	// its KB allow-list is enforced downstream by KBAccess/handler checks.
	restricted := &types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
		Capabilities:     types.StringArray{"ingest"},
	}
	if !runGate(t, a, restricted, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("KB-restricted ingest key should pass the gate on a data-plane write")
	}
}

func TestGatePlatformOnlyPolicyRejectsTenantKeyBeforeFullAccess(t *testing.T) {
	a := NewAPIKeyRouteAuthorizer()
	a.Register(http.MethodGet, "/api/v1/system/admin/settings",
		APIKeyRoutePolicy{PlatformOnly: true}.
			WithCapability(types.APIKeyCapabilitySystemSettingsRead))
	tenantFull := &types.TenantAPIKeyScope{FullAccess: true}
	platform := &types.TenantAPIKeyScope{
		ScopeType:    types.APIKeyScopePlatform,
		Capabilities: types.StringArray{string(types.APIKeyCapabilitySystemSettingsRead)},
	}
	if runGate(t, a, tenantFull, http.MethodGet, "/api/v1/system/admin/settings") {
		t.Fatal("tenant full-access key must not enter a platform-only route")
	}
	if !runGate(t, a, platform, http.MethodGet, "/api/v1/system/admin/settings") {
		t.Fatal("platform key with the required capability should pass")
	}

	withoutCapability := NewAPIKeyRouteAuthorizer()
	withoutCapability.Register(http.MethodGet, "/api/v1/system/admin/unsafe",
		APIKeyRoutePolicy{PlatformOnly: true})
	corruptPlatformFull := &types.TenantAPIKeyScope{
		ScopeType:  types.APIKeyScopePlatform,
		FullAccess: true,
	}
	if runGate(t, withoutCapability, corruptPlatformFull, http.MethodGet, "/api/v1/system/admin/unsafe") {
		t.Fatal("platform-only policy without an explicit capability must fail closed")
	}
}

// runDenyAPIKey mounts DenyAPIKeyPrincipal ahead of a handler and reports
// whether the request reached the handler.
func runDenyAPIKey(t *testing.T, scope *types.TenantAPIKeyScope) (reached bool, status int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if scope != nil {
			c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), *scope))
		}
		c.Next()
	})
	engine.GET("/api/v1/files/presigned-preview", DenyAPIKeyPrincipal(), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/files/presigned-preview", nil))
	return reached, w.Code
}

// TestDenyAPIKeyPrincipalBlocksAPIKeys guards the gate-bypass class of bug:
// engine-root routes (outside /api/v1) that rely on RequireRole must not be
// reachable by API-key principals, since RequireRole short-circuits them.
func TestDenyAPIKeyPrincipalBlocksAPIKeys(t *testing.T) {
	// Even a full-access API key must be rejected outright.
	reached, status := runDenyAPIKey(t, &types.TenantAPIKeyScope{FullAccess: true})
	if reached {
		t.Fatal("API-key principal must not reach a DenyAPIKeyPrincipal-guarded handler")
	}
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for API-key principal, got %d", status)
	}
}

// TestDenyAPIKeyPrincipalAllowsJWT confirms JWT sessions (no API-key scope)
// pass straight through.
func TestDenyAPIKeyPrincipalAllowsJWT(t *testing.T) {
	reached, status := runDenyAPIKey(t, nil)
	if !reached || status != http.StatusOK {
		t.Fatalf("JWT session should pass DenyAPIKeyPrincipal: reached=%v status=%d", reached, status)
	}
}

// runAllowFileServe mounts AllowFileServeAPIKey ahead of a handler and reports
// whether the request reached the handler.
func runAllowFileServe(t *testing.T, scope *types.TenantAPIKeyScope) (reached bool, status int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if scope != nil {
			c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), *scope))
		}
		c.Next()
	})
	engine.GET("/files", AllowFileServeAPIKey(), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/files", nil))
	return reached, w.Code
}

func TestAllowFileServeAPIKey(t *testing.T) {
	cases := []struct {
		name        string
		scope       *types.TenantAPIKeyScope
		wantReached bool
	}{
		{name: "jwt passes", scope: nil, wantReached: true},
		{name: "full access passes", scope: &types.TenantAPIKeyScope{FullAccess: true}, wantReached: true},
		{
			name: "tenant-wide retrieve passes",
			scope: &types.TenantAPIKeyScope{
				Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)},
			},
			wantReached: true,
		},
		{
			name: "kb-restricted retrieve denied",
			scope: &types.TenantAPIKeyScope{
				KnowledgeBaseIDs: types.StringArray{"kb-1"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
			},
			wantReached: false,
		},
		{
			name: "non-retrieve capability denied",
			scope: &types.TenantAPIKeyScope{
				Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
			},
			wantReached: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached, status := runAllowFileServe(t, tc.scope)
			if reached != tc.wantReached {
				t.Fatalf("reached=%v want %v (status=%d)", reached, tc.wantReached, status)
			}
			if tc.wantReached && status != http.StatusOK {
				t.Fatalf("expected 200, got %d", status)
			}
			if !tc.wantReached && status != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", status)
			}
		})
	}
}

func TestNormalizeRoutePath(t *testing.T) {
	cases := map[string]string{
		"/api/v1//models": "/api/v1/models",
		"/api/v1/models/": "/api/v1/models",
		"/":               "/",
		"/api/v1/agents":  "/api/v1/agents",
	}
	for in, want := range cases {
		if got := normalizeRoutePath(in); got != want {
			t.Fatalf("normalizeRoutePath(%q)=%q want %q", in, got, want)
		}
	}
}
