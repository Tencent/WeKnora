package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// RegisterCustomAgentRoutes registers custom agent routes.
//
// Mutating routes use OwnedAgentOrAdmin: the original creator can edit
// their agent, otherwise Admin+ is required. Built-in agents
// (IsBuiltin=true) have an empty creator and are always Admin+. Reads
// are Viewer+, copy is Contributor+ (the copy is owned by the caller).
func RegisterCustomAgentRoutes(r *gin.RouterGroup, agentHandler *handler.CustomAgentHandler, g *rbacGuards) {
	agents := g.apiKeyGroup(r.Group("/agents"), apiKeyFullAccess())
	// agentsRead are the agent read endpoints. They stay full-access only for
	// plain scoped keys (agent config can carry sensitive model/MCP bindings),
	// but read_agents, chat, or manage_agents may read them.
	agentsRead := agents.With(apiKeyReadAgents(apiKeyManageAgents(apiKeyChat(apiKeyFullAccess()))))
	// agentsWrite are the agent authoring endpoints. Owner by default, but a
	// key granted manage_agents may author agents without full Owner.
	agentsWrite := agents.With(apiKeyManageAgents(apiKeyFullAccess()))
	{
		// Get placeholder definitions (must be before /:id to avoid conflict) — Viewer+
		agentsRead.GET("/placeholders", g.Viewer(), agentHandler.GetPlaceholders)
		// List smart-reasoning agent type presets (rag-qa / wiki-qa / hybrid / custom) — Viewer+
		agentsRead.GET("/type-presets", g.Viewer(), agentHandler.GetAgentTypePresets)
		// Create custom agent — Contributor+
		agentsWrite.POST("", g.Contributor(), agentHandler.CreateAgent)
		// List all agents (including built-in) — Viewer+
		agentsRead.GET("", g.Viewer(), agentHandler.ListAgents)
		// Get agent by ID — Viewer+
		agentsRead.GET("/:id", g.Viewer(), agentHandler.GetAgent)
		// Update agent — creator OR Admin+
		agentsWrite.PUT("/:id", g.OwnedAgentOrAdmin(), agentHandler.UpdateAgent)
		// Delete agent — creator OR Admin+
		agentsWrite.DELETE("/:id", g.OwnedAgentOrAdmin(), agentHandler.DeleteAgent)
		// Copy agent — Contributor+ (copy is owned by the caller)
		agentsWrite.POST("/:id/copy", g.Contributor(), agentHandler.CopyAgent)
	}
	// Registered outside the group to avoid Gin route conflict with /agents/:id/shares in organization routes
	g.apiKeyRoute(r, http.MethodGet, "/agents/:id/suggested-questions",
		apiKeyReadAgents(apiKeyManageAgents(apiKeyChat(apiKeyFullAccess()))), g.Viewer(), agentHandler.GetSuggestedQuestions)
}

// RegisterUserFavoriteRoutes wires the per-user starred-resource endpoints.
//
// Authorization: the handler always derives (user_id, tenant_id) from the
// auth context — there is no admin-style "see another user's favorites"
// path — so a Viewer floor is the right gate. The endpoints intentionally
// don't follow the OwnedXOrAdmin pattern: favorites aren't owned by the
// resource's creator, they're owned by the user *doing* the starring.
func RegisterUserFavoriteRoutes(r *gin.RouterGroup, h *handler.UserResourceFavoriteHandler, g *rbacGuards) {
	// Favorites are per-user; not declared for API keys (default-deny).
	favs := r.Group("/user/favorites")
	{
		favs.GET("", g.Viewer(), h.ListFavorites)
		favs.POST("", g.Viewer(), h.AddFavorite)
		favs.DELETE("/:type/:id", g.Viewer(), h.RemoveFavorite)
	}
}

// RegisterSkillRoutes registers skill routes.
//
// PR 2 currently only exposes a read-only `ListSkills`; gated to
// Viewer+. Future skill upload / enable endpoints must use Admin+ since
// skills run sandboxed code on tenant resources.
func RegisterSkillRoutes(r *gin.RouterGroup, skillHandler *handler.SkillHandler, g *rbacGuards) {
	skills := r.Group("/skills")
	{
		// List all preloaded skills — Viewer+
		skills.GET("", g.Viewer(), skillHandler.ListSkills)
	}
}

// RegisterOrganizationRoutes registers organization and sharing routes
func RegisterOrganizationRoutes(r *gin.RouterGroup, orgHandler *handler.OrganizationHandler, g *rbacGuards) {
	// Organization routes
	orgs := g.apiKeyGroup(r.Group("/organizations"), apiKeyManageSpaces(apiKeyFullAccess()))
	{
		// Create organization (Admin+ in caller's tenant only)
		orgs.POST("", g.Admin(), orgHandler.CreateOrganization)
		// List my organizations — Viewer+ floor so revoked/non-member
		// accounts whose JWT still validates can't enumerate org membership.
		orgs.GET("", g.Viewer(), orgHandler.ListMyOrganizations)
		// Preview organization by invite code (without joining) — Viewer+
		orgs.GET("/preview/:code", g.Viewer(), orgHandler.PreviewByInviteCode)
		// Join organization by invite code (Admin+ in caller's tenant only)
		orgs.POST("/join", g.Admin(), orgHandler.JoinByInviteCode)
		// Submit join request (for organizations that require approval) (Admin+)
		orgs.POST("/join-request", g.Admin(), orgHandler.SubmitJoinRequest)
		// Search searchable (discoverable) organizations — Viewer+
		orgs.GET("/search", g.Viewer(), orgHandler.SearchOrganizations)
		// Join searchable organization by ID (no invite code) (Admin+)
		orgs.POST("/join-by-id", g.Admin(), orgHandler.JoinByOrganizationID)
		// Get organization by ID — Viewer+
		orgs.GET("/:id", g.Viewer(), orgHandler.GetOrganization)
		// Update organization — Admin+ in caller's tenant.
		// Service still gates on "caller's tenant is the org owner";
		// the route guard adds a defence-in-depth layer that stops a
		// tenant Viewer/Contributor from ever reaching the service.
		orgs.PUT("/:id", g.Admin(), orgHandler.UpdateOrganization)
		// Delete organization — Admin+ in caller's tenant. Same
		// rationale as PUT above; deletion is irreversible so the
		// route-layer floor is at least as strict.
		orgs.DELETE("/:id", g.Admin(), orgHandler.DeleteOrganization)
		// Leave organization (Admin+ in caller's tenant only)
		orgs.POST("/:id/leave", g.Admin(), orgHandler.LeaveOrganization)
		// Request role upgrade (Admin+ in caller's tenant only).
		// An upgrade approval changes the whole tenant's org role, so it
		// must not be initiated by a tenant Viewer/Contributor.
		orgs.POST("/:id/request-upgrade", g.Admin(), orgHandler.RequestRoleUpgrade)
		// Generate invite code — Admin+ in caller's tenant. Issuing an
		// invite code is an admin action; the service layer additionally
		// requires the caller's tenant to be admin in the org.
		orgs.POST("/:id/invite-code", g.Admin(), orgHandler.GenerateInviteCode)
		// Search tenants for invite (admin only). Plan 3 changed the
		// unit of membership to "tenant"; this endpoint returns
		// candidate tenants (with one representative user attached)
		// instead of one row per user.
		orgs.GET("/:id/search-tenants", g.Admin(), orgHandler.SearchTenantsForInvite)
		// Deprecated alias for /:id/search-tenants. Old frontends that
		// still hit search-users will receive the tenant-grouped shape;
		// the deprecation is documented in the handler.
		orgs.GET("/:id/search-users", g.Admin(), orgHandler.SearchUsersForInvite)
		// Invite member directly (admin only)
		orgs.POST("/:id/invite", g.Admin(), orgHandler.InviteMember)
		// List members — Viewer+
		orgs.GET("/:id/members", g.Viewer(), orgHandler.ListMembers)
		// Update member role (path parameter is the member tenant_id) —
		// Admin+ in caller's tenant. Changing another tenant's org role
		// is the symmetric counterpart of removing them; both must be
		// gated the same way.
		orgs.PUT("/:id/members/:tenant_id", g.Admin(), orgHandler.UpdateMemberRole)
		// Remove member (path parameter is the member tenant_id).
		// Both self-removal (caller's own tenant) and admin-removal-of-other
		// take a whole tenant out of the org, so the route must be Admin+
		// in the caller's tenant — symmetric with POST /:id/leave above.
		orgs.DELETE("/:id/members/:tenant_id", g.Admin(), orgHandler.RemoveMember)
		// List join requests (admin only) — caller's tenant must be at
		// least Admin to even see the queue (a tenant Viewer has no
		// authority to act on it).
		orgs.GET("/:id/join-requests", g.Admin(), orgHandler.ListJoinRequests)
		// Review join request (admin only)
		orgs.PUT("/:id/join-requests/:request_id/review", g.Admin(), orgHandler.ReviewJoinRequest)
		// List knowledge bases shared to this organization — Viewer+
		orgs.GET("/:id/shares", g.Viewer(), orgHandler.ListOrgShares)
		// List agents shared to this organization — Viewer+
		orgs.GET("/:id/agent-shares", g.Viewer(), orgHandler.ListOrgAgentShares)
		// List all knowledge bases in this organization (including mine) for list-page space view — Viewer+
		orgs.GET("/:id/shared-knowledge-bases", g.Viewer(), orgHandler.ListOrganizationSharedKnowledgeBases)
		// List all agents in this organization (including mine) for list-page space view — Viewer+
		orgs.GET("/:id/shared-agents", g.Viewer(), orgHandler.ListOrganizationSharedAgents)
	}

	// Knowledge base sharing routes (add to existing kb routes).
	// 分享 KB 到组织 = 让组织里所有人能读这个 KB；这跟"修改 KB 元信息"
	// 同等敏感，所以挂同款 OwnedKBOrAdmin 矩阵。Viewer 在自己空间里
	// 也不能私自把 KB 暴露出去。
	// 分享管理不通过 capability 授予（manage_spaces 也不含）；仅 full-access
	// key（空间级全权）可管理分享，scoped key 保持 default-deny。
	kbShares := g.apiKeyGroup(r.Group("/knowledge-bases/:id/shares"), apiKeyFullAccess())
	{
		// Share knowledge base
		kbShares.POST("", g.OwnedKBOrAdmin(), orgHandler.ShareKnowledgeBase)
		// List shares — Viewer+ 即可，纯读取
		kbShares.GET("", g.Viewer(), orgHandler.ListKBShares)
		// Update share permission
		kbShares.PUT("/:share_id", g.OwnedKBOrAdmin(), orgHandler.UpdateSharePermission)
		// Remove share
		kbShares.DELETE("/:share_id", g.OwnedKBOrAdmin(), orgHandler.RemoveShare)
	}

	// Agent sharing routes — same rationale as KB shares: 分享/取消分享
	// 跟修改 agent 同等敏感，挂 OwnedAgentOrAdmin。
	//
	// GET 走 OwnedAgentOrAdmin 作为 JWT 侧的 owner 校验；service 层
	// ListSharesByAgent 现在也强制 tenant 归属（与 ListSharesByKnowledgeBase
	// 对齐），这样 full-access API key（会短路路由 guard）也无法跨空间
	// 枚举他人 agent 的分享。
	// 同 KB 分享：分享管理不通过 capability 授予；仅 full-access key
	// （空间级全权）可管理 agent 分享，scoped key 保持 default-deny。
	agentShares := g.apiKeyGroup(r.Group("/agents/:id/shares"), apiKeyFullAccess())
	{
		agentShares.POST("", g.OwnedAgentOrAdmin(), orgHandler.ShareAgent)
		agentShares.GET("", g.OwnedAgentOrAdmin(), orgHandler.ListAgentShares)
		agentShares.DELETE("/:share_id", g.OwnedAgentOrAdmin(), orgHandler.RemoveAgentShare)
	}

	// Shared knowledge bases route — Viewer+
	g.apiKeyRoute(r, http.MethodGet, "/shared-knowledge-bases", apiKeyManageSpaces(apiKeyFullAccess()), g.Viewer(), orgHandler.ListSharedKnowledgeBases)
	// Shared agents route — Viewer+
	g.apiKeyRoute(r, http.MethodGet, "/shared-agents", apiKeyManageSpaces(apiKeyFullAccess()), g.Viewer(), orgHandler.ListSharedAgents)
	// "Disable by me" 是空间级偏好（写到 tenant_disabled_shared_agents），
	// 影响整个空间在会话下拉里看到的 agent 列表。任何 Viewer 改这个表就
	// 等于替整个空间做决定 — 必须 Admin+ 才允许调整。
	g.apiKeyRoute(r, http.MethodPost, "/shared-agents/disabled", apiKeyManageSpaces(apiKeyFullAccess()), g.Admin(), orgHandler.SetSharedAgentDisabledByMe)
}

// RegisterEmbedPublicRoutes registers anonymous embed endpoints secured by publish tokens.
func RegisterEmbedPublicRoutes(
	r *gin.Engine,
	embedHandler *handler.EmbedChannelHandler,
	embedService interfaces.EmbedChannelService,
	tenantService interfaces.TenantService,
	redisClient *redis.Client,
	fileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalogs ...interfaces.ResourceCatalog,
) {
	if embedHandler == nil || embedService == nil {
		return
	}
	embed := r.Group("/api/v1/embed/:channel_id", middleware.EmbedAuth(embedService, tenantService, redisClient))
	{
		embed.POST("/exchange", embedHandler.ExchangeEmbedSession)
		embed.GET("/config", embedHandler.GetEmbedConfig)
		embed.GET("/suggested-questions", embedHandler.GetEmbedSuggestedQuestions)
		embed.GET("/chunks/:chunk_id", embedHandler.GetEmbedChunk)
		embed.POST("/sessions", embedHandler.CreateEmbedSession)
		embed.POST("/knowledge-chat/:session_id", embedHandler.EmbedKnowledgeChat)
		embed.POST("/agent-chat/:session_id", embedHandler.EmbedAgentChat)
		embed.GET("/messages/:session_id/load", embedHandler.EmbedLoadMessages)
		embed.POST("/sessions/:session_id/stop", embedHandler.EmbedStopSession)
		embed.GET("/sessions/:session_id/messages/:message_id/suggestions", embedHandler.EmbedGetMessageSuggestions)
		embed.POST("/sessions/:session_id/messages/:message_id/suggestions", embedHandler.EmbedEnsureMessageSuggestions)
		embed.POST("/sessions/:session_id/suggestion-events", embedHandler.EmbedRecordSuggestionEvent)
		embed.POST("/sessions/:session_id/events", embedHandler.EmbedRelayWebhookEvent)
		embed.POST("/sessions/:session_id/mcp-oauth-resolutions/:pending_id", embedHandler.EmbedResolveMCPOAuth)
		embed.POST("/sessions/:session_id/mcp-oauth-resolutions/:pending_id/cancel", embedHandler.EmbedCancelMCPOAuth)
		embed.POST("/sessions/:session_id/mcp-services/:id/oauth/authorize-url", embedHandler.EmbedMCPOAuthAuthorizeURL)
		embed.GET("/sessions/:session_id/mcp-services/:id/oauth/status", embedHandler.EmbedMCPOAuthStatus)
		embed.POST("/sessions/:session_id/tool-approvals/:pending_id", embedHandler.EmbedResolveToolApproval)
		// Serve images embedded in bot replies (e.g. chart exports). EmbedAuth
		// injects the channel's tenant, and the handler enforces that the
		// requested path belongs to that tenant.
		embed.GET("/files", newFileServeHandler(fileService, storageResolver, resourceCatalogs...))
	}
}

// RegisterEmbedChannelRoutes registers authenticated embed channel management routes.
func RegisterEmbedChannelRoutes(r *gin.RouterGroup, embedHandler *handler.EmbedChannelHandler, g *rbacGuards) {
	if embedHandler == nil {
		return
	}
	agentEmbed := g.apiKeyGroup(r.Group("/agents/:id/embed-channels"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		agentEmbed.POST("", g.Admin(), embedHandler.CreateEmbedChannel)
		agentEmbed.GET("", g.Viewer(), embedHandler.ListEmbedChannels)
	}
	channels := g.apiKeyGroup(r.Group("/embed-channels"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		channels.GET("", g.Viewer(), embedHandler.ListAllEmbedChannels)
		channels.GET("/:channel_id", g.Viewer(), embedHandler.GetEmbedChannel)
		channels.PUT("/:channel_id", g.Admin(), embedHandler.UpdateEmbedChannel)
		channels.DELETE("/:channel_id", g.Admin(), embedHandler.DeleteEmbedChannel)
		channels.POST("/:channel_id/rotate-token", g.Admin(), embedHandler.RotateEmbedToken)
		channels.POST("/:channel_id/preview-session", g.Viewer(), embedHandler.IssuePreviewSession)
		channels.GET("/:channel_id/stats", g.Viewer(), embedHandler.GetEmbedChannelStats)
	}
}

// RegisterIMRoutes registers IM callback routes.
// These are registered BEFORE auth middleware since IM platforms use their own signature verification.
func RegisterIMRoutes(r *gin.Engine, imHandler *handler.IMHandler) {
	im := r.Group("/api/v1/im")
	{
		im.GET("/callback/:channel_id", imHandler.IMCallback)
		im.POST("/callback/:channel_id", imHandler.IMCallback)
	}
}

// RegisterIMChannelRoutes registers IM channel CRUD routes (requires authentication).
//
// IM channels carry external bot credentials (WeChat/Feishu/Slack/...);
// listing is Viewer+ but any mutation, toggle, or QR-code login flow
// (which can hijack a personal WeChat session) is Admin+.
func RegisterIMChannelRoutes(r *gin.RouterGroup, imHandler *handler.IMHandler, g *rbacGuards) {
	// Channel CRUD under agents
	agentChannels := g.apiKeyGroup(r.Group("/agents/:id/im-channels"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		agentChannels.POST("", g.Admin(), imHandler.CreateIMChannel)
		agentChannels.GET("", g.Viewer(), imHandler.ListIMChannels)
	}

	// Channel operations by channel ID
	channels := g.apiKeyGroup(r.Group("/im-channels"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		channels.GET("", g.Viewer(), imHandler.ListAllIMChannels)
		channels.PUT("/:id", g.Admin(), imHandler.UpdateIMChannel)
		channels.DELETE("/:id", g.Admin(), imHandler.DeleteIMChannel)
		channels.POST("/:id/toggle", g.Admin(), imHandler.ToggleIMChannel)
	}

	// WeChat QR code login (requires authentication) — Admin+: a successful
	// scan binds a personal WeChat account to the tenant.
	wechatGroup := g.apiKeyGroup(r.Group("/wechat"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		wechatGroup.POST("/qrcode", g.Admin(), imHandler.WeChatGetQRCode)
		wechatGroup.POST("/qrcode/status", g.Admin(), imHandler.WeChatPollQRCodeStatus)
	}
}

// embedChannelIDFromPath extracts the channel id from an /embed/:channelID path.
func embedChannelIDFromPath(path string) string {
	const prefix = "/embed/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// embedFrameAncestorsMiddleware sets a per-channel `frame-ancestors` CSP on the
// embed SPA page so it can only be framed by the channel's allowed origins.
// When the channel declares no origins (or "*"), no restriction is applied,
// matching the API allowlist semantics. Only GET/HEAD page loads are handled.
func embedFrameAncestorsMiddleware(svc interfaces.EmbedChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Next()
			return
		}
		channelID := embedChannelIDFromPath(c.Request.URL.Path)
		if channelID == "" {
			c.Next()
			return
		}
		ch, err := svc.LookupEnabledChannel(c.Request.Context(), channelID)
		if err != nil || ch == nil {
			c.Next()
			return
		}
		origins := ch.AllowedOriginsList()
		sources := make([]string, 0, len(origins))
		wildcard := false
		for _, o := range origins {
			o = strings.TrimSpace(o)
			if o == "" {
				continue
			}
			if o == "*" {
				wildcard = true
				break
			}
			sources = append(sources, o)
		}
		// No explicit origins or a wildcard => do not constrain framing here.
		if wildcard || len(sources) == 0 {
			c.Next()
			return
		}
		c.Header("Content-Security-Policy", "frame-ancestors "+strings.Join(sources, " "))
		c.Next()
	}
}
