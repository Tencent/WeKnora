package middleware

import (
	"context"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// access.go centralises the tenant-access checks that previously lived
// scattered across handlers and the various RBAC middlewares:
//
//   - "is the caller an org-level superuser" — used by the role guards
//     in rbac.go, the X-Tenant-ID branch in auth.go, the tenant
//     handler's authorizeTenantAccess, and the tenant member handler's
//     resolveTenantIDFromPath.
//
//   - "can the caller access target tenant X" — used by the
//     X-Tenant-ID branch in auth.go to decide whether to honour the
//     header. Originally allowed only superusers; now also allows
//     ordinary multi-tenant members who have an active row in the
//     target tenant's tenant_members table.
//
//   - "must the URL :id match the active tenant" — used by every
//     tenant-scoped route. Was duplicated in tenant.go (4 callers) and
//     tenant_member.go (1 caller) before; now lives here as a route
//     middleware so the route declaration is the single source of
//     truth.
//
// All of these honour cfg.Tenant.EnableCrossTenantAccess: the
// CanAccessAllTenants attribute on a User row is meaningful only when
// the cluster-wide flag is on. Reading the attribute alone (without the
// flag) would let a dormant config grant escalation, which is exactly
// what bit us during the PR 3 review.
//
// Rejections are reported via c.Error(*apperrors.AppError) + c.Abort(),
// not c.JSON: this keeps the response shape consistent with the
// {success,error:{code,message,details}} envelope that ErrorHandler
// renders for the rest of the API. The handler-level checks these
// guards replaced (authorizeTenantAccess, resolveTenantIDFromPath,
// ListAllTenants/SearchTenants if-blocks) all used that envelope, so
// preserving it avoids breaking SDK / frontend consumers that key off
// `error.code`.

// IsCrossTenantSuperuser reports whether ctx carries a user that is
// authorised for cross-tenant access at this moment. Both the user
// attribute and the cluster-wide flag must be true; either alone is
// not enough.
func IsCrossTenantSuperuser(ctx context.Context, cfg *config.Config) bool {
	if cfg == nil || cfg.Tenant == nil || !cfg.Tenant.EnableCrossTenantAccess {
		return false
	}
	u, ok := ctx.Value(types.UserContextKey).(*types.User)
	if !ok || u == nil {
		return false
	}
	return u.CanAccessAllTenants
}

// IsTenantAccessible reports whether `user` is allowed to operate inside
// `targetTenantID`. The decision order is:
//
//  1. Home tenant (user.TenantID == targetTenantID): always.
//  2. Cross-tenant superuser: governed by IsCrossTenantSuperuser.
//  3. Multi-tenant member: an active tenant_members row in the target
//     tenant grants access (this is what makes cross-tenant browsing
//     work for non-superusers added via PR 3's member management).
//
// Lookup errors are treated as "not a member" — the safest fallback
// that doesn't expose other tenants on a transient DB hiccup.
func IsTenantAccessible(
	ctx context.Context,
	user *types.User,
	targetTenantID uint64,
	memberService interfaces.TenantMemberService,
	cfg *config.Config,
) bool {
	if user == nil || targetTenantID == 0 {
		return false
	}
	if user.TenantID == targetTenantID {
		return true
	}
	if cfg != nil && cfg.Tenant != nil && cfg.Tenant.EnableCrossTenantAccess && user.CanAccessAllTenants {
		return true
	}
	if memberService == nil {
		return false
	}
	m, err := memberService.GetMembership(ctx, user.ID, targetTenantID)
	if err != nil || m == nil {
		return false
	}
	return m.Status == types.TenantMemberStatusActive
}

// RequireCrossTenantAccess gates a route on the caller being an
// org-level superuser. Used by /tenants/all, /tenants/search,
// POST /tenants, GET /tenants — endpoints that operate across tenants
// rather than inside one.
//
// Unlike RequireRole this is NOT modulated by cfg.Tenant.EnableRBAC:
// cross-workspace operations are always sensitive regardless of whether
// per-tenant RBAC is being enforced, so we never log-and-pass.
func RequireCrossTenantAccess(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok && scope.IsPlatform() {
			c.Next()
			return
		}
		// First the cluster-wide flag — if it's off, nobody gets through,
		// not even users with CanAccessAllTenants=true. This mirrors the
		// "must require BOTH" rule that previously lived in tenant.go.
		if cfg == nil || cfg.Tenant == nil || !cfg.Tenant.EnableCrossTenantAccess {
			uid, _ := types.UserIDFromContext(ctx)
			logger.Warnf(ctx,
				"[rbac] cross-tenant route blocked (EnableCrossTenantAccess=false): user=%s path=%s",
				uid, c.Request.URL.Path)
			_ = c.Error(apperrors.NewForbiddenError("Cross-workspace access is disabled"))
			c.Abort()
			return
		}
		u, ok := ctx.Value(types.UserContextKey).(*types.User)
		if !ok || u == nil || !u.CanAccessAllTenants {
			uid, _ := types.UserIDFromContext(ctx)
			logger.Warnf(ctx,
				"[rbac] cross-tenant route blocked (not a superuser): user=%s path=%s",
				uid, c.Request.URL.Path)
			_ = c.Error(apperrors.NewForbiddenError(
				"Insufficient permissions for cross-workspace operation"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequirePathTenantMatch gates a route on the URL :id matching the
// caller's active tenant context. Cross-tenant superusers bypass the
// match because their X-Tenant-ID switch was already vetted by the
// auth middleware.
//
// The router places this on the /tenants/:id group so every per-tenant
// endpoint (GetTenant / UpdateTenant / DeleteTenant /
// member management / leave) shares the same check, replacing what was
// previously a copy-pasted block in each handler.
//
// Reads :id off c.Param. If the param is missing or non-numeric the
// request is rejected as 400 — the route can't have meaningfully
// matched without it.
func RequirePathTenantMatch(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		raw := strings.TrimSpace(c.Param("id"))
		if raw == "" {
			_ = c.Error(apperrors.NewValidationError("workspace id is required"))
			c.Abort()
			return
		}
		pathTenantID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || pathTenantID == 0 {
			_ = c.Error(apperrors.NewValidationError("workspace id must be a positive integer"))
			c.Abort()
			return
		}
		ctxTenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || ctxTenantID == 0 {
			// Auth middleware should always have set this; if not we
			// fail closed rather than silently treating "no context" as
			// a match.
			logger.Warnf(ctx, "[rbac] path-tenant-match: no tenant in ctx, path=%s", c.Request.URL.Path)
			_ = c.Error(apperrors.NewUnauthorizedError("workspace context missing"))
			c.Abort()
			return
		}
		if pathTenantID == ctxTenantID {
			c.Next()
			return
		}
		if IsCrossTenantSuperuser(ctx, cfg) {
			c.Next()
			return
		}
		uid, _ := types.UserIDFromContext(ctx)
		logger.Warnf(ctx,
			"[rbac] path-tenant-match rejected: user=%s ctx_tenant=%d path_tenant=%d path=%s",
			uid, ctxTenantID, pathTenantID, c.Request.URL.Path)
		_ = c.Error(apperrors.NewForbiddenError(
			"Access denied: URL workspace does not match the active workspace"))
		c.Abort()
	}
}
