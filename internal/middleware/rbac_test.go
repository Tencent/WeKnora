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
	"github.com/stretchr/testify/assert"
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
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
}

// cfgRBACWithCrossTenant returns a config with both per-tenant RBAC
// enforcement AND the cluster-wide cross-tenant access flag enabled.
// IsCrossTenantSuperuser requires BOTH to honour the User attribute,
// so cross-tenant superuser tests need this rather than plain cfgRBAC.
func cfgRBACWithCrossTenant(enabled bool) *config.Config {
	return &config.Config{Tenant: &config.TenantConfig{
		EnableRBAC:              &enabled,
		EnableCrossTenantAccess: true,
	}}
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

func TestRequireRoleOrSystemAdmin_AllowsSystemAdminBelowTenantRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantRoleContextKey, types.TenantRoleViewer)
		ctx = context.WithValue(ctx, types.SystemAdminContextKey, true)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET("/protected",
		RequireRoleOrSystemAdmin(types.TenantRoleAdmin, cfgRBAC(true)),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/protected", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireRoleOrSystemAdmin_RejectsOrdinaryViewer(t *testing.T) {
	w := rbacTestHarness(types.TenantRoleViewer, "u1",
		RequireRoleOrSystemAdmin(types.TenantRoleAdmin, cfgRBAC(true)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("ordinary Viewer must not clear Admin-or-SystemAdmin gate, got %d", w.Code)
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

func TestRequireRole_CrossTenantSuperuserBypass(t *testing.T) {
	// Org-level superusers (User.CanAccessAllTenants) bypass tenant role
	// gates — see auth.go's resolveTenantRole, which gives them a
	// transient Admin in foreign tenants. RequireRole has to honour the
	// same bypass for Owner-only gates, otherwise a superuser would be
	// locked out of DELETE /tenants/:id once enforcement turns on.
	//
	// Pinned as a regression test: if anyone reorders the fast paths so
	// the superuser check ends up after the enforcement branch, this
	// test fails before the change ships.
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
		ctx = context.WithValue(ctx, types.UserIDContextKey, "su1")
		ctx = context.WithValue(ctx, types.UserContextKey, &types.User{
			ID: "su1", CanAccessAllTenants: true,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET("/protected",
		RequireRole(types.TenantRoleOwner, cfgRBACWithCrossTenant(true)),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) },
	)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("superuser must bypass Owner role gate, got %d", w.Code)
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

func TestRequireOwnershipOrRole_LookupErrorReturns503(t *testing.T) {
	// A transient lookup error surfaces as 503 (not 403) so monitoring
	// and clients can tell "your permission was denied" from "the server
	// briefly couldn't verify ownership". Failing open here would mean
	// any DB hiccup on the creator query becomes a free pass.
	lookup := func(c *gin.Context) (string, error) { return "", errors.New("boom") }
	w := rbacTestHarness(types.TenantRoleContributor, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup error must surface as 503, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_NotFoundPassesThroughTo404(t *testing.T) {
	// When the lookup signals "no such resource visible to this tenant",
	// the middleware MUST NOT mask it as 403. The handler downstream
	// gets to decide the right status (usually 404), which keeps client
	// error handling honest and avoids hiding "wrong URL" behind a
	// permissions error.
	called := false
	lookup := func(c *gin.Context) (string, error) {
		return "", ErrResourceNotFound
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleContributor)
		ctx = context.WithValue(ctx, types.UserIDContextKey, "u1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET("/protected",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(true)),
		func(c *gin.Context) {
			called = true
			c.JSON(http.StatusNotFound, gin.H{"error": "kb not found"})
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if !called {
		t.Fatalf("handler should have been invoked so it can produce 404")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected handler 404 to win, got %d", w.Code)
	}
}

func TestRequireOwnershipOrRole_SkipsLookupWhenRBACDisabled(t *testing.T) {
	// H1 regression: when enforcement is off, the lookup must not run at
	// all. Hooking up RBAC pre-rollout used to add a hidden DB roundtrip
	// to every mutating request even though the result was thrown away.
	calls := 0
	lookup := func(c *gin.Context) (string, error) {
		calls++
		return "someone-else", nil
	}
	w := rbacTestHarness(types.TenantRoleViewer, "u1",
		RequireOwnershipOrRole(types.TenantRoleAdmin, lookup, cfgRBAC(false)))
	if w.Code != http.StatusOK {
		t.Fatalf("fail-open should let the request through, got %d", w.Code)
	}
	if calls != 0 {
		t.Fatalf("lookup must not run when EnableRBAC=false (got %d calls)", calls)
	}
}

func TestRequireOwnershipOrRole_CrossTenantSuperuserBypass(t *testing.T) {
	// Cross-tenant superusers resolve to Admin in foreign tenants (see
	// resolveTenantRole). For Owner-only gates we additionally let them
	// through to preserve the pre-RBAC ability to administer any tenant.
	calls := 0
	lookup := func(c *gin.Context) (string, error) {
		calls++
		return "", nil
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
		ctx = context.WithValue(ctx, types.UserIDContextKey, "su1")
		ctx = context.WithValue(ctx, types.UserContextKey, &types.User{
			ID: "su1", CanAccessAllTenants: true,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET("/protected",
		RequireOwnershipOrRole(types.TenantRoleOwner, lookup, cfgRBACWithCrossTenant(true)),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) },
	)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("superuser must bypass Owner gate, got %d", w.Code)
	}
	if calls != 0 {
		t.Fatalf("superuser bypass must skip lookup, got %d", calls)
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
