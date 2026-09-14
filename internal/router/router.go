package router

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types/interfaces"

	_ "github.com/Tencent/WeKnora/docs" // swagger docs
)

// corsConfig 构建 CORS 配置。
// 优先读取 CORS_ALLOWED_ORIGINS 环境变量（逗号分隔的 origin 列表），
// 未设置时回退到 APP_EXTERNAL_URL，都未设置则拒绝跨域（禁止凭据）。
// gin-contrib/cors 在 AllowOrigins 为空且未设 AllowOriginFunc 时会 panic，
// 因此空列表改用 AllowOriginFunc 显式拒绝。
func corsConfig() cors.Config {
	origins := parseAllowedOrigins()
	cfg := cors.Config{
		AllowMethods: []string{
			"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS",
		},
		AllowHeaders: []string{
			"Origin", "Content-Type", "Accept", "Authorization",
			"X-API-Key", "X-Request-ID", "X-Tenant-ID", "X-Org-Unit-ID",
			"X-Embed-Session", "X-External-User-ID", "X-External-User-Token",
		},
		ExposeHeaders:    []string{"Content-Length", "Access-Control-Allow-Origin"},
		AllowCredentials: len(origins) > 0,
		MaxAge:           12 * time.Hour,
	}
	if len(origins) > 0 {
		cfg.AllowOrigins = origins
	} else {
		cfg.AllowOriginFunc = func(string) bool { return false }
	}
	return cfg
}

// parseAllowedOrigins 解析允许的 CORS origin 列表。
// 优先级：CORS_ALLOWED_ORIGINS > APP_EXTERNAL_URL > 空列表。
func parseAllowedOrigins() []string {
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		parts := strings.Split(v, ",")
		origins := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				origins = append(origins, p)
			}
		}
		return origins
	}
	if v := os.Getenv("APP_EXTERNAL_URL"); v != "" {
		return []string{v}
	}
	return []string{}
}

// RouterParams 路由参数
type RouterParams struct {
	dig.In

	Config                       *config.Config
	FileService                  interfaces.FileService
	UserService                  interfaces.UserService
	KBService                    interfaces.KnowledgeBaseService
	KnowledgeService             interfaces.KnowledgeService
	ChunkService                 interfaces.ChunkService
	SessionService               interfaces.SessionService
	MessageService               interfaces.MessageService
	ModelService                 interfaces.ModelService
	EvaluationService            interfaces.EvaluationService
	KBShareService               interfaces.KBShareService
	AgentShareService            interfaces.AgentShareService
	OrgUnitService               interfaces.OrgUnitService
	KBHandler                    *handler.KnowledgeBaseHandler
	KnowledgeHandler             *handler.KnowledgeHandler
	TenantHandler                *handler.TenantHandler
	TenantService                interfaces.TenantService
	TenantAPIKeyService          interfaces.TenantAPIKeyService
	TenantMemberService          interfaces.TenantMemberService
	TenantMemberHandler          *handler.TenantMemberHandler
	TenantInvitationHandler      *handler.TenantInvitationHandler
	AuditLogHandler              *handler.AuditLogHandler
	AuditLogService              interfaces.AuditLogService
	ChunkHandler                 *handler.ChunkHandler
	SessionHandler               *session.Handler
	MessageHandler               *handler.MessageHandler
	MessageSuggestionHandler     *handler.MessageSuggestionHandler
	ModelHandler                 *handler.ModelHandler
	ModelCredentialsHandler      *handler.ModelCredentialsHandler
	SandboxConfigHandler         *handler.SandboxConfigHandler
	SandboxSkillHandler          *handler.SandboxSkillHandler
	MeEnvVarHandler              *handler.MeEnvVarHandler
	EvaluationHandler            *handler.EvaluationHandler
	AuthHandler                  *handler.AuthHandler
	InitializationHandler        *handler.InitializationHandler
	SystemHandler                *handler.SystemHandler
	MCPServiceHandler            *handler.MCPServiceHandler
	MCPCredentialsHandler        *handler.MCPCredentialsHandler
	MCPOAuthHandler              *handler.MCPOAuthHandler
	WebSearchHandler             *handler.WebSearchHandler
	WebSearchProviderHandler     *handler.WebSearchProviderHandler
	WebSearchCredentialsHandler  *handler.WebSearchProviderCredentialsHandler
	VectorStoreHandler           *handler.VectorStoreHandler
	StorageBackendHandler        *handler.StorageBackendHandler
	StorageBackendResolver       interfaces.StorageBackendResolver
	ResourceCatalog              interfaces.ResourceCatalog
	FAQHandler                   *handler.FAQHandler
	TagHandler                   *handler.TagHandler
	CustomAgentHandler           *handler.CustomAgentHandler
	UserFavoriteHandler          *handler.UserResourceFavoriteHandler
	SkillHandler                 *handler.SkillHandler
	OrganizationHandler          *handler.OrganizationHandler
	OrgUnitHandler               *handler.OrgUnitHandler
	IMHandler                    *handler.IMHandler
	WeChatOAHandler               *handler.WeChatOAHandler
	EmbedChannelHandler          *handler.EmbedChannelHandler
	EmbedChannelService          interfaces.EmbedChannelService
	GuestLinkChannelHandler      *handler.GuestLinkChannelHandler
	AgentPublishAPIKeyHandler    *handler.AgentPublishAPIKeyHandler
	AgentPublishAPIKeyService    interfaces.AgentPublishAPIKeyService
	OpenAPIChatHandler           *handler.OpenAPIChatHandler
	RedisClient                  *redis.Client
	DataSourceHandler            *handler.DataSourceHandler
	DataSourceCredentialsHandler *handler.DataSourceCredentialsHandler
	TreeRAGCloudHandler          *handler.TreeRAGCloudHandler
	WikiPageHandler              *handler.WikiPageHandler
	MemoryHandler                *handler.MemoryHandler
}

// NewRouter 创建新的路由
func NewRouter(params RouterParams) *gin.Engine {
	r := gin.New()
	r.ContextWithFallback = true

	// Trusted proxies: gin defaults to trusting ALL proxies, which makes
	// c.ClientIP() honor a client-supplied X-Forwarded-For. Public, unauthed
	// embed endpoints rate-limit per (channel, ClientIP), so a spoofed XFF would
	// trivially bypass the limiter. Restrict to the fronting proxy network so
	// only the real client IP (appended by nginx) is returned. Configurable via
	// WEKNORA_TRUSTED_PROXIES (comma-separated CIDRs/IPs).
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
		logger.Errorf(context.Background(), "[Router] failed to set trusted proxies: %v", err)
	}

	// CORS 中间件应放在最前面
	// CORS_ALLOWED_ORIGINS 环境变量指定允许的 origin 列表（逗号分隔）
	// 未设置时使用 APP_EXTERNAL_URL，都未设置则禁止跨域凭据请求
	r.Use(cors.New(corsConfig()))

	// 安全响应头（在 CORS 之后、RequestID 之前）
	r.Use(middleware.SecurityHeaders())

	// 基础中间件（不需要认证）
	r.Use(middleware.RequestID())
	r.Use(middleware.Language())
	r.Use(middleware.Logger())
	r.Use(middleware.Recovery())
	r.Use(middleware.ErrorHandler())

	// 健康检查（不需要认证）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Swagger API 文档（仅在非生产环境下启用）
	// 通过 GIN_MODE 环境变量判断：release 模式下禁用 Swagger
	if gin.Mode() != gin.ReleaseMode {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler,
			ginSwagger.DefaultModelsExpandDepth(-1), // 默认折叠 Models
			ginSwagger.DocExpansion("list"),         // 展开模式: "list"(展开标签), "full"(全部展开), "none"(全部折叠)
			ginSwagger.DeepLinking(true),            // 启用深度链接
			ginSwagger.PersistAuthorization(true),   // 持久化认证信息
		))
	}

	// Embed page framing policy: emit a per-channel `frame-ancestors` CSP so the
	// embed SPA page (/embed/:channelId) can only be iframed by the channel's
	// allowed origins. This is the page-level counterpart to the API Origin
	// allowlist enforced in EmbedAuth. Registered before the static handler so
	// it runs for the embed HTML response.
	if params.EmbedChannelService != nil {
		r.Use(embedFrameAncestorsMiddleware(params.EmbedChannelService))
	}

	// 前端静态文件（仅 Lite 版本内嵌前端）
	if handler.Edition == "lite" {
		serveFrontendStatic(r)
	}

	// IM 回调路由（在认证中间件之前注册，使用各平台自身的签名验证）
	RegisterIMRoutes(r, params.IMHandler, params.WeChatOAHandler)

	// Web embed 公开路由（使用 publish token 鉴权，不走全局 Auth）
	RegisterEmbedPublicRoutes(
		r,
		params.EmbedChannelHandler,
		params.GuestLinkChannelHandler,
		params.EmbedChannelService,
		params.TenantService,
		params.RedisClient,
		params.FileService,
		params.StorageBackendResolver,
		params.ResourceCatalog,
	)

	// OpenAI-compatible chat completions (Bearer wkpub_… via PublishAPIKeyAuth).
	// Registered before global Auth; path is also listed in noAuthAPI.
	RegisterOpenAPIChatRoutes(
		r,
		params.OpenAPIChatHandler,
		params.AgentPublishAPIKeyService,
		params.TenantService,
	)

	// Short-lived capability URLs for IM and other clients that cannot attach
	// TreeRAG authentication headers.
	serveResourceGrants(r, params.ResourceCatalog, params.TenantService, params.FileService, params.StorageBackendResolver)

	// Sandbox terminal WebSocket (self-authenticated via a short-lived
	// query ticket — see RegisterSandboxTerminalRoutes; browsers cannot set
	// auth headers on the WS handshake, so this must precede the global Auth
	// middleware). The ticket is minted by an authenticated POST.
	RegisterSandboxTerminalRoutes(r, params.SessionHandler)
	r.GET("/api/v1/local-browser/extension", params.SessionHandler.BrowserSkillExtension)
	r.POST("/api/v1/local-browser/extension/authorize", params.SessionHandler.BrowserSkillAuthorize)
	r.POST("/api/v1/local-browser/internal", params.SessionHandler.BrowserSkillInternal)

	// 认证中间件
	r.Use(middleware.Auth(params.TenantService, params.UserService, params.TenantMemberService, params.TenantAPIKeyService, params.Config))
	r.Use(middleware.OrgUnitServiceProvider(params.OrgUnitService))
	r.Use(middleware.ResolveOrgUnit(params.OrgUnitService))

	// 文件服务：统一代理本地/MinIO/COS/TOS存储后端（需要认证）
	serveFilesWithResources(r, params.FileService, params.StorageBackendResolver, params.ResourceCatalog)

	// Presigned file access: no auth required, signature-verified.
	servePresignedFiles(r, params.TenantService, params.StorageBackendResolver)

	// Diagnostic preview of presigned URLs (Admin only, behind auth middleware).
	servePresignedPreview(r, params.Config, params.StorageBackendResolver)

	// Langfuse observability — only active when LANGFUSE_* env vars are set.
	// The middleware is registered unconditionally; when disabled it's a no-op.
	r.Use(langfuse.GinMiddleware())

	// Audit log injection — middleware/rbac.go's reject paths and the
	// admin-only /tenants/:id/audit-log endpoint pull the service out
	// of the gin context. Provider is a no-op when AuditLogService is
	// nil (e.g. lite mode without DB), so the rbac path degrades to
	// "log to stderr only" instead of crashing.
	r.Use(middleware.AuditServiceProvider(params.AuditLogService))

	// 需要认证的API路由
	v1 := r.Group("/api/v1")
	{
		// rbacGuards bundles the role-gating middleware factories so each
		// Register* function below can attach the right guard without
		// taking a *config.Config dependency directly. The guards honour
		// cfg.Tenant.EnableRBAC: when false, they log but pass through,
		// preserving today's behaviour during the rollout window.
		rbacGuards := newRBACGuards(
			params.Config,
			params.KBHandler,
			params.CustomAgentHandler,
			params.KnowledgeHandler,
			params.ChunkHandler,
			params.WikiPageHandler,
			params.KBService,
			params.KnowledgeService,
			params.ChunkService,
			params.KBShareService,
			params.AgentShareService,
		)

		// API-key gate: single authority for X-API-Key principals. Runs
		// first on every /api/v1 route (JWT sessions pass straight
		// through) and denies any route not explicitly declared via the
		// apiKeyGroup helpers. Must be attached BEFORE the Register* calls
		// so that sub-groups inherit it.
		v1.Use(rbacGuards.apiKeyAuthorizer.Middleware())

		RegisterAuthRoutes(v1, params.AuthHandler, rbacGuards, params.RedisClient)
		RegisterTenantRoutes(v1, params.TenantHandler, params.TenantMemberHandler, params.TenantInvitationHandler, params.AuditLogHandler, rbacGuards)
		RegisterMyInvitationRoutes(v1, params.TenantInvitationHandler)
		RegisterKnowledgeBaseRoutes(v1, params.KBHandler, rbacGuards)
		RegisterKnowledgeBaseActivityRoutes(v1, params.AuditLogHandler, rbacGuards)
		// KB-scoped image proxy: lets tenants render images embedded in
		// org-shared / agent-visible KB content, which the tenant-scoped
		// /files route cannot serve because it enforces same-tenant paths.
		serveKBScopedFiles(
			v1,
			rbacGuards,
			params.TenantService,
			params.FileService,
			params.StorageBackendResolver,
			params.ResourceCatalog,
		)
		// Message-scoped image proxy: shared-agent replies belong to the
		// caller's session but may reference resources stored in the agent's
		// source workspace. Authorization is derived from the persisted message,
		// never from a client-provided workspace ID. Replies produced by the
		// caller's own agent over an org-shared KB fall back to the KB share
		// relation instead (#3022).
		serveMessageScopedFiles(
			v1,
			rbacGuards,
			params.MessageService,
			params.AgentShareService,
			params.TenantService,
			params.FileService,
			params.StorageBackendResolver,
			params.ResourceCatalog,
			params.KBShareService,
			params.KBService,
			params.KnowledgeService,
		)
		RegisterKnowledgeTagRoutes(v1, params.TagHandler, rbacGuards)
		RegisterKnowledgeRoutes(v1, params.KnowledgeHandler, rbacGuards)
		RegisterFAQRoutes(v1, params.FAQHandler, rbacGuards)
		RegisterChunkRoutes(v1, params.ChunkHandler, rbacGuards)
		RegisterSessionRoutes(v1, params.SessionHandler, params.MessageSuggestionHandler, rbacGuards)
		RegisterChatRoutes(v1, params.SessionHandler, rbacGuards)
		RegisterMessageRoutes(v1, params.MessageHandler, rbacGuards)
		RegisterModelRoutes(v1, params.ModelHandler, params.ModelCredentialsHandler, rbacGuards)
		RegisterSandboxConfigRoutes(v1, params.SandboxConfigHandler, params.SandboxSkillHandler, rbacGuards)
		RegisterMyEnvVarRoutes(v1, params.MeEnvVarHandler)
		v1.GET("/me/browser", params.SessionHandler.BrowserSkillAccount)
		v1.GET("/me/browser/extension", params.SessionHandler.BrowserSkillDownload)
		v1.POST("/me/browser", params.SessionHandler.BrowserSkillAccount)
		RegisterEvaluationRoutes(v1, params.EvaluationHandler, rbacGuards)
		RegisterInitializationRoutes(v1, params.InitializationHandler, rbacGuards)
		params.SystemHandler.BindDeploymentCapabilities(deploymentCapabilitiesFromRouter(params))
		RegisterSystemRoutes(v1, params.SystemHandler, rbacGuards)
		RegisterSystemAdminRoutes(v1, params.SystemHandler, params.AuditLogHandler, rbacGuards)
		RegisterMCPServiceRoutes(v1, params.MCPServiceHandler, params.MCPCredentialsHandler, params.MCPOAuthHandler, rbacGuards)
		RegisterWebSearchRoutes(v1, params.WebSearchHandler, rbacGuards)
		RegisterWebSearchProviderRoutes(v1, params.WebSearchProviderHandler, params.WebSearchCredentialsHandler, rbacGuards)
		RegisterVectorStoreRoutes(v1, params.VectorStoreHandler, rbacGuards)
		RegisterStorageBackendRoutes(v1, params.StorageBackendHandler, rbacGuards)
		RegisterCustomAgentRoutes(v1, params.CustomAgentHandler, rbacGuards)
		RegisterUserFavoriteRoutes(v1, params.UserFavoriteHandler, rbacGuards)
		RegisterSkillRoutes(v1, params.SkillHandler, rbacGuards)
		RegisterOrganizationRoutes(v1, params.OrganizationHandler, rbacGuards)
		RegisterOrgUnitRoutes(v1, params.OrgUnitHandler, rbacGuards)
		RegisterIMChannelRoutes(v1, params.IMHandler, params.WeChatOAHandler, rbacGuards)
		RegisterEmbedChannelRoutes(v1, params.EmbedChannelHandler, rbacGuards)
		RegisterGuestLinkChannelRoutes(v1, params.GuestLinkChannelHandler, rbacGuards)
		RegisterAgentPublishAPIKeyRoutes(v1, params.AgentPublishAPIKeyHandler, rbacGuards)
		RegisterDataSourceRoutes(v1, params.DataSourceHandler, params.DataSourceCredentialsHandler, rbacGuards)
		RegisterTreeRAGCloudRoutes(v1, params.TreeRAGCloudHandler, rbacGuards)
		RegisterWikiPageRoutes(v1, params.WikiPageHandler, rbacGuards)
		RegisterMemoryRoutes(v1, params.MemoryHandler, rbacGuards)
		RegisterChunkerDebugRoutes(v1, rbacGuards)

		// Fail fast if any declared API-key policy points at a route
		// template that does not actually exist (typo / path drift). A
		// stale template would silently 403 every API key on that route,
		// so we panic at startup instead of shipping a dead policy.
		rbacGuards.assertAPIKeyPoliciesMatchRoutes(r)
	}

	return r
}

// trustedProxies returns the proxy CIDRs/IPs whose X-Forwarded-For headers
// gin should trust when resolving the client IP. Defaults to loopback and
// private ranges (covers the bundled nginx in a container network); override
// with WEKNORA_TRUSTED_PROXIES (comma-separated). An explicit empty value
// disables proxy trust entirely so ClientIP() returns the direct peer.
func trustedProxies() []string {
	raw, ok := os.LookupEnv("WEKNORA_TRUSTED_PROXIES")
	if !ok {
		return []string{
			"127.0.0.0/8",
			"::1/128",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"fc00::/7",
		}
	}
	proxies := make([]string, 0)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			proxies = append(proxies, p)
		}
	}
	return proxies
}

// RegisterAuthRoutes registers authentication routes
func RegisterAuthRoutes(r *gin.RouterGroup, handler *handler.AuthHandler, g *rbacGuards, redisClient *redis.Client) {
	authRL := middleware.AuthRateLimit(redisClient)
	loginRL := middleware.LoginRateLimit(redisClient)

	r.POST("/auth/register", authRL, handler.Register)
	// Share-link surfaces are unauthenticated and accept a plaintext
	// token from the caller; rate-limit by IP to bound brute-force /
	// enumeration / abuse traffic. Limiter is shared across both
	// endpoints (see middleware/auth_public_ratelimit.go) so total
	// budget per IP is intuitive.
	publicAuthRL := middleware.PublicAuthRateLimit()
	r.POST("/auth/register-by-invite", publicAuthRL, handler.RegisterByInvite)
	r.POST("/auth/invitations/lookup", publicAuthRL, handler.LookupInvitationByToken)
	r.POST("/auth/login", loginRL, handler.Login)
	r.POST("/auth/auto-setup", handler.AutoSetup)
	r.GET("/auth/config", handler.GetAuthConfig)
	r.POST("/auth/switch-tenant", handler.SwitchTenant)
	r.GET("/auth/oidc/config", handler.GetOIDCConfig)
	r.GET("/auth/oidc/url", handler.GetOIDCAuthorizationURL)
	r.GET("/auth/oidc/callback", handler.OIDCRedirectCallback)
	// /auth/oidc/start：直连 302 跳转到 OIDC 提供方，供前端无法走 JS 拉取 URL 的场景直接发起登录
	r.GET("/auth/oidc/start", handler.OIDCStart)
	r.POST("/auth/refresh", authRL, handler.RefreshToken)
	r.GET("/auth/validate", handler.ValidateToken)
	r.POST("/auth/logout", handler.Logout)
	// auth/me returns only the caller's own identity/profile, so it is safe
	// for any valid API key. Chat clients / MCP call it to discover "who am I";
	// leaving it default-deny was why scoped keys got a 403 here.
	g.apiKeyRoute(r, http.MethodGet, "/auth/me", apiKeyAny(), handler.GetCurrentUser)
	r.PUT("/auth/me/preferences", handler.UpdateMyPreferences)
	r.POST("/auth/change-password", handler.ChangePassword)
}


// RegisterOrgUnitRoutes registers tenant-scoped administrative hierarchy
// routes (省/市/县 OrgUnit). Orthogonal to /organizations (SharedSpace).
func RegisterOrgUnitRoutes(
	r *gin.RouterGroup,
	orgUnitHandler *handler.OrgUnitHandler,
	g *rbacGuards,
) {
	if orgUnitHandler == nil {
		return
	}
	units := r.Group("/org-units")
	{
		units.GET("", g.Viewer(), orgUnitHandler.List)
		units.GET("/me", g.Viewer(), orgUnitHandler.ListMyMemberships)
		units.GET("/visibility", g.Viewer(), orgUnitHandler.GetVisibility)
		units.GET("/inviteable", g.Viewer(), orgUnitHandler.ListInviteable)
		// Register before /:id so Gin does not treat "members" as an id.
		units.POST("/members/transfer", g.Admin(), orgUnitHandler.TransferMember)
		units.POST("", g.Admin(), orgUnitHandler.Create)
		units.GET("/:id", g.Viewer(), orgUnitHandler.Get)
		units.PUT("/:id", g.Admin(), orgUnitHandler.Update)
		units.DELETE("/:id", g.Admin(), orgUnitHandler.Delete)
		units.POST("/:id/move", g.Admin(), orgUnitHandler.Move)
		units.POST("/:id/primary", g.Viewer(), orgUnitHandler.SetPrimary)
		units.GET("/:id/members", g.Viewer(), orgUnitHandler.ListMembers)
		units.POST("/:id/members", g.Admin(), orgUnitHandler.AddMember)
		units.DELETE("/:id/members/:user_id", g.Admin(), orgUnitHandler.RemoveMember)
	}
}

// RegisterEmbedPublicRoutes registers anonymous embed endpoints secured by publish tokens.
func RegisterEmbedPublicRoutes(
	r *gin.Engine,
	embedHandler *handler.EmbedChannelHandler,
	guestLinkHandler *handler.GuestLinkChannelHandler,
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
	// Short web link bootstrap (no publish token in URL). Owned by the guest
	// link handler: guest links are the only surface reachable via /w/:slug.
	if guestLinkHandler != nil {
		r.POST("/api/v1/embed/web/:slug/bootstrap", guestLinkHandler.BootstrapWebLink)
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

// RegisterGuestLinkChannelRoutes registers authenticated guest link
// management routes. Guest links carry no publish-token/allowed-origins
// surface (see GuestLinkChannelHandler doc comment), so they get their own
// admin routes alongside — not merged into — the embed channel ones.
func RegisterGuestLinkChannelRoutes(r *gin.RouterGroup, guestLinkHandler *handler.GuestLinkChannelHandler, g *rbacGuards) {
	if guestLinkHandler == nil {
		return
	}
	agentGuestLinks := g.apiKeyGroup(r.Group("/agents/:id/guest-links"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		agentGuestLinks.GET("", g.Viewer(), guestLinkHandler.GetGuestLinkByAgent)
		agentGuestLinks.POST("", g.OwnerOrSystemAdmin(), guestLinkHandler.CreateGuestLink)
	}
	guestLinks := g.apiKeyGroup(r.Group("/guest-links"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		guestLinks.GET("/:id", g.Viewer(), guestLinkHandler.GetGuestLink)
		guestLinks.PUT("/:id", g.OwnerOrSystemAdmin(), guestLinkHandler.UpdateGuestLink)
		guestLinks.DELETE("/:id", g.OwnerOrSystemAdmin(), guestLinkHandler.DeleteGuestLink)
	}
}

// RegisterAgentPublishAPIKeyRoutes registers authenticated admin routes for
// agent-bound publish API keys. Guards match guest-link channel management:
// apiKeyManageChannels + Viewer for list, OwnerOrSystemAdmin for mutations.

func RegisterAgentPublishAPIKeyRoutes(
	r *gin.RouterGroup,
	publishHandler *handler.AgentPublishAPIKeyHandler,
	g *rbacGuards,
) {
	if publishHandler == nil {
		return
	}
	group := g.apiKeyGroup(
		r.Group("/agents/:id/publish-api-keys"),
		apiKeyManageChannels(apiKeyFullAccess()),
	)
	{
		group.GET("", g.Viewer(), publishHandler.List)
		group.POST("", g.OwnerOrSystemAdmin(), publishHandler.Create)
		group.DELETE("/:key_id", g.OwnerOrSystemAdmin(), publishHandler.Delete)
	}
}

// RegisterOpenAPIChatRoutes wires POST /api/v1/chat/completions with
// PublishAPIKeyAuth. Must stay outside JWT Auth (and match noAuthAPI).

func RegisterOpenAPIChatRoutes(
	r *gin.Engine,
	chatHandler *handler.OpenAPIChatHandler,
	publishKeySvc interfaces.AgentPublishAPIKeyService,
	tenantSvc interfaces.TenantService,
) {
	if chatHandler == nil || publishKeySvc == nil || tenantSvc == nil {
		return
	}
	openapi := r.Group("/api/v1")
	openapi.POST(
		"/chat/completions",
		middleware.PublishAPIKeyAuth(publishKeySvc, tenantSvc),
		chatHandler.ChatCompletions,
	)
}

// RegisterIMRoutes registers IM callback routes.
// These are registered BEFORE auth middleware since IM platforms use their own signature verification.

func RegisterIMRoutes(r *gin.Engine, imHandler *handler.IMHandler, oaHandler *handler.WeChatOAHandler) {
	im := r.Group("/api/v1/im")
	{
		im.GET("/callback/:channel_id", imHandler.IMCallback)
		im.POST("/callback/:channel_id", imHandler.IMCallback)
		if oaHandler != nil {
			im.POST("/wechat_oa/binding/complete", oaHandler.BindingComplete)
		}
	}
}

// RegisterIMChannelRoutes registers IM channel CRUD routes (requires authentication).
//
// IM channels carry external bot credentials (WeChat/Feishu/Slack/...);
// listing is Viewer+ but any mutation, toggle, or QR-code login flow
// (which can hijack a personal WeChat session) is Admin+.

func RegisterIMChannelRoutes(r *gin.RouterGroup, imHandler *handler.IMHandler, oaHandler *handler.WeChatOAHandler, g *rbacGuards) {
	// Channel CRUD under agents
	agentChannels := g.apiKeyGroup(r.Group("/agents/:id/im-channels"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		agentChannels.POST("", g.OwnerOrSystemAdmin(), imHandler.CreateIMChannel)
		agentChannels.GET("", g.Viewer(), imHandler.ListIMChannels)
	}

	// Channel operations by channel ID
	channels := g.apiKeyGroup(r.Group("/im-channels"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		channels.GET("", g.Viewer(), imHandler.ListAllIMChannels)
		channels.PUT("/:id", g.OwnerOrSystemAdmin(), imHandler.UpdateIMChannel)
		channels.DELETE("/:id", g.OwnerOrSystemAdmin(), imHandler.DeleteIMChannel)
		channels.POST("/:id/toggle", g.OwnerOrSystemAdmin(), imHandler.ToggleIMChannel)
	}


	// WeChat Official Account Cloud pre-auth bind
	if oaHandler != nil {
		oaAgent := g.apiKeyGroup(r.Group("/agents/:id/wechat-oa"), apiKeyManageChannels(apiKeyFullAccess()))
		{
			oaAgent.POST("/preauth", g.OwnerOrSystemAdmin(), oaHandler.CreatePreAuth)
		}
		oStatus := g.apiKeyGroup(r.Group("/wechat-oa"), apiKeyManageChannels(apiKeyFullAccess()))
		{
			oStatus.GET("/preauth/:id", g.OwnerOrSystemAdmin(), oaHandler.GetPreAuthStatus)
		}
	}

	// WeChat QR code login (requires authentication) — Admin+: a successful
	// scan binds a personal WeChat account to the tenant.
	wechatGroup := g.apiKeyGroup(r.Group("/wechat"), apiKeyManageChannels(apiKeyFullAccess()))
	{
		wechatGroup.POST("/qrcode", g.OwnerOrSystemAdmin(), imHandler.WeChatGetQRCode)
		wechatGroup.POST("/qrcode/status", g.OwnerOrSystemAdmin(), imHandler.WeChatPollQRCodeStatus)
	}
}

// embedWebSlugFromPath extracts the short slug from /w/:slug.
func embedWebSlugFromPath(path string) string {
	const prefix = "/w/"
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
// Direct web links (/w/:slug) always set frame-ancestors 'none'.
// When the channel declares no origins (or "*"), no restriction is applied,
// matching the API allowlist semantics. Only GET/HEAD page loads are handled.

func embedFrameAncestorsMiddleware(svc interfaces.EmbedChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Next()
			return
		}
		if slug := embedWebSlugFromPath(c.Request.URL.Path); slug != "" {
			c.Header("Content-Security-Policy", "frame-ancestors 'none'")
			c.Header("X-Frame-Options", "DENY")
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

// RegisterTreeRAGCloudRoutes registers the TreeRAGCloud credential
// management endpoints. SaveCredentials persists external SaaS keys
// for the tenant (Admin+), Status is a low-risk readiness probe (Viewer+).
func RegisterTreeRAGCloudRoutes(r *gin.RouterGroup, handler *handler.TreeRAGCloudHandler, g *rbacGuards) {
	g.apiKeyRoute(r, http.MethodPost, "/weknoracloud/credentials", apiKeyManageModels(apiKeyFullAccess()), g.OwnerOrSystemAdmin(), handler.SaveCredentials)
	g.apiKeyRoute(r, http.MethodGet, "/models/weknoracloud/status", apiKeyManageModels(apiKeyFullAccess()), g.Viewer(), handler.Status)
}
