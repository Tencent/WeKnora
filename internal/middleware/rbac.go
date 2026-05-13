package middleware

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// CreatorLookup resolves the creator user ID for the resource targeted
// by the current request, based on whatever is on the gin.Context (URL
// params, query, body). Implementations live next to the handlers they
// guard, e.g. handler.kbCreatorLookup(c) reads ":id" and returns
// KnowledgeBase.CreatorID.
//
// Returning ("", nil) means "no creator recorded" — the resource pre-dates
// the migration backfill. RequireOwnershipOrRole treats this as
// "tenant-owned" and requires the role check to pass.
//
// Any non-nil error short-circuits the request with 403; callers should
// distinguish "not found" (404 in the handler if needed) from genuine
// failures inside their lookup helper.
type CreatorLookup func(c *gin.Context) (creatorID string, err error)

// RequireRole returns a gin middleware that aborts the request with
// HTTP 403 unless the caller's TenantRole (set by the auth middleware
// in TenantRoleContextKey) is at least min.
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
	return func(c *gin.Context) {
		role := types.TenantRoleFromContext(c.Request.Context())
		if role.HasPermission(min) {
			c.Next()
			return
		}
		uid, _ := types.UserIDFromContext(c.Request.Context())
		if !rbacEnforcementEnabled(cfg) {
			logger.Warnf(c.Request.Context(),
				"[rbac] role insufficient (logged but not enforced): user=%s have=%s need=%s path=%s",
				uid, role, min, c.Request.URL.Path)
			c.Next()
			return
		}
		logger.Warnf(c.Request.Context(),
			"[rbac] role insufficient: user=%s have=%s need=%s path=%s",
			uid, role, min, c.Request.URL.Path)
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Forbidden: insufficient tenant role",
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
// user ID.
//
// Decision order:
//  1. role >= min -> allow.
//  2. lookup returns the caller's user ID -> allow.
//  3. lookup returns a non-empty different user ID -> deny (or log when
//     enforcement is disabled).
//  4. lookup returns an empty creator ID (legacy / unmigrated row) ->
//     treat as tenant-owned; only role >= min may proceed. Effectively
//     equivalent to step 1 for that row.
//  5. lookup returns an error -> deny (don't fail open on lookup errors:
//     a broken creator query is more often a bug than a transient blip,
//     and treating a load failure as "approve" would be a footgun).
//
// Like RequireRole, when cfg.Tenant.EnableRBAC is false the middleware
// logs but does not block.
func RequireOwnershipOrRole(min types.TenantRole, lookup CreatorLookup, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		role := types.TenantRoleFromContext(ctx)

		// Fast path: role meets the bar, skip the lookup entirely.
		if role.HasPermission(min) {
			c.Next()
			return
		}

		uid, _ := types.UserIDFromContext(ctx)
		creator, err := lookup(c)
		if err != nil {
			logger.Warnf(ctx,
				"[rbac] creator lookup failed: user=%s path=%s err=%v",
				uid, c.Request.URL.Path, err)
			if !rbacEnforcementEnabled(cfg) {
				c.Next()
				return
			}
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Forbidden: cannot verify resource ownership",
			})
			c.Abort()
			return
		}

		// Ownership match wins even when role is below min — that's the
		// whole point: Contributors can edit their own resources.
		if creator != "" && creator == uid {
			c.Next()
			return
		}

		if !rbacEnforcementEnabled(cfg) {
			logger.Warnf(ctx,
				"[rbac] ownership/role insufficient (logged, not enforced): "+
					"user=%s have=%s need=%s creator=%q path=%s",
				uid, role, min, creator, c.Request.URL.Path)
			c.Next()
			return
		}

		logger.Warnf(ctx,
			"[rbac] ownership/role insufficient: user=%s have=%s need=%s creator=%q path=%s",
			uid, role, min, creator, c.Request.URL.Path)
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Forbidden: must own the resource or have the required role",
		})
		c.Abort()
	}
}

// rbacEnforcementEnabled reports whether middleware should actually
// reject failed checks. When the flag is off the middleware still runs
// (so role lookups exercise the membership table and any logging fires)
// but rejection is downgraded to a warning. Mirrors the pattern in
// resolveTenantRole's fail-open branch in middleware/auth.go.
func rbacEnforcementEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Tenant != nil && cfg.Tenant.EnableRBAC
}
