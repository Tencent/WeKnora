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
	// Data-plane routes (Viewer read, Contributor write, Admin destructive)
	// plus one tenant-level Owner route.
	a.Register(http.MethodGet, "/api/v1/knowledge-bases/:id", policyMinRole(types.TenantRoleViewer))
	a.Register(http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file", policyMinRole(types.TenantRoleContributor))
	a.Register(http.MethodDelete, "/api/v1/knowledge-bases/:id/knowledge", policyMinRole(types.TenantRoleAdmin))
	a.Register(http.MethodGet, "/api/v1/models", policyMinRole(types.TenantRoleOwner))
	a.Register(http.MethodPut, "/api/v1/tenants/kv/:key", policyMinRole(types.TenantRoleOwner))
	return a
}

func policyMinRole(r types.TenantRole) APIKeyRoutePolicy {
	return APIKeyRoutePolicy{MinRole: r}
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
	case "/api/v1/knowledge-bases/:id/knowledge":
		return "/api/v1/knowledge-bases/kb-1/knowledge"
	case "/api/v1/tenants/kv/:key":
		return "/api/v1/tenants/kv/some-key"
	default:
		return tmpl
	}
}

func TestGateJWTPassesThrough(t *testing.T) {
	a := newTestAuthorizer()
	// No scope => JWT principal => always allowed, even on an Owner route.
	if !runGate(t, a, nil, http.MethodGet, "/api/v1/models") {
		t.Fatal("JWT principal must pass the gate")
	}
}

func TestGateDefaultDeny(t *testing.T) {
	a := newTestAuthorizer()
	owner := &types.TenantAPIKeyScope{Role: types.TenantRoleOwner}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), *owner))
		c.Next()
	})
	engine.Use(a.Middleware())
	engine.POST("/api/v1/agents", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("undeclared route should default-deny even an Owner key: status=%d", w.Code)
	}
}

func TestGateDataPlaneRoleLadder(t *testing.T) {
	a := newTestAuthorizer()
	viewer := &types.TenantAPIKeyScope{Role: types.TenantRoleViewer}
	contributor := &types.TenantAPIKeyScope{Role: types.TenantRoleContributor}
	admin := &types.TenantAPIKeyScope{Role: types.TenantRoleAdmin}

	// Safe read: any data-plane key (Viewer) passes.
	if !runGate(t, a, viewer, http.MethodGet, "/api/v1/knowledge-bases/:id") {
		t.Fatal("viewer key should read a declared KB route")
	}
	// Contributor write.
	if runGate(t, a, viewer, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("viewer key must not perform a contributor write")
	}
	if !runGate(t, a, contributor, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("contributor key should perform a contributor write")
	}
	// Admin destructive.
	if runGate(t, a, contributor, http.MethodDelete, "/api/v1/knowledge-bases/:id/knowledge") {
		t.Fatal("contributor key must not perform an admin destructive op")
	}
	if !runGate(t, a, admin, http.MethodDelete, "/api/v1/knowledge-bases/:id/knowledge") {
		t.Fatal("admin key should perform an admin destructive op")
	}
}

func TestGateTenantRoutesRequireOwner(t *testing.T) {
	a := newTestAuthorizer()
	admin := &types.TenantAPIKeyScope{Role: types.TenantRoleAdmin}
	owner := &types.TenantAPIKeyScope{Role: types.TenantRoleOwner}

	// Even a safe GET on a tenant-level route requires Owner (no Viewer floor).
	if runGate(t, a, admin, http.MethodGet, "/api/v1/models") {
		t.Fatal("admin key must NOT read a tenant-level route")
	}
	if !runGate(t, a, owner, http.MethodGet, "/api/v1/models") {
		t.Fatal("owner key should read a tenant-level route")
	}
	// Unsafe tenant write also requires Owner.
	if runGate(t, a, admin, http.MethodPut, "/api/v1/tenants/kv/:key") {
		t.Fatal("admin key must NOT write a tenant-level route")
	}
	if !runGate(t, a, owner, http.MethodPut, "/api/v1/tenants/kv/:key") {
		t.Fatal("owner key should write a tenant-level route")
	}
}

func TestGateKBScopeDoesNotBlockDataPlane(t *testing.T) {
	a := newTestAuthorizer()
	// A KB-restricted key is NOT blocked by the gate on data-plane routes;
	// its KB allow-list is enforced downstream by KBAccess/handler checks.
	restricted := &types.TenantAPIKeyScope{
		Role:             types.TenantRoleContributor,
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	}
	if !runGate(t, a, restricted, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file") {
		t.Fatal("KB-restricted contributor key should pass the gate on a data-plane write")
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
	// Even an Owner-role API key must be rejected outright.
	reached, status := runDenyAPIKey(t, &types.TenantAPIKeyScope{Role: types.TenantRoleOwner})
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
