package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// 无需认证的API列表
var noAuthAPI = map[string][]string{
	"/health":                    {"GET"},
	"/api/v1/auth/register":      {"POST"},
	"/api/v1/auth/login":         {"POST"},
	"/api/v1/auth/auto-setup":    {"POST"},
	"/api/v1/auth/config":        {"GET"},
	"/api/v1/auth/oidc/config":   {"GET"},
	"/api/v1/auth/oidc/url":      {"GET"},
	"/api/v1/auth/oidc/callback": {"GET"},
	"/api/v1/auth/refresh":       {"POST"},
	"/api/v1/files/presigned":    {"GET"},
}

// 检查请求是否在无需认证的API列表中
func isNoAuthAPI(path string, method string) bool {
	for api, methods := range noAuthAPI {
		// 如果以*结尾，按照前缀匹配，否则按照全路径匹配
		if strings.HasSuffix(api, "*") {
			if strings.HasPrefix(path, strings.TrimSuffix(api, "*")) && slices.Contains(methods, method) {
				return true
			}
		} else if path == api && slices.Contains(methods, method) {
			return true
		}
	}
	return false
}

// Auth 认证中间件
func Auth(
	tenantService interfaces.TenantService,
	userService interfaces.UserService,
	memberService interfaces.TenantMemberService,
	cfg *config.Config,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ignore OPTIONS request
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// 检查请求是否在无需认证的API列表中
		if isNoAuthAPI(c.Request.URL.Path, c.Request.Method) {
			c.Next()
			return
		}

		// 尝试JWT Token认证
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			user, jwtTenantID, err := userService.ValidateToken(c.Request.Context(), token)
			if err == nil && user != nil {
				// JWT Token认证成功
				// 默认 target = JWT 里的 tenant_id（来自登录或 /auth/switch-tenant），
				// 兼容 ValidateToken 的 fallback：claim 缺失时 jwtTenantID == user.TenantID。
				targetTenantID := jwtTenantID
				if targetTenantID == 0 {
					targetTenantID = user.TenantID
				}
				crossTenantSwitch := targetTenantID != user.TenantID
				tenantHeader := c.GetHeader("X-Tenant-ID")
				if tenantHeader != "" {
					// 解析目标租户ID
					parsedTenantID, err := strconv.ParseUint(tenantHeader, 10, 64)
					if err == nil {
						// 检查用户是否有权限访问目标租户：自家租户、跨租户超管、或
						// 有 active membership 行——三选一，由 IsTenantAccessible
						// 统一判定。
						if IsTenantAccessible(c.Request.Context(), user, parsedTenantID, memberService, cfg) {
							// 验证目标租户是否存在
							targetTenant, err := tenantService.GetTenantByID(c.Request.Context(), parsedTenantID)
							if err == nil && targetTenant != nil {
								targetTenantID = parsedTenantID
								crossTenantSwitch = parsedTenantID != user.TenantID
								log.Printf("User %s switching to tenant %d", user.ID, targetTenantID)
							} else {
								log.Printf("Error getting target tenant by ID: %v, tenantID: %d", err, parsedTenantID)
								c.JSON(http.StatusBadRequest, gin.H{
									"error": "Invalid target tenant ID",
								})
								c.Abort()
								return
							}
						} else {
							// 用户没有权限访问目标租户
							log.Printf("User %s attempted to access tenant %d without permission", user.ID, parsedTenantID)
							c.JSON(http.StatusForbidden, gin.H{
								"error": "Forbidden: insufficient permissions to access target tenant",
							})
							c.Abort()
							return
						}
					}
				}

				// 获取租户信息（使用目标租户ID）
				tenant, err := tenantService.GetTenantByID(c.Request.Context(), targetTenantID)
				if err != nil {
					log.Printf("Error getting tenant by ID: %v, tenantID: %d, userID: %s", err, targetTenantID, user.ID)
					c.JSON(http.StatusUnauthorized, gin.H{
						"error": "Unauthorized: invalid tenant",
					})
					c.Abort()
					return
				}

				// 解析当前租户内的角色 (issue #1303)
				role, ok := resolveTenantRole(c.Request.Context(), memberService, user, targetTenantID, crossTenantSwitch, cfg)
				if !ok {
					// 强制 RBAC 时，缺少 active membership 即拒绝；fail-open 路径已在
					// resolveTenantRole 内部处理。
					logger.Warnf(c.Request.Context(),
						"User %s has no active membership in tenant %d", user.ID, targetTenantID)
					c.JSON(http.StatusForbidden, gin.H{
						"error": "Forbidden: not a member of the target tenant",
					})
					c.Abort()
					return
				}

				// 存储用户和租户信息到上下文
				logger.Infof(c.Request.Context(),
					"[auth] resolved role=%s for user=%s in tenant=%d (jwt_tenant=%d, header=%q, cross_switch=%v)",
					role, user.ID, targetTenantID, jwtTenantID, tenantHeader, crossTenantSwitch)
				c.Set(types.TenantIDContextKey.String(), targetTenantID)
				c.Set(types.TenantInfoContextKey.String(), tenant)
				c.Set(types.UserContextKey.String(), user)
				c.Set(types.UserIDContextKey.String(), user.ID)
				c.Set(types.TenantRoleContextKey.String(), role)
				ctx := c.Request.Context()
				ctx = context.WithValue(ctx, types.TenantIDContextKey, targetTenantID)
				ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
				ctx = context.WithValue(ctx, types.UserContextKey, user)
				ctx = context.WithValue(ctx, types.UserIDContextKey, user.ID)
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
				c.Request = c.Request.WithContext(ctx)
				c.Next()
				return
			}
		}

		// 尝试X-API-Key认证（兼容模式）
		apiKey := c.GetHeader("X-API-Key")
		if apiKey != "" {
			// Get tenant information
			tenantID, err := tenantService.ExtractTenantIDFromAPIKey(apiKey)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Unauthorized: invalid API key format",
				})
				c.Abort()
				return
			}

			// Verify API key validity (matches the one in database)
			t, err := tenantService.GetTenantByID(c.Request.Context(), tenantID)
			if err != nil {
				log.Printf("Error getting tenant by ID: %v, tenantID: %d", err, tenantID)
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Unauthorized: invalid API key",
				})
				c.Abort()
				return
			}

			if t == nil || subtle.ConstantTimeCompare([]byte(t.APIKey), []byte(apiKey)) != 1 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Unauthorized: invalid API key",
				})
				c.Abort()
				return
			}

			// 存储租户和用户信息到上下文
			c.Set(types.TenantIDContextKey.String(), tenantID)
			c.Set(types.TenantInfoContextKey.String(), t)

			ctx := context.WithValue(
				context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID),
				types.TenantInfoContextKey, t,
			)

			// 通过 TenantID 关联查询用户；找不到时构造系统虚拟用户，
			// 确保所有依赖 UserContextKey 的下游 handler 正常工作。
			user, err := userService.GetUserByTenantID(c.Request.Context(), tenantID)
			if err != nil || user == nil {
				// Synthetic user. The "system-<tenantID>" shape is recognised
				// by types.IsSyntheticUserID, which RBAC service-layer code
				// uses to skip recording these IDs as a resource creator.
				// Do NOT change the prefix or numeric suffix without
				// updating that helper, otherwise KB/Agent CreatorID will
				// silently start pointing at the synthetic user again.
				user = &types.User{
					ID:       fmt.Sprintf("system-%d", tenantID),
					Username: fmt.Sprintf("system-%d", tenantID),
					Email:    fmt.Sprintf("system-%d@api-key.local", tenantID),
					TenantID: tenantID,
					IsActive: true,
				}
				log.Printf("No user found for tenant %d via API key, using synthetic system user %s", tenantID, user.ID)
			}
			// API-Key 走的是程序化全租户访问，固定授予 Admin 角色：可以做几乎所有事情，
			// 但保留 Owner-only 操作（删除租户、修改租户级配置）的边界。
			c.Set(types.UserContextKey.String(), user)
			c.Set(types.UserIDContextKey.String(), user.ID)
			c.Set(types.TenantRoleContextKey.String(), types.TenantRoleAdmin)
			ctx = context.WithValue(ctx, types.UserContextKey, user)
			ctx = context.WithValue(ctx, types.UserIDContextKey, user.ID)
			ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)

			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return
		}

		// 没有提供任何认证信息
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: missing authentication"})
		c.Abort()
	}
}

// resolveTenantRole determines the caller's TenantRole inside targetTenantID.
//
// Order of resolution:
//  1. Active TenantMember row → return that role.
//  2. Cross-tenant superuser switch (X-Tenant-ID with CanAccessAllTenants=true)
//     → grant Admin in the target tenant. Org admins are intentionally not
//     promoted to Owner; tenant deletion / API-key rotation should always
//     stay with a real Owner inside the target tenant. Cross-tenant access
//     is also never allowed to trigger the orphan-tenant auto-promotion
//     below — a superuser only visits, never claims ownership.
//  3. No membership but the tenant currently has zero active members AND
//     the caller is authenticating into their own home tenant (i.e.
//     targetTenantID == user.TenantID and this is not a cross-tenant
//     switch). This is the API-key-only orphan-tenant self-heal path:
//     the registrant becomes Owner of the tenant their own user record
//     points to. Any other path (cross-tenant switch, JWT minted for a
//     foreign tenant, etc.) is intentionally excluded to avoid silent
//     ownership grabs.
//  4. Otherwise → return ok=false. Caller decides:
//     - When EnableRBAC=true (or cfg unavailable): treat as 403.
//     - When EnableRBAC=false: fail open with Admin so existing deployments
//     don't break in the rollout window where memberships might lag user
//     records.
//
// The boolean second return value reports whether enforcement should reject
// the request. It is true whenever a usable role was found OR fail-open
// applies; false only when we want callers to abort with 403.
func resolveTenantRole(
	ctx context.Context,
	memberService interfaces.TenantMemberService,
	user *types.User,
	targetTenantID uint64,
	crossTenantSwitch bool,
	cfg *config.Config,
) (types.TenantRole, bool) {
	// 1. 正常成员关系
	member, err := memberService.GetMembership(ctx, user.ID, targetTenantID)
	if err == nil && member != nil && member.Status == types.TenantMemberStatusActive {
		logger.Infof(ctx,
			"[auth] resolveTenantRole step1 hit: user=%s tenant=%d row_role=%s row_status=%s",
			user.ID, targetTenantID, member.Role, member.Status)
		return member.Role, true
	}
	if err != nil {
		logger.Warnf(ctx, "tenant_members lookup failed user=%s tenant=%d: %v",
			user.ID, targetTenantID, err)
		// Fall through; treat lookup errors the same as "no membership
		// found" so a transient DB hiccup doesn't lock everyone out.
	} else {
		var statusInfo string
		if member == nil {
			statusInfo = "no_row"
		} else {
			statusInfo = "row_exists status=" + string(member.Status) + " role=" + string(member.Role)
		}
		logger.Warnf(ctx,
			"[auth] resolveTenantRole step1 miss: user=%s tenant=%d (%s)",
			user.ID, targetTenantID, statusInfo)
	}

	// 2. 跨租户超管直通：CanAccessAllTenants 用户切到别的租户时不强制要求 membership。
	//    注意：这里只授予临时 Admin 角色，不写入 tenant_members，避免"看一眼别人租户"
	//    意外升级为持久化所有权。
	if crossTenantSwitch && user.CanAccessAllTenants {
		logger.Infof(ctx,
			"[auth] resolveTenantRole step2 (cross-tenant superuser) -> Admin: user=%s tenant=%d",
			user.ID, targetTenantID)
		return types.TenantRoleAdmin, true
	}

	// 3. 孤儿租户自愈：仅当用户登录的是自己的 home tenant、且该租户尚无任何活跃成员时
	//    允许自动晋升为 Owner。跨租户 switch / JWT 指向他人租户的场景一律不进入此分支，
	//    防止越权获得他人租户的 Owner 权限。
	isHomeTenant := !crossTenantSwitch && targetTenantID == user.TenantID
	if isHomeTenant {
		hasAny, anyErr := memberService.HasAnyMembers(ctx, targetTenantID)
		if anyErr == nil && !hasAny {
			if _, e := memberService.AddMember(
				ctx, user.ID, targetTenantID, types.TenantRoleOwner, nil,
			); e == nil {
				logger.Infof(ctx,
					"[audit] Auto-promoted user %s to Owner of orphan tenant %d (home_tenant=true)",
					user.ID, targetTenantID,
				)
				return types.TenantRoleOwner, true
			} else {
				logger.Warnf(ctx, "Failed to auto-promote user %s in tenant %d: %v",
					user.ID, targetTenantID, e)
			}
		}
	}

	// 4. 兜底：根据 EnableRBAC 决定 fail-closed 还是 fail-open
	if cfg != nil && cfg.Tenant != nil && cfg.Tenant.EnableRBAC {
		logger.Warnf(ctx,
			"[auth] resolveTenantRole step4 fail-closed (EnableRBAC=true): user=%s tenant=%d",
			user.ID, targetTenantID)
		return "", false
	}
	logger.Warnf(ctx,
		"[auth] resolveTenantRole step4 fail-open (EnableRBAC=false) -> Admin: user=%s tenant=%d",
		user.ID, targetTenantID)
	// fail-open 期间保持现有行为（每个登录用户在自己租户里都是"管理员"）。
	return types.TenantRoleAdmin, true
}

// GetTenantIDFromContext helper function to get tenant ID from context
func GetTenantIDFromContext(ctx context.Context) (uint64, error) {
	tenantID, ok := ctx.Value("tenantID").(uint64)
	if !ok {
		return 0, errors.New("tenant ID not found in context")
	}
	return tenantID, nil
}
