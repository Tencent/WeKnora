package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ErrResourceNotFound is the sentinel a CreatorLookup returns when the
// :id on the request does not match any row the lookup can see (either
// the row is genuinely missing or its tenant doesn't match). When a
// lookup returns this error, RequireOwnershipOrRole intentionally lets
// the request proceed so the downstream handler can respond with its
// own 404 — middleware-level 403 would hide real "URL is wrong" failures
// behind a permissions error, which breaks client diagnostics and
// operator dashboards.
var ErrResourceNotFound = access.ErrResourceNotFound

// CreatorLookup resolves the creator user ID for the resource targeted
// by the current request, based on whatever is on the gin.Context (URL
// params, query, body). Implementations live next to the handlers they
// guard, e.g. handler.kbCreatorLookup(c) reads ":id" and returns
// KnowledgeBase.CreatorID.
//
// Return value contract:
//   - (creatorID, nil) where creatorID != ""  -> the resource has a
//     recorded owner; ownership match grants access.
//   - ("", nil)                                -> "tenant-owned": no
//     human creator was recorded (legacy row or built-in resource);
//     only callers whose role meets the bar may proceed.
//   - ("", ErrResourceNotFound)                -> the :id does not
//     resolve to any row visible to this caller's tenant. Middleware
//     proceeds to the handler so the handler can return 404 instead
//     of masking it as 403.
//   - ("", other error)                        -> transient or
//     unexpected failure (DB hiccup, etc.). Middleware returns 503
//     when enforcement is on so monitoring catches the real fault;
//     when enforcement is off, it logs and lets the request through.
type CreatorLookup func(c *gin.Context) (creatorID string, err error)

// RequireRole returns a gin middleware that aborts the request with
// HTTP 403 unless the caller's TenantRole (set by the auth middleware
// in TenantRoleContextKey) is at least min.
//
// Cross-tenant superusers (User.CanAccessAllTenants) automatically
// satisfy any role gate. Otherwise rolling out tenant-RBAC would silently
// break organisation-level operators who own no tenant_members row in
// the tenant they're administering. The escape hatch is bounded by the
// existing canAccessTenant gate in auth.go; this middleware does not
// grant extra reach, only honours what was already approved.
//
// When cfg.Tenant.EnableRBAC is false, the middleware logs the would-be
// rejection but lets the request through — preserving today's behaviour
// during the rollout window. Once operators flip the flag to true,
// the same code paths start rejecting unauthorised callers.
//
// The auth middleware always sets a TenantRole; if for some reason it
// is missing, TenantRoleFromContext defaults to TenantRoleViewer, which
// is the safest fail-closed value: anything that requires more than
// Viewer will reject.
func RequireRole(min types.TenantRole, cfg *config.Config) gin.HandlerFunc {
	warnOnNilConfig(cfg)
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		// API-key principals are authorized solely by the APIKeyGate
		// (role + KB scope + default-deny). The JWT role ladder does not
		// apply to a machine principal, so short-circuit here.
		if _, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
			c.Next()
			return
		}
		role := types.CallerFromContext(ctx).Role
		if role.HasPermission(min) {
			c.Next()
			return
		}
		if IsCrossTenantSuperuser(ctx, cfg) {
			c.Next()
			return
		}
		uid, _ := types.UserIDFromContext(ctx)
		if !rbacEnforcementEnabled(cfg) {
			logger.Warnf(ctx,
				"[rbac] role insufficient (logged but not enforced): user=%s have=%s need=%s path=%s",
				uid, role, min, c.Request.URL.Path)
			c.Next()
			return
		}
		logger.Warnf(ctx,
			"[rbac] role insufficient: user=%s have=%s need=%s path=%s",
			uid, role, min, c.Request.URL.Path)
		// Durable audit row for the reject. AuditServiceProvider
		// injects the service; subject to 1-minute sliding-window
		// dedup inside the service so probing clients can't fill the
		// table.
		if svc := AuditServiceFromContext(c); svc != nil {
			tenantID := types.CallerFromContext(ctx).TenantID
			_ = svc.LogDenied(ctx, c, tenantID, uid, string(role), min)
		}
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Forbidden: insufficient workspace role",
		})
		c.Abort()
	}
}

// RequireRoleOrSystemAdmin applies the tenant role floor while also allowing
// platform system administrators. Use it for routes that normally mutate
// tenant infrastructure but have a narrowly-scoped platform-owned resource
// (for example built-in models) that system administrators must be able to
// maintain independently of their role in the active tenant.
//
// API-key behavior remains identical to RequireRole: the APIKeyGate is the
// source of truth for machine principals, so they short-circuit the role
// check here as well.
func RequireRoleOrSystemAdmin(min types.TenantRole, cfg *config.Config) gin.HandlerFunc {
	requireRole := RequireRole(min, cfg)
	return func(c *gin.Context) {
		if types.IsSystemAdminFromContext(c.Request.Context()) {
			c.Next()
			return
		}
		requireRole(c)
	}
}

// RequireSystemAdmin returns a gin middleware that aborts the request with
// HTTP 403 unless the caller is a system administrator
// (User.IsSystemAdmin = true).
//
// System administrators operate independently of tenant-scoped roles and
// are not bound by the per-tenant RBAC matrix. Use this guard for
// platform-wide administrative endpoints (managing other system admins,
// editing global settings, cross-workspace operations) where the per-tenant
// Owner/Admin/Contributor/Viewer ladder does not apply.
//
// Unlike tenant-role guards, this check is always enforced. The
// tenant RBAC rollout switch only controls per-tenant Owner/Admin/etc.
// checks; it must not turn platform-wide administration endpoints into
// "any authenticated user can call this" endpoints.
func RequireSystemAdmin(cfg *config.Config) gin.HandlerFunc {
	warnOnNilConfig(cfg)
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		// The API-key gate runs before this guard and default-denies undeclared
		// routes. Only platform keys that passed an explicit platform capability
		// policy may reuse the system-admin handlers below.
		if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
			if scope.IsPlatform() {
				c.Next()
				return
			}
			logger.Warnf(ctx,
				"[rbac] system admin required: API-key principal denied path=%s",
				c.Request.URL.Path)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: API keys cannot access this endpoint",
			})
			c.Abort()
			return
		}
		if types.IsSystemAdminFromContext(ctx) {
			c.Next()
			return
		}
		uid, _ := types.UserIDFromContext(ctx)
		logger.Warnf(ctx,
			"[rbac] system admin required: user=%s path=%s",
			uid, c.Request.URL.Path)
		// Durable audit row for the reject — same dedup as RequireRole.
		if svc := AuditServiceFromContext(c); svc != nil {
			tenantID := types.CallerFromContext(ctx).TenantID
			_ = svc.LogDenied(ctx, c, tenantID, uid, "user", "system_admin")
		}
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Forbidden: system administrator required",
		})
		c.Abort()
	}
}

// RequireOwnershipOrRole guards endpoints whose access is allowed for
// either (a) callers whose role is at least min, or (b) the original
// creator of the resource being touched.
//
// Use it for KB / agent mutations where Contributors should only manage
// their own resources but Admins+ have free reign. The lookup closure
// is responsible for translating the URL into the resource's creator
// user ID (see CreatorLookup for the return-value contract).
//
// API-key principals skip this human ownership gate; route capabilities and
// allowed KB scope remain mandatory. For human callers, decision order is:
//  1. role >= min -> allow without running lookup.
//  2. cross-tenant superuser -> allow without running lookup.
//  3. enforcement off -> log, allow without running lookup. This is the
//     critical rollout-safety guarantee: when EnableRBAC is false, the
//     lookup is NEVER invoked, so dormant mode incurs zero extra DB
//     roundtrips on hot per-resource mutation paths.
//  4. lookup returns ErrResourceNotFound -> pass through; let the
//     handler issue the proper 404.
//  5. lookup returns other error -> 503 (transient/unexpected fault).
//     Preserves observability instead of disguising server errors as 403.
//  6. lookup returns the caller's user ID -> allow (ownership match).
//  7. lookup returns "" (tenant-owned, no human creator) -> 403 (we
//     already know role < min from step 1).
//  8. lookup returns a non-empty creator that is not the caller -> 403.
func RequireOwnershipOrRole(min types.TenantRole, lookup CreatorLookup, cfg *config.Config) gin.HandlerFunc {
	warnOnNilConfig(cfg)
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		request := ownershipRequestFromContext(ctx, cfg)
		var resolveCreator func() (string, error)
		if lookup != nil {
			resolveCreator = func() (string, error) { return lookup(c) }
		}
		decision, err := access.CheckOwnershipOrRole(request, min, resolveCreator)
		logOwnershipDecision(ctx, request, min, decision, err, c.Request.URL.Path)
		switch {
		case err == nil, errors.Is(err, ErrResourceNotFound):
			// A missing resource remains the handler's responsibility so it
			// can produce its existing 404 response.
			c.Next()
			return
		case errors.Is(err, ErrOwnershipForbidden) && !decision.LookupFailed:
			if svc := AuditServiceFromContext(c); svc != nil {
				tenantID := types.CallerFromContext(ctx).TenantID
				_ = svc.LogDenied(ctx, c, tenantID, request.UserID, string(request.Role), min)
			}
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: must own the resource or have the required role",
			})
		default:
			logger.Errorf(ctx,
				"[rbac] creator lookup failed: user=%s path=%s err=%v",
				request.UserID, c.Request.URL.Path, err)
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "Service Unavailable: cannot verify resource ownership",
			})
		}
		c.Abort()
	}
}

// rbacEnforcementEnabled reports whether middleware should actually
// reject failed checks. When the flag is off the middleware still runs
// role-only checks (logging, fast paths), but rejection is downgraded
// to a warning and ownership lookups are skipped entirely so the dormant
// rollout window incurs no per-request DB cost.
func rbacEnforcementEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Tenant.IsRBACEnforced()
}

// ErrOwnershipForbidden is returned by EvaluateOwnershipOrRole when the
// caller is neither the resource creator nor meets the minimum role.
var ErrOwnershipForbidden = access.ErrOwnershipForbidden

// EvaluateOwnershipOrRole is the handler adapter for the same lazy decision
// used by RequireOwnershipOrRole. Body/query-based callers supply a closure so
// ownership lookup is skipped on exactly the same fast paths as URL routes.
// Not-found, forbidden and lookup failures remain distinct; each handler maps
// them to its existing response format and status.
func EvaluateOwnershipOrRole(
	ctx context.Context,
	cfg *config.Config,
	min types.TenantRole,
	lookup func() (string, error),
) error {
	request := ownershipRequestFromContext(ctx, cfg)
	decision, err := access.CheckOwnershipOrRole(request, min, lookup)
	logOwnershipDecision(ctx, request, min, decision, err, "")
	return err
}

func ownershipRequestFromContext(ctx context.Context, cfg *config.Config) access.OwnershipRequest {
	caller := types.CallerFromContext(ctx)
	_, apiKey := types.TenantAPIKeyScopeFromContext(ctx)
	return access.OwnershipRequest{
		UserID:               caller.UserID,
		Role:                 caller.Role,
		APIKey:               apiKey,
		CrossTenantSuperuser: IsCrossTenantSuperuser(ctx, cfg),
		Enforce:              rbacEnforcementEnabled(cfg),
	}
}

func logOwnershipDecision(
	ctx context.Context,
	request access.OwnershipRequest,
	required types.TenantRole,
	decision access.OwnershipDecision,
	err error,
	path string,
) {
	switch {
	case decision.EnforcementSkipped:
		logger.Warnf(ctx,
			"[rbac] ownership/role would be checked (enforcement off, lookup skipped): user=%s have=%s need=%s path=%s",
			request.UserID, request.Role, required, path)
	case errors.Is(err, ErrOwnershipForbidden) && !decision.LookupFailed:
		logger.Warnf(ctx,
			"[rbac] ownership/role insufficient: user=%s have=%s need=%s creator=%q path=%s",
			request.UserID, request.Role, required, decision.CreatorID, path)
	}
}

// isCrossTenantSuperuser was moved to access.go (renamed to
// IsCrossTenantSuperuser, exported, and made flag-aware) so the same
// helper backs the X-Tenant-ID gate in auth.go and the RequireRole /
// RequireOwnershipOrRole guards above.

// warnOnNilConfig emits a one-shot startup warning when a guard is
// constructed with a nil-or-incomplete config. nil cfg makes
// rbacEnforcementEnabled return false, which means an entire deployment
// silently runs with RBAC disabled — usually because of a configuration
// bug rather than an intentional choice. Operators should see a noisy
// log line at boot pointing at the misconfiguration.
var nilCfgWarnOnce sync.Once

func warnOnNilConfig(cfg *config.Config) {
	if cfg != nil && cfg.Tenant != nil {
		return
	}
	nilCfgWarnOnce.Do(func() {
		logger.Errorf(context.Background(),
			"[rbac] middleware constructed with nil/incomplete config "+
				"(cfg=%v); enforcement is permanently disabled. This is "+
				"almost certainly a wiring bug.", cfg)
	})
}
