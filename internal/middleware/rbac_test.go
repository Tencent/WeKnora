package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// rbacTestHarness builds a tiny gin engine with the RBAC middleware in
// front of a no-op handler. It seeds context just like the real auth
// middleware would, so RequireRole / RequireOwnershipOrRole see the
// expected TenantRole and UserID.
//
// Returning the recorder rather than asserting inline keeps each test
// case focused on the (input -> status) pair it cares about.
func rbacTestHarness(role types.TenantRole, userID string, mw gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Mirror what middleware/auth.go's JWT path sets.
		ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, role)
		ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/protected", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.ServeHTTP(w, req)
	return w
}

func cfgRBAC(enabled bool) *config.Config {
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: enabled}}
}

// ---------- RequireRole ----------

func TestRequireRole_AllowsAtMin(t *testing.T) {
	w := rbacTestHarness(types.TenantRoleAdmin, "u1",
		RequireRole(types.TenantRoleAdmin, cfgRBAC(true)))
	if w.Code != http.StatusOK {
		t.Fatalf("Admin should clear Admin gate, got %d", w.Code)
	}
}

func TestRequireRole_AllowsAboveMin(t *testing.T) {
	w := rbacTestHarness(types.TenantRoleOwner, "u1",
		RequireRole(types.TenantRoleAdmin, cfgRBAC(true)))
	if w.Code != http.StatusOK {
		t.Fatalf("Owner should clear Admin gate, got %d", w.Code)
	}
}

func TestRequireRole_RejectsBelowMin(t *testing.T) {
	w := rbacTestHarness(types.TenantRoleContributor, "u1",
		RequireRole(types.TenantRoleAdmin, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("Contributor must NOT clear Admin gate, got %d", w.Code)
	}
}

func TestRequireRole_FailOpenWhenRBACDisabled(t *testing.T) {
	// EnableRBAC=false: the middleware should log but not block, so the
	// downstream handler still runs. This is the rollout-safety guarantee.
	w := rbacTestHarness(types.TenantRoleViewer, "u1",
		RequireRole(types.TenantRoleOwner, cfgRBAC(false)))
	if w.Code != http.StatusOK {
		t.Fatalf("EnableRBAC=false must let Viewer through Owner gate, got %d", w.Code)
	}
}

func TestRequireRole_NilConfigFailsOpen(t *testing.T) {
	// Defensive: nil config must not panic and must fail open (no enforcement
	// configured = behave like the legacy path).
	w := rbacTestHarness(types.TenantRoleViewer, "u1",
		RequireRole(types.TenantRoleAdmin, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("nil config must fail open, got %d", w.Code)
	}
}

// ---------- RequireOwnershipOrRole ----------

func TestRequireOwnershipOrRole_AdminBypassesLookup(t *testing.T) {
	// Admin / Owner clear the role gate without touching the lookup,
	// so an erroring lookup still passes when the caller has the role.
	called := false
	lookup := func(c *gin.Context) (string, error) {
		called = true
		return "", errors.New("must not be called")
	}
	w := rbacTestHarness(types.TenantRoleAdmin, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusOK {
		t.Fatalf("Admin should pass without lookup, got %d", w.Code)
	}
	if called {
		t.Fatalf("lookup must not run when role already meets min")
	}
}

func TestRequireOwnershipOrRole_CreatorAllowed(t *testing.T) {
	lookup := func(c *gin.Context) (string, error) { return "u1", nil }
	w := rbacTestHarness(types.TenantRoleContributor, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusOK {
		t.Fatalf("creator must clear ownership gate, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_NonCreatorContributorRejected(t *testing.T) {
	// Contributor editing someone else's resource is the exact case the
	// matrix targets: only the original creator OR Admin+ may proceed.
	lookup := func(c *gin.Context) (string, error) { return "someone-else", nil }
	w := rbacTestHarness(types.TenantRoleContributor, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-creator Contributor must hit 403, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_LegacyEmptyCreatorTreatedAsTenantOwned(t *testing.T) {
	// Pre-migration rows (or rows the backfill couldn't resolve) carry
	// creator_id = "". Per the contract those are tenant-owned: only the
	// role check decides.
	lookup := func(c *gin.Context) (string, error) { return "", nil }
	// Contributor on a tenant-owned row -> rejected, only Admin+ can mutate.
	w := rbacTestHarness(types.TenantRoleContributor, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("Contributor on legacy tenant-owned row should hit 403, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_LookupErrorRejects(t *testing.T) {
	// A failing lookup is treated as deny — failing open here would mean
	// any DB hiccup on the creator query becomes a free pass.
	lookup := func(c *gin.Context) (string, error) { return "", errors.New("boom") }
	w := rbacTestHarness(types.TenantRoleContributor, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("lookup error must reject, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_FailOpenWhenRBACDisabled(t *testing.T) {
	// Enforcement off: even a failing lookup + non-creator + low role lets
	// the request through. This preserves today's "anyone in the tenant
	// can edit anything" behaviour while we ship the schema.
	lookup := func(c *gin.Context) (string, error) { return "someone-else", nil }
	w := rbacTestHarness(types.TenantRoleViewer, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(false)))
	if w.Code != http.StatusOK {
		t.Fatalf("EnableRBAC=false must let Viewer non-creator through, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_FailOpenOnLookupErrorWhenRBACDisabled(t *testing.T) {
	// Lookup errors in fail-open mode also let the request through —
	// otherwise turning RBAC off wouldn't actually unblock anything that
	// needs the lookup.
	lookup := func(c *gin.Context) (string, error) { return "", errors.New("boom") }
	w := rbacTestHarness(types.TenantRoleViewer, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(false)))
	if w.Code != http.StatusOK {
		t.Fatalf("EnableRBAC=false + lookup error must fail open, got %d", w.Code)
	}
}
