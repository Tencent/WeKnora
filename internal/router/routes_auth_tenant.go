package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// RegisterTenantRoutes 注册空间相关的路由
//
// Tenant-internal RBAC for /tenants/:id:
//   - GET   /:id          Viewer+ (read tenant settings)
//   - PUT   /:id          Owner+ (mutate tenant config)
//   - DELETE /:id         Owner+ (also normally a CanAccessAllTenants op)
//   - GET/POST/PUT/DELETE /:id/api-keys   Owner+ (scoped API key management)
//   - GET    /:id/members            Viewer+ (any member can see who else is in)
//   - POST   /:id/members            Owner+ (only Owner can add new members)
//   - PUT    /:id/members/:user_id   Owner+ (only Owner can change roles)
//   - DELETE /:id/members/:user_id   Owner+ (only Owner can remove members)
//   - POST   /:id/leave              Viewer+ (any member can quit on their own)
//
// All /tenants/:id endpoints share g.PathTenantMatch() at the group
// level: middleware/access.go enforces "URL :id == active tenant"
// (with the cross-tenant superuser carve-out) so an Owner-of-A cannot
// drive operations against tenant B by changing the URL. This used to
// be authorizeTenantAccess in tenant.go and resolveTenantIDFromPath in
// tenant_member.go; collapsing it into one route guard means the
// declaration itself documents the rule.
//
// Cross-tenant superuser endpoints (/tenants/all, /tenants/search) use
// g.CrossTenant(): RequireCrossTenantAccess in access.go combines the
// CanAccessAllTenants user attribute with the cluster-wide
// EnableCrossTenantAccess flag, replacing the 12-line if-block that
// previously opened ListAllTenants and SearchTenants.
//
// JWT behavior for POST /tenants and GET /tenants remains unchanged. Platform
// API keys may create tenants through system_tenants_manage; workspace keys
// remain default-denied on tenant-catalog operations.
func RegisterTenantRoutes(
	r *gin.RouterGroup,
	handler *handler.TenantHandler,
	memberHandler *handler.TenantMemberHandler,
	invitationHandler *handler.TenantInvitationHandler,
	auditLogHandler *handler.AuditLogHandler,
	g *rbacGuards,
) {
	// Cross-tenant superuser endpoints — promoted from handler if-blocks
	// to middleware.RequireCrossTenantAccess at the route layer.
	g.apiKeyRoute(r, http.MethodGet, "/tenants/all",
		apiKeyPlatform(types.APIKeyCapabilitySystemTenantsRead, types.APIKeyCapabilitySystemTenantsManage),
		g.CrossTenant(), handler.ListAllTenants)
	g.apiKeyRoute(r, http.MethodGet, "/tenants/search",
		apiKeyPlatform(types.APIKeyCapabilitySystemTenantsRead, types.APIKeyCapabilitySystemTenantsManage),
		g.CrossTenant(), handler.SearchTenants)

	// 空间路由组
	tenantRoutes := r.Group("/tenants")
	{
		// 创建空间对所有已登录用户开放：用户可以为自己再开一个工作区，
		// handler 内部会调 EnsureOwner 把调用者写成新空间的 Owner。
		// 跨空间超管走同一个端点，但能携带 storage_quota / status 等
		// 全字段（见 handler.CreateTenant 内部分支）。
		// 安全说明：这里不挂 g.CrossTenant()，因为 self-service 创建
		// 不需要跨空间特权；handler 也不读写 X-Tenant-ID 指向的现有
		// 空间，所以越过 PathTenantMatch 守卫不会扩大攻击面。
		// 创建空间不对 API key 开放（注册在原始 group，默认拒绝）。
		g.apiKeyRoute(tenantRoutes, http.MethodPost, "",
			apiKeyPlatform(types.APIKeyCapabilitySystemTenantsManage), handler.CreateTenant)
		g.apiKeyRoute(tenantRoutes, http.MethodGet, "", apiKeyManageTenantSettings(apiKeyFullAccess()), handler.ListTenants)

		// Generic KV configuration management (tenant-level). Tenant ID
		// is obtained from authentication context; the URL :key is a
		// config key, not a tenant ID, so these stay outside the
		// PathTenantMatch group. Tenant-level surface: full-access keys may
		// call it, and scoped keys need manage_tenant_settings.
		g.apiKeyRoute(tenantRoutes, http.MethodGet, "/kv/:key", apiKeyManageTenantSettings(apiKeyFullAccess()), g.Viewer(), handler.GetTenantKV)
		g.apiKeyRoute(tenantRoutes, http.MethodPut, "/kv/:key", apiKeyManageTenantSettings(apiKeyFullAccess()), g.Admin(), handler.UpdateTenantKV)

		// Per-tenant endpoints share PathTenantMatch at the group level.
		// Most /tenants/:id/* endpoints stay undeclared for API keys by
		// default — tenant lifecycle and key/principal management require
		// full tenant access or JWT ownership. Member/invitation management
		// opts in below through the manage_members capability.
		tenantByID := tenantRoutes.Group("/:id", g.PathTenantMatch())
		{
			g.apiKeyRoute(tenantByID, http.MethodGet, "",
				apiKeyPlatform(types.APIKeyCapabilitySystemTenantsRead, types.APIKeyCapabilitySystemTenantsManage),
				g.Viewer(), handler.GetTenant)
			g.apiKeyRoute(tenantByID, http.MethodPut, "",
				apiKeyPlatform(types.APIKeyCapabilitySystemTenantsManage), g.Owner(), handler.UpdateTenant)
			g.apiKeyRoute(tenantByID, http.MethodDelete, "",
				apiKeyPlatform(types.APIKeyCapabilitySystemTenantsManage), g.Owner(), handler.DeleteTenant)
			tenantByID.GET("/api-keys", g.Owner(), handler.ListAPIKeys)
			tenantByID.POST("/api-keys", g.Owner(), handler.CreateAPIKey)
			tenantByID.PUT("/api-keys/:key_id/knowledge-bases", g.Owner(), handler.UpdateAPIKeyKnowledgeBases)
			tenantByID.DELETE("/api-keys/:key_id", g.Owner(), handler.DeleteAPIKey)
			tenantByID.GET("/api-principal-config", g.Owner(), handler.GetAPIPrincipalConfig)
			tenantByID.PUT("/api-principal-config", g.Owner(), handler.UpdateAPIPrincipalConfig)
			tenantByID.POST("/api-principal-test-token", g.Owner(), handler.CreateAPIPrincipalTestToken)

			// Tenant member management (PR 3 of #1303). Listing is
			// Viewer+ so any active member can see the roster; mutation
			// is Owner+ because membership changes are the highest-impact
			// tenant op. /:id/leave is Viewer+ — any member can quit on
			// their own; the service still rejects when it would leave
			// the tenant without an Owner.
			if memberHandler != nil {
				g.apiKeyRoute(tenantByID, http.MethodGet, "/members", apiKeyManageMembers(apiKeyFullAccess()), g.Viewer(), memberHandler.ListMembers)
				g.apiKeyRoute(tenantByID, http.MethodPost, "/members", apiKeyManageMembers(apiKeyFullAccess()), g.Owner(), memberHandler.AddMember)
				g.apiKeyRoute(tenantByID, http.MethodPut, "/members/:user_id", apiKeyManageMembers(apiKeyFullAccess()), g.Owner(), memberHandler.UpdateMemberRole)
				g.apiKeyRoute(tenantByID, http.MethodDelete, "/members/:user_id", apiKeyManageMembers(apiKeyFullAccess()), g.Owner(), memberHandler.RemoveMember)
				tenantByID.POST("/leave", g.Viewer(), memberHandler.LeaveTenant)
			}

			// Tenant invitation flow. The UI-driven "Invite Member"
			// button hits POST /invitations rather than POST /members,
			// so the invitee gets to confirm via /me/invitations
			// before any tenant_members row is written. List is
			// Viewer+ so any member can see pending invites in the
			// management view; create/revoke are Owner+ to match the
			// existing /members mutation gates. nil-skip pattern
			// mirrors memberHandler above for environments built
			// without the invitation dependency wired.
			if invitationHandler != nil {
				g.apiKeyRoute(tenantByID, http.MethodGet, "/invitations", apiKeyManageMembers(apiKeyFullAccess()), g.Viewer(), invitationHandler.ListTenantInvitations)
				g.apiKeyRoute(tenantByID, http.MethodPost, "/invitations", apiKeyManageMembers(apiKeyFullAccess()), g.Owner(), invitationHandler.CreateInvitation)
				g.apiKeyRoute(tenantByID, http.MethodDelete, "/invitations/:inv_id", apiKeyManageMembers(apiKeyFullAccess()), g.Owner(), invitationHandler.RevokeInvitation)
				// Share-link create lives under /invite-links so the URL
				// reads as "create a link" rather than another flavour
				// of /invitations; the underlying row still lives in the
				// tenant_invitations table and shows up in the GET above.
				g.apiKeyRoute(tenantByID, http.MethodPost, "/invite-links", apiKeyManageMembers(apiKeyFullAccess()), g.Owner(), invitationHandler.CreateInviteLink)
			}

			// Audit log feed (PR 6 of #1303). Admin+ so denied-action
			// histories don't surface to ordinary members; the
			// PathTenantMatch group already prevents cross-tenant
			// reads. nil-skip mirrors the memberHandler pattern above
			// for environments wired without the audit dependency.
			if auditLogHandler != nil {
				tenantByID.GET("/audit-log", g.Admin(), auditLogHandler.ListTenantAuditLog)
			}
		}
	}
}

// RegisterMyInvitationRoutes wires the per-user invitation inbox under
// /me/invitations. The v1 group already applies middleware.Auth so we
// don't need a role gate here — the service enforces "only the invitee
// can accept/decline". The list endpoint mounts under /me to make the
// "show me MY invitations" semantics obvious in URLs and logs (vs the
// tenant-scoped /tenants/:id/invitations which lists ALL invitations
// for the tenant). pending-count is a separate, ultra-light endpoint
// the avatar-row badge polls; splitting it off so polling doesn't
// transfer the full list every cycle.
//
// invitationHandler may be nil in environments built without the
// invitation dependency wired; that's a no-op registration which is
// preferable to a startup crash.
func RegisterMyInvitationRoutes(r *gin.RouterGroup, invitationHandler *handler.TenantInvitationHandler) {
	if invitationHandler == nil {
		return
	}
	me := r.Group("/me")
	{
		me.GET("/invitations", invitationHandler.ListMyInvitations)
		me.GET("/invitations/pending-count", invitationHandler.CountMyPendingInvitations)
		me.POST("/invitations/:inv_id/accept", invitationHandler.AcceptMyInvitation)
		me.POST("/invitations/:inv_id/decline", invitationHandler.DeclineMyInvitation)
	}
}

// RegisterAuthRoutes registers authentication routes
func RegisterAuthRoutes(r *gin.RouterGroup, handler *handler.AuthHandler, g *rbacGuards) {
	r.POST("/auth/register", handler.Register)
	// Share-link surfaces are unauthenticated and accept a plaintext
	// token from the caller; rate-limit by IP to bound brute-force /
	// enumeration / abuse traffic. Limiter is shared across both
	// endpoints (see middleware/auth_public_ratelimit.go) so total
	// budget per IP is intuitive.
	publicAuthRL := middleware.PublicAuthRateLimit()
	r.POST("/auth/register-by-invite", publicAuthRL, handler.RegisterByInvite)
	r.POST("/auth/invitations/lookup", publicAuthRL, handler.LookupInvitationByToken)
	r.POST("/auth/login", handler.Login)
	r.POST("/auth/auto-setup", handler.AutoSetup)
	r.GET("/auth/config", handler.GetAuthConfig)
	r.POST("/auth/switch-tenant", handler.SwitchTenant)
	r.GET("/auth/oidc/config", handler.GetOIDCConfig)
	r.GET("/auth/oidc/url", handler.GetOIDCAuthorizationURL)
	r.GET("/auth/oidc/callback", handler.OIDCRedirectCallback)
	r.POST("/auth/refresh", handler.RefreshToken)
	r.GET("/auth/validate", handler.ValidateToken)
	r.POST("/auth/logout", handler.Logout)
	// auth/me returns only the caller's own identity/profile, so it is safe
	// for any valid API key. Chat clients / MCP call it to discover "who am I";
	// leaving it default-deny was why scoped keys got a 403 here.
	g.apiKeyRoute(r, http.MethodGet, "/auth/me", apiKeyAny(), handler.GetCurrentUser)
	r.PUT("/auth/me/preferences", handler.UpdateMyPreferences)
	r.POST("/auth/change-password", handler.ChangePassword)
}

// RegisterSystemRoutes registers system information routes
//
// Reads (GetSystemInfo / ListParserEngines / GetStorageEngineStatus)
// are gated to Viewer+ — any tenant member can see "is the parser
// reachable". The /*-check / /reconnect endpoints actively probe
// remote services with tenant credentials and could trigger network
// fanout, so they're Admin+.
func RegisterSystemRoutes(r *gin.RouterGroup, handler *handler.SystemHandler, g *rbacGuards) {
	systemRoutes := g.apiKeyGroup(r.Group("/system"), apiKeyManageVectorStores(apiKeyFullAccess()))
	{
		systemRoutes.GET("/info", g.Viewer(), handler.GetSystemInfo)
		systemRoutes.GET("/parser-engines", g.Viewer(), handler.ListParserEngines)
		systemRoutes.POST("/parser-engines/check", g.Admin(), handler.CheckParserEngines)
		systemRoutes.POST("/docreader/reconnect", g.Admin(), handler.ReconnectDocReader)
		systemRoutes.GET("/storage-engine-status", g.Viewer(), handler.GetStorageEngineStatus)
		systemRoutes.POST("/storage-engine-check", g.Admin(), handler.CheckStorageEngine)
	}
}

// RegisterSystemAdminRoutes registers system administration routes.
//
// All endpoints under this group are gated to SystemAdmin users (i.e.
// User.IsSystemAdmin == true). These are platform-wide operations
// independent of per-tenant Owner/Admin/Contributor/Viewer roles —
// they let org-level superusers grant/revoke system-admin status and,
// in later milestones, will host global settings, built-in models, and
// cross-tenant observability.
//
// Mounted under /api/v1/system/admin/* so the URL scheme stays aligned
// with the existing /api/v1/system/info family. Front-end clients live
// in frontend/src/api/system/index.ts.
//
// auditLogHandler may be nil in environments wired without the audit
// dependency; the /audit-log subroute is then omitted. This mirrors
// the optional wiring in RegisterTenantRoutes.
func RegisterSystemAdminRoutes(
	r *gin.RouterGroup,
	handler *handler.SystemHandler,
	auditLogHandler *handler.AuditLogHandler,
	g *rbacGuards,
) {
	// Apply SystemAdmin() at the group level — every route below inherits
	// the guard, so adding new endpoints can't accidentally drop the gate.
	adminRoutes := r.Group("/system/admin", g.SystemAdmin())
	{
		// P0: SystemAdmin role management
		adminRoutes.POST("/promote", handler.PromoteUserToSystemAdmin)
		adminRoutes.POST("/revoke", handler.RevokeSystemAdmin)
		adminRoutes.GET("/list", handler.ListSystemAdmins)
		adminRoutes.POST("/users/reset-password", handler.ResetUserPassword)
		adminRoutes.GET("/api-keys", handler.ListPlatformAPIKeys)
		adminRoutes.POST("/api-keys", handler.CreatePlatformAPIKey)
		adminRoutes.DELETE("/api-keys/:key_id", handler.DeletePlatformAPIKey)

		// P1: platform-wide system settings (DB-backed runtime tunables).
		// Reads return raw model rows / arrays (no `gin.H{"data":...}`
		// wrapping), matching the project's axios interceptor convention
		// — see frontend/src/utils/request.ts:97.
		g.apiKeyRoute(adminRoutes, http.MethodGet, "/settings",
			apiKeyPlatform(types.APIKeyCapabilitySystemSettingsRead, types.APIKeyCapabilitySystemSettingsManage),
			handler.ListSystemSettings)
		g.apiKeyRoute(adminRoutes, http.MethodGet, "/settings/:key",
			apiKeyPlatform(types.APIKeyCapabilitySystemSettingsRead, types.APIKeyCapabilitySystemSettingsManage),
			handler.GetSystemSetting)
		g.apiKeyRoute(adminRoutes, http.MethodPut, "/settings/:key",
			apiKeyPlatform(types.APIKeyCapabilitySystemSettingsManage), handler.UpdateSystemSetting)
		g.apiKeyRoute(adminRoutes, http.MethodDelete, "/settings/:key",
			apiKeyPlatform(types.APIKeyCapabilitySystemSettingsManage), handler.ResetSystemSetting)

		// Runtime operations: live asynq queue depths, safe task projections,
		// and state-checked task actions for the SystemAdmin dashboard. Lite
		// mode returns available=false.
		g.apiKeyRoute(adminRoutes, http.MethodGet, "/runtime/queues",
			apiKeyPlatform(types.APIKeyCapabilitySystemRuntimeRead, types.APIKeyCapabilitySystemRuntimeManage),
			handler.GetRuntimeQueues)
		g.apiKeyRoute(adminRoutes, http.MethodGet, "/runtime/queues/:queue/tasks",
			apiKeyPlatform(types.APIKeyCapabilitySystemRuntimeRead, types.APIKeyCapabilitySystemRuntimeManage),
			handler.ListRuntimeTasks)
		g.apiKeyRoute(adminRoutes, http.MethodPost, "/runtime/queues/:queue/tasks/:task_id/actions/:action",
			apiKeyPlatform(types.APIKeyCapabilitySystemRuntimeManage), handler.MutateRuntimeTask)
		g.apiKeyRoute(adminRoutes, http.MethodDelete, "/runtime/queues/:queue/archived",
			apiKeyPlatform(types.APIKeyCapabilitySystemRuntimeManage), handler.PurgeArchivedRuntimeTasks)

		// Bulk action — write the current default-quota setting onto
		// every existing tenant. Lives under /tenants instead of
		// /settings because it changes tenants, not the setting row.
		g.apiKeyRoute(adminRoutes, http.MethodPost, "/tenants/apply-default-storage-quota",
			apiKeyPlatform(types.APIKeyCapabilitySystemTenantsManage),
			handler.ApplyDefaultStorageQuotaToAllTenants)

		// Platform-wide audit feed (tenant_id=0 rows). Covers
		// system.setting_changed / system.admin_promoted /
		// system.admin_revoked etc. — events written by the routes
		// above. Without this endpoint those audit rows would have
		// no UI surface (per-tenant ListTenantAuditLog filters them
		// out by tenant_id). Optional: skip when audit deps are
		// absent, matching RegisterTenantRoutes' /audit-log handling.
		if auditLogHandler != nil {
			g.apiKeyRoute(adminRoutes, http.MethodGet, "/audit-log",
				apiKeyPlatform(types.APIKeyCapabilitySystemAuditRead), auditLogHandler.ListSystemAuditLog)
		}
	}
}
