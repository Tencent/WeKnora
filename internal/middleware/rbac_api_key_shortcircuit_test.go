package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// apiKeyRBACHarness seeds an API-key scope (as attachAPIKeyAuthContext would)
// plus a deliberately-insufficient TenantRole, then runs the guard. It asserts
// the JWT guard short-circuits for API-key principals so that per-route
// API-key authorization is left entirely to the APIKeyGate.
func apiKeyRBACHarness(scope types.TenantAPIKeyScope, role types.TenantRole, mw gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, role)
		ctx = types.WithTenantAPIKeyScope(ctx, scope)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/protected", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/protected", nil))
	return w
}

func TestRequireRole_ShortCircuitsAPIKey(t *testing.T) {
	// Scoped API key against an Admin gate would 403 under the JWT
	// ladder; the short-circuit lets it through (the gate handles it).
	w := apiKeyRBACHarness(
		types.TenantAPIKeyScope{},
		types.TenantRoleViewer,
		RequireRole(types.TenantRoleAdmin, cfgRBAC(true)),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("API-key principal should short-circuit RequireRole, got %d", w.Code)
	}
}

func TestRequireSystemAdmin_RejectsAPIKey(t *testing.T) {
	w := apiKeyRBACHarness(
		types.TenantAPIKeyScope{FullAccess: true},
		types.TenantRoleOwner,
		RequireSystemAdmin(cfgRBAC(true)),
	)
	if w.Code != http.StatusForbidden {
		t.Fatalf("API-key principal must be rejected by RequireSystemAdmin, got %d", w.Code)
	}
}

func TestRequireSystemAdmin_AllowsPlatformAPIKeyAfterRouteGate(t *testing.T) {
	w := apiKeyRBACHarness(
		types.TenantAPIKeyScope{ScopeType: types.APIKeyScopePlatform},
		types.TenantRoleViewer,
		RequireSystemAdmin(cfgRBAC(true)),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("platform API key should pass the system-admin role guard after route authorization, got %d", w.Code)
	}
}

// TestRequireOwnershipOrRole_ShortCircuitsAPIKey is the core regression for
// review #1: an API key writing to a KB it does not
// "own" (synthetic system user never matches creator_id) must not be 403'd by
// the OwnedKBOrAdmin guard when EnableRBAC=true. The lookup deliberately
// returns a foreign creator to prove the guard never runs it.
func TestRequireOwnershipOrRole_ShortCircuitsAPIKey(t *testing.T) {
	lookupCalled := false
	lookup := func(_ *gin.Context) (string, error) {
		lookupCalled = true
		return "some-other-human-user", nil
	}
	w := apiKeyRBACHarness(
		types.TenantAPIKeyScope{
			KnowledgeBaseIDs: types.StringArray{"kb-1"},
			Capabilities:     types.StringArray{string(types.APIKeyCapabilityIngest)},
		},
		types.TenantRoleContributor,
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("API-key write should short-circuit OwnedKBOrAdmin, got %d", w.Code)
	}
	if lookupCalled {
		t.Fatal("ownership lookup must not run for API-key principals")
	}
}

// TestEvaluateOwnershipOrRole_ShortCircuitsAPIKey mirrors
// TestRequireOwnershipOrRole_ShortCircuitsAPIKey for the handler-side twin used
// when the KB id is carried in the request body (MoveKnowledge /
// BatchDeleteKnowledge via requireKBOwnershipOrAdmin). A scoped ingest key that
// the APIKeyGate + KB allow-list already admitted must not be re-rejected here
// just because it is synthesized as a Viewer for legacy-guard compatibility.
func TestEvaluateOwnershipOrRole_ShortCircuitsAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityIngest)},
	})
	// Synthesized Viewer role + a foreign creator would 403 a human caller.
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)

	if err := EvaluateOwnershipOrRole(ctx, cfgRBAC(true),
		types.TenantRoleAdmin, "some-other-human-user", nil); err != nil {
		t.Fatalf("API-key principal should short-circuit EvaluateOwnershipOrRole, got %v", err)
	}
}

// Sanity: a JWT Viewer with a foreign creator is still forbidden — the
// short-circuit must be scoped to API-key principals only.
func TestEvaluateOwnershipOrRole_JWTViewerStillDenied(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleViewer)
	err := EvaluateOwnershipOrRole(ctx, cfgRBAC(true),
		types.TenantRoleAdmin, "some-other-human-user", nil)
	if err == nil {
		t.Fatal("JWT Viewer with foreign creator must be denied by EvaluateOwnershipOrRole")
	}
}

// Sanity: a real JWT Viewer is still rejected by the Admin gate — the
// short-circuit must be scoped to API-key principals only.
func TestRequireRole_JWTViewerStillDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, types.TenantRoleViewer)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/p", RequireRole(types.TenantRoleAdmin, &config.Config{Tenant: &config.TenantConfig{EnableRBAC: boolPtr(true)}}),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("JWT Viewer must still be denied by Admin gate, got %d", w.Code)
	}
}

func boolPtr(b bool) *bool { return &b }
