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

// access_test.go covers the four exported helpers in access.go:
// IsCrossTenantSuperuser, IsTenantAccessible, RequireCrossTenantAccess,
// RequirePathTenantMatch. The shapes are similar — feature-flag +
// per-user attribute combinations — so the tests are organised around
// the truth-table for each helper rather than per-handler scenarios.

// cfgCrossTenant returns a config with the cluster-wide
// EnableCrossTenantAccess flag set as requested. EnableRBAC is left at
// its zero value because none of these helpers consult it directly —
// the role middleware does.
func cfgCrossTenant(enabled bool) *config.Config {
	return &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: enabled}}
}

// ---------- IsCrossTenantSuperuser ----------

func TestIsCrossTenantSuperuser_NilCfgRejects(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.UserContextKey,
		&types.User{ID: "u1", CanAccessAllTenants: true})
	if IsCrossTenantSuperuser(ctx, nil) {
		t.Fatalf("nil cfg must reject (no flag implies no cross-tenant access)")
	}
}

func TestIsCrossTenantSuperuser_FlagOffRejectsEvenWithAttribute(t *testing.T) {
	// The user attribute alone is not enough — by design. This is the
	// "stale claim revocation" guarantee: flipping the cluster flag off
	// disables every CanAccessAllTenants user without a re-issue.
	ctx := context.WithValue(context.Background(), types.UserContextKey,
		&types.User{ID: "u1", CanAccessAllTenants: true})
	if IsCrossTenantSuperuser(ctx, cfgCrossTenant(false)) {
		t.Fatalf("flag off must reject even when User.CanAccessAllTenants=true")
	}
}

func TestIsCrossTenantSuperuser_FlagOnNoAttributeRejects(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.UserContextKey,
		&types.User{ID: "u1", CanAccessAllTenants: false})
	if IsCrossTenantSuperuser(ctx, cfgCrossTenant(true)) {
		t.Fatalf("ordinary user must not pass even when flag is on")
	}
}

func TestIsCrossTenantSuperuser_BothAllow(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.UserContextKey,
		&types.User{ID: "u1", CanAccessAllTenants: true})
	if !IsCrossTenantSuperuser(ctx, cfgCrossTenant(true)) {
		t.Fatalf("flag on + attribute true must allow")
	}
}

func TestIsCrossTenantSuperuser_NoUserInCtxRejects(t *testing.T) {
	// Defensive: a context lacking UserContextKey should never be
	// promoted. Returning false rather than panicking keeps the caller
	// (auth.go fast paths) safe to use even before user resolution.
	if IsCrossTenantSuperuser(context.Background(), cfgCrossTenant(true)) {
		t.Fatalf("missing user must reject")
	}
}

// ---------- IsTenantAccessible ----------

func TestIsTenantAccessible_HomeTenantAlwaysAllows(t *testing.T) {
	user := &types.User{ID: "u1", TenantID: 1}
	// Even with no member service and the cross-tenant flag off, a user
	// reaching their own tenant is fine. Home tenant is the cheapest
	// fast path.
	if !IsTenantAccessible(context.Background(), user, 1, nil, cfgCrossTenant(false)) {
		t.Fatalf("home tenant must allow regardless of other inputs")
	}
}

func TestIsTenantAccessible_NilUserRejects(t *testing.T) {
	if IsTenantAccessible(context.Background(), nil, 1, nil, cfgCrossTenant(true)) {
		t.Fatalf("nil user must reject (defensive)")
	}
}

func TestIsTenantAccessible_ZeroTargetRejects(t *testing.T) {
	user := &types.User{ID: "u1", TenantID: 1}
	if IsTenantAccessible(context.Background(), user, 0, nil, cfgCrossTenant(true)) {
		t.Fatalf("zero target tenant must reject")
	}
}

func TestIsTenantAccessible_SuperuserPathRequiresFlag(t *testing.T) {
	user := &types.User{ID: "u1", TenantID: 1, CanAccessAllTenants: true}
	// With flag OFF, the superuser attribute alone must NOT grant
	// cross-tenant access — same revocation rule as
	// IsCrossTenantSuperuser.
	if IsTenantAccessible(context.Background(), user, 99, nil, cfgCrossTenant(false)) {
		t.Fatalf("superuser without cluster flag must reject cross-tenant target")
	}
	// With the flag ON, no membership lookup is needed — the user can
	// reach any tenant.
	if !IsTenantAccessible(context.Background(), user, 99, nil, cfgCrossTenant(true)) {
		t.Fatalf("superuser with cluster flag must allow without membership lookup")
	}
}

func TestIsTenantAccessible_ActiveMembershipAllows(t *testing.T) {
	user := &types.User{ID: "u1", TenantID: 1}
	ms := newFakeMemberService()
	ms.seedActive("u1", 99, types.TenantRoleContributor)
	if !IsTenantAccessible(context.Background(), user, 99, ms, cfgCrossTenant(false)) {
		t.Fatalf("active membership must allow even with flag off and no superuser")
	}
}

func TestIsTenantAccessible_NoMembershipRejects(t *testing.T) {
	user := &types.User{ID: "u1", TenantID: 1}
	ms := newFakeMemberService() // empty
	if IsTenantAccessible(context.Background(), user, 99, ms, cfgCrossTenant(true)) {
		t.Fatalf("no membership and not superuser must reject")
	}
}

func TestIsTenantAccessible_LookupErrorRejects(t *testing.T) {
	// A DB hiccup must NOT silently grant access; the safest behaviour
	// is "treat as no membership". The X-Tenant-ID gate in auth.go
	// relies on this — failing closed prevents tenant-bleed during
	// transient outages.
	user := &types.User{ID: "u1", TenantID: 1}
	ms := newFakeMemberService()
	ms.failGet = errors.New("db down")
	if IsTenantAccessible(context.Background(), user, 99, ms, cfgCrossTenant(true)) {
		t.Fatalf("lookup error must reject (fail closed)")
	}
}

func TestIsTenantAccessible_NilMemberServiceRejectsNonHome(t *testing.T) {
	user := &types.User{ID: "u1", TenantID: 1}
	if IsTenantAccessible(context.Background(), user, 99, nil, cfgCrossTenant(false)) {
		t.Fatalf("no member service + non-home tenant must reject")
	}
}

// ---------- RequireCrossTenantAccess ----------

func runCrossTenantHandler(cfg *config.Config, user *types.User) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			ctx := context.WithValue(c.Request.Context(), types.UserContextKey, user)
			ctx = context.WithValue(ctx, types.UserIDContextKey, user.ID)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	r.GET("/cross", RequireCrossTenantAccess(cfg), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cross", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestRequireCrossTenantAccess_FlagOffBlocksEveryone(t *testing.T) {
	w := runCrossTenantHandler(cfgCrossTenant(false),
		&types.User{ID: "u1", CanAccessAllTenants: true})
	if w.Code != http.StatusForbidden {
		t.Fatalf("flag off must 403 even for superuser, got %d", w.Code)
	}
}

func TestRequireCrossTenantAccess_FlagOnNoAttributeBlocks(t *testing.T) {
	w := runCrossTenantHandler(cfgCrossTenant(true),
		&types.User{ID: "u1", CanAccessAllTenants: false})
	if w.Code != http.StatusForbidden {
		t.Fatalf("ordinary user must 403 even with flag on, got %d", w.Code)
	}
}

func TestRequireCrossTenantAccess_BothAllow(t *testing.T) {
	w := runCrossTenantHandler(cfgCrossTenant(true),
		&types.User{ID: "u1", CanAccessAllTenants: true})
	if w.Code != http.StatusOK {
		t.Fatalf("flag + superuser must allow, got %d", w.Code)
	}
}

func TestRequireCrossTenantAccess_NoUserBlocks(t *testing.T) {
	w := runCrossTenantHandler(cfgCrossTenant(true), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing user must 403, got %d", w.Code)
	}
}

// ---------- RequirePathTenantMatch ----------

func runPathTenantHandler(
	cfg *config.Config, ctxTenantID uint64, user *types.User, urlPath string,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		if ctxTenantID != 0 {
			ctx = context.WithValue(ctx, types.TenantIDContextKey, ctxTenantID)
		}
		if user != nil {
			ctx = context.WithValue(ctx, types.UserContextKey, user)
			ctx = context.WithValue(ctx, types.UserIDContextKey, user.ID)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/tenants/:id", RequirePathTenantMatch(cfg), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, urlPath, nil)
	r.ServeHTTP(w, req)
	return w
}

func TestRequirePathTenantMatch_MatchAllows(t *testing.T) {
	w := runPathTenantHandler(cfgCrossTenant(false), 7, nil, "/tenants/7")
	if w.Code != http.StatusOK {
		t.Fatalf("matching :id must allow, got %d", w.Code)
	}
}

func TestRequirePathTenantMatch_MismatchRejects(t *testing.T) {
	w := runPathTenantHandler(cfgCrossTenant(false), 7, nil, "/tenants/9")
	if w.Code != http.StatusForbidden {
		t.Fatalf("mismatch must 403, got %d", w.Code)
	}
}

func TestRequirePathTenantMatch_SuperuserBypassesMismatch(t *testing.T) {
	// Pinned regression: this is the same carve-out
	// resolveTenantIDFromPath used to enforce in tenant_member.go.
	// Both flag and attribute must be set; either alone must reject
	// (covered by the next test).
	w := runPathTenantHandler(cfgCrossTenant(true), 7,
		&types.User{ID: "su1", CanAccessAllTenants: true},
		"/tenants/9")
	if w.Code != http.StatusOK {
		t.Fatalf("superuser must bypass mismatch with flag on, got %d", w.Code)
	}
}

func TestRequirePathTenantMatch_SuperuserNeedsClusterFlag(t *testing.T) {
	w := runPathTenantHandler(cfgCrossTenant(false), 7,
		&types.User{ID: "su1", CanAccessAllTenants: true},
		"/tenants/9")
	if w.Code != http.StatusForbidden {
		t.Fatalf("superuser without flag must 403, got %d", w.Code)
	}
}

func TestRequirePathTenantMatch_MissingCtxTenantRejects(t *testing.T) {
	// If the auth middleware never set TenantIDContextKey we fail
	// closed — silently treating "no ctx" as a match would be a footgun
	// the next time someone refactors the auth chain.
	w := runPathTenantHandler(cfgCrossTenant(true), 0, nil, "/tenants/7")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing tenant ctx must 401, got %d", w.Code)
	}
}

func TestRequirePathTenantMatch_NonNumericIDRejects(t *testing.T) {
	w := runPathTenantHandler(cfgCrossTenant(true), 7, nil, "/tenants/abc")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric :id must 400, got %d", w.Code)
	}
}

func TestRequirePathTenantMatch_ZeroIDRejects(t *testing.T) {
	w := runPathTenantHandler(cfgCrossTenant(true), 7, nil, "/tenants/0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf(":id=0 must 400, got %d", w.Code)
	}
}
