package router

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
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
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"

	_ "github.com/Tencent/WeKnora/docs" // swagger docs
)

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
	FAQHandler                   *handler.FAQHandler
	TagHandler                   *handler.TagHandler
	CustomAgentHandler           *handler.CustomAgentHandler
	UserFavoriteHandler          *handler.UserResourceFavoriteHandler
	SkillHandler                 *handler.SkillHandler
	OrganizationHandler          *handler.OrganizationHandler
	IMHandler                    *handler.IMHandler
	EmbedChannelHandler          *handler.EmbedChannelHandler
	EmbedChannelService          interfaces.EmbedChannelService
	RedisClient                  *redis.Client
	DataSourceHandler            *handler.DataSourceHandler
	DataSourceCredentialsHandler *handler.DataSourceCredentialsHandler
	WeKnoraCloudHandler          *handler.WeKnoraCloudHandler
	WikiPageHandler              *handler.WikiPageHandler
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
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", "X-Request-ID", "X-Tenant-ID", "X-Embed-Session", "X-External-User-ID", "X-External-User-Token"},
		ExposeHeaders:    []string{"Content-Length", "Access-Control-Allow-Origin"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

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
	RegisterIMRoutes(r, params.IMHandler)

	// Web embed 公开路由（使用 publish token 鉴权，不走全局 Auth）
	RegisterEmbedPublicRoutes(r, params.EmbedChannelHandler, params.EmbedChannelService, params.TenantService, params.RedisClient, params.FileService)

	// 认证中间件
	r.Use(middleware.Auth(params.TenantService, params.UserService, params.TenantMemberService, params.TenantAPIKeyService, params.Config))

	// 文件服务：统一代理本地/MinIO/COS/TOS存储后端（需要认证）
	serveFiles(r, params.FileService)

	// Presigned file access: no auth required, signature-verified.
	servePresignedFiles(r, params.TenantService)

	// Diagnostic preview of presigned URLs (Admin only, behind auth middleware).
	servePresignedPreview(r, params.Config)

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

		RegisterAuthRoutes(v1, params.AuthHandler, rbacGuards)
		RegisterTenantRoutes(v1, params.TenantHandler, params.TenantMemberHandler, params.TenantInvitationHandler, params.AuditLogHandler, rbacGuards)
		RegisterMyInvitationRoutes(v1, params.TenantInvitationHandler)
		RegisterKnowledgeBaseRoutes(v1, params.KBHandler, rbacGuards)
		// KB-scoped image proxy: lets tenants render images embedded in
		// org-shared / agent-visible KB content, which the tenant-scoped
		// /files route cannot serve because it enforces same-tenant paths.
		serveKBScopedFiles(v1, rbacGuards, params.TenantService, params.FileService)
		RegisterKnowledgeTagRoutes(v1, params.TagHandler, rbacGuards)
		RegisterKnowledgeRoutes(v1, params.KnowledgeHandler, rbacGuards)
		RegisterFAQRoutes(v1, params.FAQHandler, rbacGuards)
		RegisterChunkRoutes(v1, params.ChunkHandler, rbacGuards)
		RegisterSessionRoutes(v1, params.SessionHandler, params.MessageSuggestionHandler, rbacGuards)
		RegisterChatRoutes(v1, params.SessionHandler, rbacGuards)
		RegisterMessageRoutes(v1, params.MessageHandler, rbacGuards)
		RegisterModelRoutes(v1, params.ModelHandler, params.ModelCredentialsHandler, rbacGuards)
		RegisterEvaluationRoutes(v1, params.EvaluationHandler, rbacGuards)
		RegisterInitializationRoutes(v1, params.InitializationHandler, rbacGuards)
		RegisterSystemRoutes(v1, params.SystemHandler, rbacGuards)
		RegisterSystemAdminRoutes(v1, params.SystemHandler, params.AuditLogHandler, rbacGuards)
		RegisterMCPServiceRoutes(v1, params.MCPServiceHandler, params.MCPCredentialsHandler, params.MCPOAuthHandler, rbacGuards)
		RegisterWebSearchRoutes(v1, params.WebSearchHandler, rbacGuards)
		RegisterWebSearchProviderRoutes(v1, params.WebSearchProviderHandler, params.WebSearchCredentialsHandler, rbacGuards)
		RegisterVectorStoreRoutes(v1, params.VectorStoreHandler, rbacGuards)
		RegisterCustomAgentRoutes(v1, params.CustomAgentHandler, rbacGuards)
		RegisterUserFavoriteRoutes(v1, params.UserFavoriteHandler, rbacGuards)
		RegisterSkillRoutes(v1, params.SkillHandler, rbacGuards)
		RegisterOrganizationRoutes(v1, params.OrganizationHandler, rbacGuards)
		RegisterIMChannelRoutes(v1, params.IMHandler, rbacGuards)
		RegisterEmbedChannelRoutes(v1, params.EmbedChannelHandler, rbacGuards)
		RegisterDataSourceRoutes(v1, params.DataSourceHandler, params.DataSourceCredentialsHandler, rbacGuards)
		RegisterWeKnoraCloudRoutes(v1, params.WeKnoraCloudHandler, rbacGuards)
		RegisterWikiPageRoutes(v1, params.WikiPageHandler, rbacGuards)
		RegisterChunkerDebugRoutes(v1, rbacGuards)

		// Fail fast if any declared API-key policy points at a route
		// template that does not actually exist (typo / path drift). A
		// stale template would silently 403 every API key on that route,
		// so we panic at startup instead of shipping a dead policy.
		rbacGuards.assertAPIKeyPoliciesMatchRoutes(r)
	}

	return r
}

// RegisterChunkerDebugRoutes wires the read-only chunker preview endpoint
// used by the KB editor's debug panel. Stateless — uses no service deps.
//
// Viewer+ floor: the endpoint surfaces inside the tenant UI, so any
// authenticated tenant member can call it; revoked accounts whose JWT
// has not yet expired are kept out by the role check, matching the
// rest of the RBAC matrix in this file.
func RegisterChunkerDebugRoutes(r *gin.RouterGroup, g *rbacGuards) {
	g.apiKeyRoute(r, http.MethodPost, "/chunker/preview", apiKeyRetrieve(apiKeyIngest(apiKeyFullAccess())), g.Viewer(), handler.PreviewChunking)
}

// RegisterChunkRoutes 注册分块相关的路由
//
// Mutating routes addressed via :knowledge_id inherit per-KB ownership
// from the owning knowledge entry's KB (PR 5, #1303); the chain hop is
// shared with RegisterKnowledgeRoutes via OwnedChunkKBOrAdmin so the
// same "creator-of-the-KB OR Admin+" rule applies to chunk edits.
func RegisterChunkRoutes(r *gin.RouterGroup, handler *handler.ChunkHandler, g *rbacGuards) {
	// 分块路由组。Scoped API key 需要 ingest 能力写内容，retrieve 能力读内容；
	// 两者仍受 KB 白名单约束。
	chunks := g.apiKeyGroup(r.Group("/chunks"), apiKeyIngest(apiKeyFullAccess()))
	chunkRead := chunks.With(apiKeyRetrieve(apiKeyFullAccess()))
	{
		// 获取分块列表 — Viewer+ 且对父 KB 有 read 权限（own / shared / via shared agent）
		chunkRead.GET("/:knowledge_id", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("knowledge_id"), handler.ListKnowledgeChunks)
		// 通过chunk_id获取单个chunk（不需要knowledge_id） — Viewer+ 且对父 KB 有 read 权限
		chunkRead.GET("/by-id/:id", g.Viewer(), g.KBAccessReadFromChunkIDParam("id"), handler.GetChunkByIDOnly)
		// 删除分块 — KB owner OR Admin+，且对父 KB 有 write 权限
		chunks.DELETE("/:knowledge_id/:id", g.OwnedChunkKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("knowledge_id"), handler.DeleteChunk)
		// 删除知识下的所有分块 — KB owner OR Admin+，且对父 KB 有 write 权限
		chunks.DELETE("/:knowledge_id", g.OwnedChunkKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("knowledge_id"), handler.DeleteChunksByKnowledgeID)
		// 更新分块信息 — KB owner OR Admin+，且对父 KB 有 write 权限
		chunks.PUT("/:knowledge_id/:id", g.OwnedChunkKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("knowledge_id"), handler.UpdateChunk)
		// 删除单个生成的问题（通过分块 id） — 与其它 chunk mutation 一致：
		// KB owner OR Admin+。早期这里因为链路 (chunk_id -> knowledge_id ->
		// kb -> creator_id) 还没接通，被临时降级成 Contributor，导致一个
		// 「能编辑所有 chunk 的同样规则在这条路由上反而更宽松」的不一致。
		// 现在通过 KBCreatorLookupFromChunkIDParam 把那一跳补上，统一矩阵。
		chunks.DELETE("/by-id/:id/questions", g.OwnedChunkKBOrAdminFromChunkID(), g.KBAccessWriteFromChunkIDParam("id"), handler.DeleteGeneratedQuestion)
	}
}

// RegisterKnowledgeRoutes 注册知识相关的路由
//
// Per-KB ownership applies on the per-:id mutating routes (PR 5,
// #1303): the URL :id is a knowledge id, OwnedKnowledgeKBOrAdmin
// walks it back to KB.CreatorID so a Contributor who owns the KB can
// edit/delete any of its documents while a non-owner Contributor gets
// 403. KB-scoped upload routes (`/knowledge-bases/:id/knowledge/...`)
// reuse OwnedKBOrAdmin because the URL :id is the KB id directly.
// Cross-:id batch operations stay Contributor-gated — they don't have
// a single owning KB to check against.
func RegisterKnowledgeRoutes(r *gin.RouterGroup, handler *handler.KnowledgeHandler, g *rbacGuards) {
	// 知识库下的知识路由组（URL :id is the KB id）。Scoped API key 需要
	// ingest 能力才能写内容，且仍受 KB 范围限制；清空 KB 只允许 full-access key。
	kb := g.apiKeyGroup(r.Group("/knowledge-bases/:id/knowledge"), apiKeyIngest(apiKeyFullAccess()))
	kbRead := kb.With(apiKeyRetrieve(apiKeyFullAccess()))
	{
		kb.POST("/file", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateKnowledgeFromFile)
		kb.POST("/url", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateKnowledgeFromURL)
		kb.POST("/manual", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateManualKnowledge)
		kbRead.GET("", g.Viewer(), g.KBAccessRead("id"), handler.ListKnowledge)
		// Clearing all contents under a KB is a destructive op; gate
		// behind Admin instead of Contributor.
		kb.With(apiKeyFullAccess()).DELETE("", g.Admin(), g.KBAccessWrite("id"), handler.ClearKnowledgeBaseContents)
	}

	// 知识路由组（URL :id is a knowledge id; the guard walks it to the parent KB）
	kgrp := r.Group("/knowledge")
	k := g.apiKeyGroup(kgrp, apiKeyIngest(apiKeyFullAccess()))
	kRead := k.With(apiKeyRetrieve(apiKeyFullAccess()))
	{
		// Cross-knowledge endpoints (no :id) can't be gated on a single
		// KB — they accept arbitrary knowledge IDs and the handler must
		// fan out the access check itself. /batch and /search are read
		// routes; /move and /batch-delete stay JWT Contributor-gated and are
		// not declared for API keys.
		kRead.GET("/batch", g.Viewer(), handler.GetKnowledgeBatch)
		kRead.GET("/:id", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.GetKnowledge)
		kRead.GET("/:id/stages", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.GetKnowledgeSpans)
		kRead.GET("/:id/spans", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.GetKnowledgeSpans)
		k.DELETE("/:id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.DeleteKnowledge)
		k.PUT("/:id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.UpdateKnowledge)
		k.PUT("/manual/:id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.UpdateManualKnowledge)
		k.POST("/:id/reparse", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.ReparseKnowledge)
		k.POST("/:id/cancel-parse", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.CancelKnowledgeParse)
		kRead.GET("/:id/download", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.DownloadKnowledgeFile)
		kRead.GET("/:id/preview", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.PreviewKnowledgeFile)
		k.PUT("/image/:id/:chunk_id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.UpdateImageInfo)
		kRead.GET("/search", g.Viewer(), handler.SearchKnowledge)
		kRead.GET("/move/progress/:task_id", g.Viewer(), handler.GetKnowledgeMoveProgress)
		// Batch / cross-KB write ops stay Contributor-gated for JWT and are
		// NOT declared for API keys (default-deny): they fan out to arbitrary
		// KBs with no single owning KB to bound a key's scope against.
		kgrp.PUT("/tags", g.Contributor(), handler.UpdateKnowledgeTagBatch)
		kgrp.POST("/batch-reparse", g.Contributor(), handler.BatchReparseKnowledge)
		kgrp.POST("/batch-delete", g.Contributor(), handler.BatchDeleteKnowledge)
		kgrp.POST("/move", g.Contributor(), handler.MoveKnowledge)
	}
}

// RegisterFAQRoutes 注册 FAQ 相关路由
//
// FAQ entries are KB content: reads are Viewer+, all mutations
// (create / update / upsert / delete / batch field+tag updates,
// import display flag) are Contributor+. Search is read-only.
func RegisterFAQRoutes(r *gin.RouterGroup, handler *handler.FAQHandler, g *rbacGuards) {
	if handler == nil {
		return
	}
	// FAQ entries 是 KB 的子资源（FAQ-type KB 的内容主体）。修改 FAQ
	// 等价于修改 KB 内容，必须遵循 KB 的"creator OR Admin+"矩阵 ——
	// 跟 chunks / wiki pages 保持一致。Viewer+ 可以读，Contributor 不能
	// 改不属于自己的 KB 的 FAQ。
	faq := g.apiKeyGroup(r.Group("/knowledge-bases/:id/faq"), apiKeyIngest(apiKeyFullAccess()))
	faqRead := faq.With(apiKeyRetrieve(apiKeyFullAccess()))
	{
		// KBAccessRead/Write resolve own/shared/agent-visible access and
		// rewrite the request's tenant context — handler no longer
		// carries an effectiveCtxForKB helper.
		faqRead.GET("/entries", g.Viewer(), g.KBAccessRead("id"), handler.ListEntries)
		faqRead.GET("/entries/export", g.Viewer(), g.KBAccessRead("id"), handler.ExportEntries)
		faqRead.GET("/entries/:entry_id", g.Viewer(), g.KBAccessRead("id"), handler.GetEntry)
		faq.POST("/entries", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpsertEntries)
		faq.POST("/entry", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateEntry)
		faq.PUT("/entries/:entry_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateEntry)
		faq.POST("/entries/:entry_id/similar-questions", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.AddSimilarQuestions)
		// Unified batch update API - supports is_enabled, is_recommended, tag_id
		faq.PUT("/entries/fields", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateEntryFieldsBatch)
		faq.PUT("/entries/tags", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateEntryTagBatch)
		faq.DELETE("/entries", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.DeleteEntries)
		// Search is a read route: scoped API keys may call it with retrieve
		// even though POST is otherwise an unsafe method.
		faqRead.POST("/search", g.Viewer(), g.KBAccessRead("id"), handler.SearchFAQ)
		// FAQ import result display status
		faq.PUT("/import/last-result/display", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateLastImportResultDisplayStatus)
	}
	// FAQ import progress route (outside of knowledge-base scope) — Viewer+.
	// Scoped API keys that can ingest (they start the import) or retrieve may
	// poll their own import/dry-run progress. The task is tenant-scoped by
	// requireTaskProgressTenant, so a key only ever sees its own tenant's
	// tasks. Declared through apiKeyRoute so the APIKeyGate doesn't fail-closed
	// and 403 the poller with "scope does not allow this operation".
	g.apiKeyRoute(r, http.MethodGet, "/faq/import/progress/:task_id",
		apiKeyRetrieve(apiKeyIngest(apiKeyFullAccess())), g.Viewer(), handler.GetImportProgress)
}

// RegisterKnowledgeBaseRoutes 注册知识库相关的路由
func RegisterKnowledgeBaseRoutes(r *gin.RouterGroup, handler *handler.KnowledgeBaseHandler, g *rbacGuards) {
	// 知识库路由组。Scoped API key 需要 retrieve 能力读取（限 KB 范围）；KB 内容写入
	// 由 RegisterKnowledgeRoutes/FAQ/Tag/Wiki 等子路由的 ingest 能力控制；
	// KB 自身的元数据/生命周期管理需要 full access 或 manage_kbs。创建/拷贝
	// KB 没有单个目标 KB 可约束，因此不对 scoped key 开放（capability 无法
	// 把它约束到某个 KB 上）；但 full-access key 是空间级全权，与它已能
	// 更新/删除/管理 KB 对齐，允许其创建 KB，消除"能删不能建"的不一致。
	kbgrp := r.Group("/knowledge-bases")
	kb := g.apiKeyGroup(kbgrp, apiKeyRetrieve(apiKeyFullAccess()))
	kbManagement := kb.With(apiKeyManageKnowledgeBases(apiKeyFullAccess()))
	{
		// 创建知识库 — Contributor+ for JWT callers; full-access API keys may
		// create KBs (scoped keys stay default-deny: no KB to bound against).
		kb.With(apiKeyFullAccess()).POST("", g.Contributor(), handler.CreateKnowledgeBase)
		// 获取知识库列表 — Viewer+ for JWT callers; retrieve-capable API keys pass via the gate.
		kb.GET("", g.Viewer(), handler.ListKnowledgeBases)
		// 获取知识库详情 — Viewer+ 且对 KB 有 read 权限
		kb.GET("/:id", g.Viewer(), g.KBAccessRead("id"), handler.GetKnowledgeBase)
		// 更新知识库 — 创建者本人 OR Admin+ 且对 KB 有 write 权限
		kbManagement.PUT("/:id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateKnowledgeBase)
		// 删除知识库 — 创建者本人 OR Admin+ 且对 KB 有 write 权限
		kbManagement.DELETE("/:id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.DeleteKnowledgeBase)
		// 置顶/取消置顶知识库 — 创建者本人 OR Admin+ 且对 KB 有 write 权限
		// Pin state is now per-(user, kb) (migration 000050). Anyone with
		// at least Viewer-level read access to the KB — including users
		// who reached it via a shared agent — may pin it for themselves;
		// no edit permission is required. The OwnedKBOrAdmin guard was
		// removed accordingly. The route still requires KB read access
		// so callers can't poke at KBs they can't see.
		kb.PUT("/:id/pin", g.Viewer(), g.KBAccessRead("id"), handler.TogglePinKnowledgeBase)
		// 混合搜索 — Viewer+ 且对 KB 有 read 权限 (read-only)
		// POST is preferred; GET with JSON body is kept for backward compatibility (#1727).
		kb.POST("/:id/hybrid-search", g.Viewer(), g.KBAccessRead("id"), handler.HybridSearch)
		kb.GET("/:id/hybrid-search", g.Viewer(), g.KBAccessRead("id"), handler.HybridSearch)
		// 拷贝知识库 — Contributor+ (副本归调用者所有；不需要原 KB 的所有权)
		kbgrp.POST("/copy", g.Contributor(), handler.CopyKnowledgeBase)
		// 创建知识库副本 — Contributor+ 且对源 KB 有 read 权限；只创建新的 KB 设置记录，不复制内容/索引/分享。
		kbgrp.POST("/:id/duplicate", g.Contributor(), g.KBAccessRead("id"), handler.DuplicateKnowledgeBase)
		// 获取知识库复制进度 — Viewer+
		kb.GET("/copy/progress/:task_id", g.Viewer(), handler.GetKBCloneProgress)
		// 获取可移动目标知识库列表 — Viewer+ 且对 KB 有 read 权限
		kb.GET("/:id/move-targets", g.Viewer(), g.KBAccessRead("id"), handler.ListMoveTargets)
	}
}

// RegisterKnowledgeTagRoutes 注册知识库标签相关路由。
//
// Tags are KB metadata: Viewer reads, Contributor writes. Per-KB
// ownership granularity for tags is out of scope for PR 2; this is
// purely role-based.
func RegisterKnowledgeTagRoutes(r *gin.RouterGroup, tagHandler *handler.TagHandler, g *rbacGuards) {
	if tagHandler == nil {
		return
	}
	// Tags 是 KB 的子资源 — 创建/编辑/删除标签会改变 KB 内容的检索分类
	// 行为，应该与 KB 主体的"creator OR Admin+"矩阵一致，避免一个无
	// 关 Contributor 在他人 KB 里乱建/删标签影响 KB owner 的内容组织。
	kbTags := g.apiKeyGroup(r.Group("/knowledge-bases/:id/tags"), apiKeyIngest(apiKeyFullAccess()))
	kbTagsRead := kbTags.With(apiKeyRetrieve(apiKeyFullAccess()))
	{
		// KBAccessRead/Write resolve own/shared/agent-visible access and
		// rewrite the request's tenant context to the effective tenant
		// for the duration of the handler — so the handler no longer
		// needs its own effectiveCtxForKB helper.
		kbTagsRead.GET("", g.Viewer(), g.KBAccessRead("id"), tagHandler.ListTags)
		kbTags.POST("", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), tagHandler.CreateTag)
		kbTags.PUT("/:tag_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), tagHandler.UpdateTag)
		kbTags.DELETE("/:tag_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), tagHandler.DeleteTag)
	}
}

// RegisterMessageRoutes 注册消息相关的路由。
//
// Per-session ownership is already enforced inside each handler (the
// user must own the session). We add Viewer+ here so non-members
// (e.g. revoked accounts retained in the tenant for audit) cannot
// reach the endpoints at all once RBAC is on.
func RegisterMessageRoutes(r *gin.RouterGroup, handler *handler.MessageHandler, g *rbacGuards) {
	// Message history is tenant-wide and not attributable to a KB, so it is
	// a full-access surface for API keys by default. The narrow
	// exceptions are explicit capabilities:
	//   - chat: load/delete messages inside the caller's own session, where
	//     ownership is enforced by the message service.
	//   - message_history: search/read tenant chat-history metadata without
	//     granting every other full-access API.
	messages := g.apiKeyGroup(r.Group("/messages"), apiKeyFullAccess())
	chatMessages := messages.With(apiKeyChat(apiKeyFullAccess()))
	historyMessages := messages.With(apiKeyMessageHistory(apiKeyFullAccess()))
	{
		historyMessages.POST("/search", g.Viewer(), handler.SearchMessages)
		historyMessages.GET("/chat-history-stats", g.Viewer(), handler.GetChatHistoryKBStats)
		chatMessages.GET("/:session_id/load", g.Viewer(), handler.LoadMessages)
		chatMessages.DELETE("/:session_id/:id", g.Viewer(), handler.DeleteMessage)
	}
}

// RegisterSessionRoutes 注册路由。
//
// Sessions are per-user resources; the handler enforces user ownership.
// We gate at Viewer+ to keep non-members out once RBAC is on, matching
// the message routes above. A future refactor can introduce
// per-session ownership in the middleware layer the same way KB/agent
// routes do today.
func RegisterSessionRoutes(
	r *gin.RouterGroup,
	handler *session.Handler,
	suggestionHandler *handler.MessageSuggestionHandler,
	g *rbacGuards,
) {
	// Sessions are per-user chat state, not knowledge-base content. The
	// chat capability lets a scoped key run the full conversation flow
	// (create/manage its own sessions) without full tenant access.
	sessions := g.apiKeyGroup(r.Group("/sessions", g.Viewer()), apiKeyChat(apiKeyFullAccess()))
	{
		sessions.POST("", handler.CreateSession)
		sessions.DELETE("/batch", handler.BatchDeleteSessions)
		sessions.GET("/:id", handler.GetSession)
		sessions.GET("", handler.GetSessionsByTenant)
		sessions.PUT("/:id", handler.UpdateSession)
		sessions.DELETE("/:id", handler.DeleteSession)
		sessions.DELETE("/:id/messages", handler.ClearSessionMessages)
		sessions.POST("/:session_id/generate_title", handler.GenerateTitle)
		sessions.POST("/:session_id/stop", handler.StopSession)
		// POST and DELETE share this path but gin maintains a separate radix tree
		// per HTTP verb, and the existing trees use different wildcard names
		// (POST uses :session_id, DELETE uses :id). Use whatever matches each
		// tree to avoid "wildcard conflicts" panic at route registration.
		sessions.POST("/:session_id/pin", handler.PinSession)
		sessions.DELETE("/:id/pin", handler.UnpinSession)
		// 继续接收活跃流
		sessions.GET("/continue-stream/:session_id", handler.ContinueStream)
		if suggestionHandler != nil {
			// Gin requires wildcard names to be identical within the same HTTP-method
			// radix tree. Existing GET session routes use :id, so keep that name here.
			sessions.GET("/:id/messages/:message_id/suggestions", suggestionHandler.Get)
			sessions.POST("/:session_id/messages/:message_id/suggestions", suggestionHandler.Ensure)
			sessions.POST("/:session_id/suggestion-events", suggestionHandler.RecordEvent)
		}
	}
}

// RegisterChatRoutes 注册路由。Chat endpoints are tenant-member usage
// surfaces; Viewer+ is sufficient because per-session/per-agent
// authorisation is enforced inside the handlers.
func RegisterChatRoutes(r *gin.RouterGroup, handler *session.Handler, g *rbacGuards) {
	// These POST routes append messages and run generation, so a scoped key
	// needs the explicit chat capability unless it has full tenant access.
	knowledgeChat := g.apiKeyGroup(r.Group("/knowledge-chat", g.Viewer()), apiKeyChat(apiKeyFullAccess()))
	{
		knowledgeChat.POST("/:session_id", handler.KnowledgeQA)
	}

	// Agent-based chat
	agentChat := g.apiKeyGroup(r.Group("/agent-chat", g.Viewer()), apiKeyChat(apiKeyFullAccess()))
	{
		agentChat.POST("/:session_id", handler.AgentQA)
	}

	// 新增知识检索接口，不需要session_id
	knowledgeSearch := g.apiKeyGroup(r.Group("/knowledge-search", g.Viewer()), apiKeyRetrieve(apiKeyFullAccess()))
	{
		knowledgeSearch.POST("", handler.SearchKnowledge)
	}
}

// RegisterTenantRoutes 注册空间相关的路由
//
// Tenant-internal RBAC for /tenants/:id:
//   - GET   /:id          Viewer+ (read tenant settings)
//   - PUT   /:id          Owner+ (mutate tenant config)
//   - DELETE /:id         Owner+ (also normally a CanAccessAllTenants op)
//   - GET/POST/DELETE /:id/api-keys   Owner+ (scoped API key management)
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
// POST /tenants and GET /tenants stay open to authenticated users —
// the previous handler comments claimed CanAccessAllTenants gating
// "is in the handler" but the bodies never enforced it; this PR is a
// pure refactor and does not introduce new gates.
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
	r.GET("/tenants/all", g.CrossTenant(), handler.ListAllTenants)
	r.GET("/tenants/search", g.CrossTenant(), handler.SearchTenants)

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
		tenantRoutes.POST("", handler.CreateTenant)
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
			tenantByID.GET("", g.Viewer(), handler.GetTenant)
			tenantByID.PUT("", g.Owner(), handler.UpdateTenant)
			tenantByID.DELETE("", g.Owner(), handler.DeleteTenant)
			tenantByID.GET("/api-keys", g.Owner(), handler.ListAPIKeys)
			tenantByID.POST("/api-keys", g.Owner(), handler.CreateAPIKey)
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

// Models are tenant-wide infrastructure (LLM credentials, embeddings,
// rerankers); Viewer+ for reads, Admin+ for any mutation. Credential
// subresource writes are also Admin+ since secrets are tenant-scoped.
func RegisterModelRoutes(
	r *gin.RouterGroup,
	handler *handler.ModelHandler,
	credHandler *handler.ModelCredentialsHandler,
	g *rbacGuards,
) {
	// 模型路由组。空间级基础设施：仅完全访问（Owner）API key 可访问。
	models := g.apiKeyGroup(r.Group("/models"), apiKeyManageModels(apiKeyFullAccess()))
	{
		// 获取模型厂商列表 — Viewer+
		models.GET("/providers", g.Viewer(), handler.ListModelProviders)
		// 创建模型 — Admin+
		models.POST("", g.Admin(), handler.CreateModel)
		// 获取模型列表 — Viewer+
		models.GET("", g.Viewer(), handler.ListModels)
		// 调试已保存模型会发起真实上游调用并产生费用 — Admin+
		models.POST("/:id/debug", g.Admin(), handler.DebugModel)
		// 获取单个模型 — Viewer+
		models.GET("/:id", g.Viewer(), handler.GetModel)
		// 更新模型 — Admin+；内置模型仍由服务层额外限定为 SystemAdmin。
		models.PUT("/:id", g.AdminOrSystemAdmin(), handler.UpdateModel)
		// 删除模型 — Admin+
		models.DELETE("/:id", g.Admin(), handler.DeleteModel)
		// Per-field credential subresource (see internal/handler/model_credentials.go) — Admin+
		models.PUT("/:id/credentials", g.AdminOrSystemAdmin(), credHandler.Put)
		models.DELETE("/:id/credentials/:field", g.AdminOrSystemAdmin(), credHandler.DeleteField)
	}
}

// RegisterEvaluationRoutes registers evaluation endpoints. Running an
// evaluation drives LLM calls (cost) and reads from KBs across the
// tenant; gate to Admin+ until product asks for a finer-grained
// matrix.
func RegisterEvaluationRoutes(r *gin.RouterGroup, handler *handler.EvaluationHandler, g *rbacGuards) {
	evaluationRoutes := g.apiKeyGroup(r.Group("/evaluation"), apiKeyRunEvaluations(apiKeyFullAccess()))
	{
		evaluationRoutes.POST("", g.Admin(), handler.Evaluation)
		evaluationRoutes.GET("", g.Viewer(), handler.GetEvaluationResult)
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

func RegisterInitializationRoutes(r *gin.RouterGroup, handler *handler.InitializationHandler, g *rbacGuards) {
	// 初始化接口
	// GetCurrentConfigByKB 是只读，Viewer+ 即可（KB 受限 key 可读其范围内的 KB）。
	g.apiKeyRoute(r, http.MethodGet, "/initialization/config/:kbId",
		apiKeyRetrieve(apiKeyFullAccess()), g.Viewer(), g.KBAccessRead("kbId"), handler.GetCurrentConfigByKB)
	// InitializeByKB / UpdateKBConfig 都是改 KB 的核心模型/storage 配置 —
	// 跟 PUT /knowledge-bases/:id 同等敏感，挂同款 OwnedKB 矩阵 + KBAccessWrite
	//（API-key 主体短路 Owned* 守卫，KB allow-list 只能靠 KBAccess 兜底）。
	g.apiKeyRoute(r, http.MethodPost, "/initialization/initialize/:kbId",
		apiKeyManageKnowledgeBases(apiKeyFullAccess()), g.OwnedKBOrAdminFromKbIDParam(), g.KBAccessWrite("kbId"), handler.InitializeByKB)
	g.apiKeyRoute(r, http.MethodPut, "/initialization/config/:kbId",
		apiKeyManageKnowledgeBases(apiKeyFullAccess()), g.OwnedKBOrAdminFromKbIDParam(), g.KBAccessWrite("kbId"), handler.UpdateKBConfig)

	// Ollama / 远程 API / 抽取等系统级检测/下载操作。这些不绑某个 KB，
	// 会改空间级模型配置或拉远端模型；JWT 侧只读探测 Viewer+、变更 Admin+。
	// 对 API key 均为空间级：full-access key 可用，scoped key 需要 manage_models。
	g.apiKeyRoute(r, http.MethodGet, "/initialization/ollama/status", apiKeyManageModels(apiKeyFullAccess()), g.Viewer(), handler.CheckOllamaStatus)
	g.apiKeyRoute(r, http.MethodGet, "/initialization/ollama/models", apiKeyManageModels(apiKeyFullAccess()), g.Viewer(), handler.ListOllamaModels)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/ollama/models/check", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.CheckOllamaModels)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/ollama/models/download", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.DownloadOllamaModel)
	g.apiKeyRoute(r, http.MethodGet, "/initialization/ollama/download/progress/:taskId", apiKeyManageModels(apiKeyFullAccess()), g.Viewer(), handler.GetDownloadProgress)
	g.apiKeyRoute(r, http.MethodGet, "/initialization/ollama/download/tasks", apiKeyManageModels(apiKeyFullAccess()), g.Viewer(), handler.ListDownloadTasks)

	// 远程API相关接口
	g.apiKeyRoute(r, http.MethodPost, "/initialization/remote/check", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.CheckRemoteModel)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/embedding/test", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.TestEmbeddingModel)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/rerank/check", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.CheckRerankModel)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/asr/check", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.CheckASRModel)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/multimodal/test", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.TestMultimodalFunction)

	g.apiKeyRoute(r, http.MethodPost, "/initialization/extract/text-relation", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.ExtractTextRelations)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/extract/fabri-tag", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.FabriTag)
	g.apiKeyRoute(r, http.MethodPost, "/initialization/extract/fabri-text", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.FabriText)
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

// RegisterMCPServiceRoutes registers MCP service routes.
//
// MCP services are tenant-level integrations (external tool servers); we
// gate reads to Viewer+ and any mutation/test to Admin+. Tool-approval
// resolution is also Admin+ since approving a pending tool call grants
// the agent permission to execute side-effecting external commands.
// Credential subresource writes are Admin+ as well since secrets are
// tenant-scoped.
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

		// P1: platform-wide system settings (DB-backed runtime tunables).
		// Reads return raw model rows / arrays (no `gin.H{"data":...}`
		// wrapping), matching the project's axios interceptor convention
		// — see frontend/src/utils/request.ts:97.
		adminRoutes.GET("/settings", handler.ListSystemSettings)
		adminRoutes.GET("/settings/:key", handler.GetSystemSetting)
		adminRoutes.PUT("/settings/:key", handler.UpdateSystemSetting)
		adminRoutes.DELETE("/settings/:key", handler.ResetSystemSetting)

		// Runtime operations: live asynq queue depths, safe task projections,
		// and state-checked task actions for the SystemAdmin dashboard. Lite
		// mode returns available=false.
		adminRoutes.GET("/runtime/queues", handler.GetRuntimeQueues)
		adminRoutes.GET("/runtime/queues/:queue/tasks", handler.ListRuntimeTasks)
		adminRoutes.POST("/runtime/queues/:queue/tasks/:task_id/actions/:action", handler.MutateRuntimeTask)

		// Bulk action — write the current default-quota setting onto
		// every existing tenant. Lives under /tenants instead of
		// /settings because it changes tenants, not the setting row.
		adminRoutes.POST(
			"/tenants/apply-default-storage-quota",
			handler.ApplyDefaultStorageQuotaToAllTenants,
		)

		// Platform-wide audit feed (tenant_id=0 rows). Covers
		// system.setting_changed / system.admin_promoted /
		// system.admin_revoked etc. — events written by the routes
		// above. Without this endpoint those audit rows would have
		// no UI surface (per-tenant ListTenantAuditLog filters them
		// out by tenant_id). Optional: skip when audit deps are
		// absent, matching RegisterTenantRoutes' /audit-log handling.
		if auditLogHandler != nil {
			adminRoutes.GET("/audit-log", auditLogHandler.ListSystemAuditLog)
		}
	}
}

func RegisterMCPServiceRoutes(
	r *gin.RouterGroup,
	handler *handler.MCPServiceHandler,
	credHandler *handler.MCPCredentialsHandler,
	oauthHandler *handler.MCPOAuthHandler,
	g *rbacGuards,
) {
	// MCP OAuth provider redirect. Registered OUTSIDE the /mcp-services group
	// to avoid a static-vs-":id" route conflict, and left unauthenticated
	// (allow-listed in middleware/auth.go) because the third-party browser
	// redirect carries no WeKnora bearer — the single-use state authenticates.
	r.GET("/mcp-oauth/callback", oauthHandler.Callback)

	mcpServices := g.apiKeyGroup(r.Group("/mcp-services"), apiKeyManageMCPServices(apiKeyFullAccess()))
	{
		// Create MCP service — Admin+
		mcpServices.POST("", g.Admin(), handler.CreateMCPService)
		// List MCP services — Viewer+
		mcpServices.GET("", g.Viewer(), handler.ListMCPServices)
		// Get MCP service by ID — Viewer+
		mcpServices.GET("/:id", g.Viewer(), handler.GetMCPService)
		// Update MCP service — Admin+
		mcpServices.PUT("/:id", g.Admin(), handler.UpdateMCPService)
		// Delete MCP service — Admin+
		mcpServices.DELETE("/:id", g.Admin(), handler.DeleteMCPService)
		// Test MCP service connection — Admin+ (probes external infra)
		mcpServices.POST("/:id/test", g.Admin(), handler.TestMCPService)
		// Get MCP service tools — Viewer+
		mcpServices.GET("/:id/tools", g.Viewer(), handler.GetMCPServiceTools)
		// Get MCP service resources — Viewer+
		mcpServices.GET("/:id/resources", g.Viewer(), handler.GetMCPServiceResources)
		// Per-field credential subresource: secrets never travel via the main
		// PUT body. See internal/handler/mcp_credentials.go for the contract. — Admin+
		mcpServices.PUT("/:id/credentials", g.Admin(), credHandler.Put)
		mcpServices.DELETE("/:id/credentials/:field", g.Admin(), credHandler.DeleteField)
		// MCP tool human approval (issue #1173) — Viewer+ to read, Admin+ to set policy
		mcpServices.GET("/:id/tool-approvals", g.Viewer(), handler.ListMCPToolApprovals)
		mcpServices.PUT("/:id/tool-approvals/:tool_name", g.Admin(), handler.SetMCPToolApproval)
		// Per-user OAuth authorization flow. Viewer+ may authorize/inspect/
		// revoke their own token; the callback is the separate public route
		// registered above.
		mcpServices.POST("/:id/oauth/authorize-url", g.Viewer(), oauthHandler.AuthorizeURL)
		mcpServices.GET("/:id/oauth/status", g.Viewer(), oauthHandler.Status)
		mcpServices.DELETE("/:id/oauth/token", g.Viewer(), oauthHandler.Revoke)
	}

	// /agent tool-approval + OAuth resolution are interactive human flows;
	// not declared for API keys (default-deny).
	agentTool := r.Group("/agent")
	{
		// Resolving a pending tool-approval is gated to tenant members
		// (Viewer+). The approval card surfaces inside an agent chat the
		// caller initiated — restricting it to Admin+ blocks the only
		// people who actually have context to approve, so the gate is
		// kept at "anyone in the tenant" instead.
		agentTool.POST("/tool-approvals/:pending_id", g.Viewer(), handler.ResolveToolApproval)
		// Resume an agent run paused on an in-conversation MCP OAuth prompt.
		// Same tenant-member (Viewer+) gating rationale as tool-approvals.
		agentTool.POST("/mcp-oauth-resolutions/:pending_id", g.Viewer(), oauthHandler.ResolveMCPOAuth)
		agentTool.POST("/mcp-oauth-resolutions/:pending_id/cancel", g.Viewer(), oauthHandler.CancelMCPOAuth)
	}
}

// RegisterWebSearchRoutes registers web search routes
func RegisterWebSearchRoutes(r *gin.RouterGroup, webSearchHandler *handler.WebSearchHandler, g *rbacGuards) {
	// Web search providers — Viewer+ (read-only listing of provider catalog).
	webSearch := r.Group("/web-search")
	{
		webSearch.GET("/providers", g.Viewer(), webSearchHandler.GetProviders)
	}
}

// RegisterWebSearchProviderRoutes registers CRUD routes for web search
// provider configurations.
//
// Provider rows hold external service credentials (Bing, Tavily, Google,
// etc.); reads are Viewer+, all mutations / connection tests (which
// probe external systems with stored credentials) and the per-field
// credential subresource are Admin+.
func RegisterWebSearchProviderRoutes(
	r *gin.RouterGroup,
	h *handler.WebSearchProviderHandler,
	credHandler *handler.WebSearchProviderCredentialsHandler,
	g *rbacGuards,
) {
	providers := g.apiKeyGroup(r.Group("/web-search-providers"), apiKeyManageWebSearch(apiKeyFullAccess()))
	{
		// List available provider types (metadata for UI forms) — Viewer+
		providers.GET("/types", g.Viewer(), h.ListProviderTypes)
		// Test with raw credentials (no persistence) — Admin+
		providers.POST("/test", g.Admin(), h.TestProviderRaw)
		// CRUD
		providers.POST("", g.Admin(), h.CreateProvider)
		providers.GET("", g.Viewer(), h.ListProviders)
		providers.GET("/:id", g.Viewer(), h.GetProvider)
		providers.PUT("/:id", g.Admin(), h.UpdateProvider)
		providers.DELETE("/:id", g.Admin(), h.DeleteProvider)
		// Per-field credential subresource — Admin+
		providers.PUT("/:id/credentials", g.Admin(), credHandler.Put)
		providers.DELETE("/:id/credentials/:field", g.Admin(), credHandler.DeleteField)
		// Test existing saved provider — Admin+
		providers.POST("/:id/test", g.Admin(), h.TestProviderByID)
	}
}

// RegisterVectorStoreRoutes registers CRUD routes for vector store configurations.
//
// Vector stores are tenant-level infrastructure; reads are Viewer+, all
// writes (and connection tests, which probe external systems with stored
// credentials) are Admin+.
func RegisterVectorStoreRoutes(r *gin.RouterGroup, h *handler.VectorStoreHandler, g *rbacGuards) {
	stores := g.apiKeyGroup(r.Group("/vector-stores"), apiKeyManageVectorStores(apiKeyFullAccess()))
	{
		// List available engine types (metadata for UI forms) — Viewer+
		stores.GET("/types", g.Viewer(), h.ListStoreTypes)
		// Test with raw credentials (no persistence) — Admin+
		stores.POST("/test", g.Admin(), h.TestStoreRaw)
		// CRUD
		stores.POST("", g.Admin(), h.CreateStore)
		stores.GET("", g.Viewer(), h.ListStores)
		stores.GET("/:id", g.Viewer(), h.GetStore)
		stores.PUT("/:id", g.Admin(), h.UpdateStore)
		stores.DELETE("/:id", g.Admin(), h.DeleteStore)
		// Test existing saved or env store — Admin+
		stores.POST("/:id/test", g.Admin(), h.TestStoreByID)
	}
}

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
		embed.GET("/files", newFileServeHandler(fileService))
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

// serveFrontendStatic registers a middleware that serves the frontend SPA
// from the ./web directory if it exists. Must be called BEFORE auth middleware
// so static files are served without authentication.
func serveFrontendStatic(r *gin.Engine) {
	webDir := os.Getenv("WEKNORA_WEB_DIR")
	if webDir == "" {
		webDir = "./web"
	}
	absDir, _ := filepath.Abs(webDir)
	indexPath := filepath.Join(absDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		return
	}

	logger.Infof(context.Background(), "[Router] Serving frontend static files from %s", absDir)

	fs := http.Dir(absDir)
	fileServer := http.FileServer(fs)

	r.Use(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/health") || strings.HasPrefix(path, "/swagger/") {
			c.Next()
			return
		}
		fullPath := filepath.Join(absDir, path)
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			setFrontendCacheHeaders(c.Writer, path)
			fileServer.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}
		setFrontendCacheHeaders(c.Writer, "/index.html")
		c.File(indexPath)
		c.Abort()
	})
}

// setFrontendCacheHeaders sets Cache-Control headers for frontend static resources.
// Vite 构建产物中 /assets/* 的文件名带 hash，可长期缓存；其余（index.html、config.js、favicon 等）
// 每次都需 revalidate，避免前端升级后用户看到旧版本。
func setFrontendCacheHeaders(w http.ResponseWriter, path string) {
	if strings.HasPrefix(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
}

// serveFiles serves files via query parameters and tenant storage settings.
// It is registered after auth middleware, so tenant context comes from authentication.
//
// Route:
//   - /files?file_path=<provider://...>
type getRouteRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
}

// newFileServeHandler builds the file-proxy handler. It reads the tenant from
// the request context (set by whichever auth middleware precedes it), so the
// same handler backs both the authenticated /files route and the embed route
// (where EmbedAuth injects the channel's tenant). Tenant ownership of the
// requested path is enforced via ValidateStoragePathTenant either way.
func newFileServeHandler(globalFileService interfaces.FileService) gin.HandlerFunc {
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	absDir, _ := filepath.Abs(baseDir)
	if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			logger.Warnf(context.Background(), "[Router] Cannot create local storage dir %s: %v", absDir, err)
		}
	}

	return func(c *gin.Context) {
		filePath := strings.TrimSpace(c.Query("file_path"))
		if filePath == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameter: file_path"})
			return
		}
		if strings.Contains(filePath, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
			return
		}

		provider := types.ParseProviderScheme(filePath)

		tenant, _ := c.Request.Context().Value(types.TenantInfoContextKey).(*types.Tenant)
		if tenant == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
			return
		}

		if err := secutils.ValidateStoragePathTenant(filePath, tenant.ID); err != nil {
			logger.Warnf(context.Background(), "[Router] /files denied cross-tenant or invalid path: tenant_id=%d file_path=%q err=%v", tenant.ID, filePath, err)
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: file path not accessible"})
			return
		}

		var (
			fileSvc          interfaces.FileService
			resolvedProvider string
			err              error
		)

		if tenant.StorageEngineConfig != nil {
			fileSvc, resolvedProvider, err = filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
		} else {
			err = http.ErrMissingFile
		}
		if err != nil {
			globalStorageType := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
			if globalStorageType == "" {
				globalStorageType = "local"
			}
			if provider == globalStorageType && globalFileService != nil {
				logger.Warnf(context.Background(), "[Router] /files tenant storage config missing or invalid, fallback to global file service: tenant_id=%d provider=%s err=%v", tenant.ID, provider, err)
				fileSvc = globalFileService
				resolvedProvider = globalStorageType
			} else {
				logger.Warnf(context.Background(), "[Router] /files resolve file service failed without fallback: tenant_id=%d provider=%s global_storage_type=%s err=%v", tenant.ID, provider, globalStorageType, err)
				c.Status(http.StatusBadRequest)
				return
			}
		}

		reader, err := fileSvc.GetFile(c.Request.Context(), filePath)
		if err != nil {
			logger.Warnf(context.Background(), "[Router] /files get file failed: tenant_id=%d provider=%s path=%q err=%v", tenant.ID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}
		defer reader.Close()

		contentType, inline := secutils.SafeContentTypeByFilename(filePath)
		c.Header("Content-Type", contentType)
		c.Header("X-Content-Type-Options", "nosniff")
		if !inline {
			c.Header("Content-Disposition", "attachment")
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, reader); err != nil {
			logger.Warnf(context.Background(), "[Router] /files write response failed: %v", err)
		}
	}
}

func serveFiles(r getRouteRegistrar, globalFileService interfaces.FileService) {
	logger.Infof(context.Background(), "[Router] Serving files from /files")
	// /files sits outside the /api/v1 APIKeyGate; storage paths cannot be
	// attributed to a key's KB allow-list, so API keys are denied outright
	// (embed routes use their own /embed/.../files handler).
	r.GET("/files", middleware.DenyAPIKeyPrincipal(), newFileServeHandler(globalFileService))
}

// serveKBScopedFiles registers the KB-scoped file proxy used to render images
// embedded in a knowledge base's content (chunks / wiki pages). Unlike the
// tenant-scoped /files route — which enforces file_path.tenant == caller.tenant
// and therefore cannot serve objects owned by another tenant — this route is
// gated by RequireKBAccess. That guard resolves org-shared / agent-visible KBs
// and rewrites the request context's tenant ID to the KB's *owner* (source)
// tenant, so images stored under the owner tenant (local://<owner>/exports/...)
// become reachable by tenants that legitimately share the KB, while still
// enforcing that the requested path belongs to that owner tenant.
//
// Route:
//   - GET /api/v1/knowledge-bases/:id/files?file_path=<provider://...>
func serveKBScopedFiles(
	r *gin.RouterGroup,
	g *rbacGuards,
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
) {
	logger.Infof(context.Background(), "[Router] Serving KB-scoped files from /knowledge-bases/:id/files")
	// API keys are denied outright (as on /files): a signed URL's tenant/KB
	// scope cannot authorize serving an arbitrary file_path under the owner
	// tenant, and this route deliberately crosses the caller's own tenant.
	r.GET("/knowledge-bases/:id/files",
		middleware.DenyAPIKeyPrincipal(),
		g.Viewer(),
		g.KBAccessRead("id"),
		newKBScopedFileServeHandler(tenantService, globalFileService),
	)
}

// newKBScopedFileServeHandler builds the handler backing serveKBScopedFiles.
// The effective (owner) tenant is taken from the request context, which
// RequireKBAccess has already rewritten to the KB's source tenant. The owner
// tenant's storage config is loaded via TenantService so the file is fetched
// from the backend that actually holds it — the caller's own storage config is
// irrelevant here.
func newKBScopedFileServeHandler(
	tenantService interfaces.TenantService,
	globalFileService interfaces.FileService,
) gin.HandlerFunc {
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	absDir, _ := filepath.Abs(baseDir)

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		filePath := strings.TrimSpace(c.Query("file_path"))
		if filePath == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameter: file_path"})
			return
		}
		if strings.Contains(filePath, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
			return
		}

		// RequireKBAccess rewrote the request context tenant ID to the KB's
		// owner (source) tenant for shared KBs; for own KBs it equals the
		// caller's tenant. Either way it is the tenant that owns this KB's
		// storage objects, so the requested path must belong to it.
		ownerTenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || ownerTenantID == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
			return
		}

		if err := secutils.ValidateKBScopedStoragePath(filePath, ownerTenantID); err != nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files denied path not allowed for KB proxy: owner_tenant_id=%d file_path=%q err=%v",
				ownerTenantID, filePath, err)
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden: file path not accessible"})
			return
		}

		tenant, err := tenantService.GetTenantByID(ctx, ownerTenantID)
		if err != nil || tenant == nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files owner tenant lookup failed: owner_tenant_id=%d err=%v",
				ownerTenantID, err)
			c.Status(http.StatusNotFound)
			return
		}

		provider := types.ParseProviderScheme(filePath)

		var (
			fileSvc          interfaces.FileService
			resolvedProvider string
		)
		if tenant.StorageEngineConfig != nil {
			fileSvc, resolvedProvider, err = filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
		} else {
			err = http.ErrMissingFile
		}
		if err != nil {
			globalStorageType := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
			if globalStorageType == "" {
				globalStorageType = "local"
			}
			if provider == globalStorageType && globalFileService != nil {
				fileSvc = globalFileService
				resolvedProvider = globalStorageType
			} else {
				logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files resolve file service failed: owner_tenant_id=%d provider=%s global_storage_type=%s err=%v",
					ownerTenantID, provider, globalStorageType, err)
				c.Status(http.StatusBadRequest)
				return
			}
		}

		reader, err := fileSvc.GetFile(ctx, filePath)
		if err != nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files get file failed: owner_tenant_id=%d provider=%s path=%q err=%v",
				ownerTenantID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}
		defer reader.Close()

		contentType, inline := secutils.SafeContentTypeByFilename(filePath)
		c.Header("Content-Type", contentType)
		c.Header("X-Content-Type-Options", "nosniff")
		if !inline {
			c.Header("Content-Disposition", "attachment")
		}
		// Cross-tenant shared content — keep it private so shared proxies /
		// CDNs do not cache one tenant's view for another.
		c.Header("Cache-Control", "private, max-age=86400")
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, reader); err != nil {
			logger.Warnf(ctx, "[Router] /knowledge-bases/:id/files write response failed: %v", err)
		}
	}
}

// servePresignedFiles serves files via HMAC-signed URLs without requiring authentication.
// This is used by IM channels to serve images that are embedded in bot replies.
//
// Routes:
//   - GET  /api/v1/files/presigned?file_path=<provider://...>&tenant_id=<id>&expires=<unix>&sig=<hmac>
//   - HEAD /api/v1/files/presigned?...  (IM platforms issue HEAD first to validate
//     Content-Type / Content-Length before rendering image previews; HEAD must
//     succeed or the inline image renders as broken)
//
// Failure paths log client IP + User-Agent + (truncated) file_path so operators
// can correlate an IM platform's fetch against the upstream signing log line.
// Without this it is otherwise impossible to tell whether a "broken image" is
// caused by an expired signature, a stale URL cached by the platform, the
// platform's IP being blocked, or the URL simply never reaching us.
func servePresignedFiles(r *gin.Engine, tenantService interfaces.TenantService) {
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	absDir, _ := filepath.Abs(baseDir)

	handler := presignedFileHandler(tenantService, absDir)
	r.GET("/api/v1/files/presigned", handler)
	r.HEAD("/api/v1/files/presigned", handler)
}

// presignedFileHandler returns the shared Gin handler used by both GET and HEAD.
// For HEAD requests it returns the same status + headers but does not stream
// the body — this is enough for IM platforms to validate the URL while saving
// us a full read of the backing object.
func presignedFileHandler(tenantService interfaces.TenantService, absDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()

		filePath := strings.TrimSpace(c.Query("file_path"))
		tenantIDStr := strings.TrimSpace(c.Query("tenant_id"))
		expiresStr := strings.TrimSpace(c.Query("expires"))
		sig := strings.TrimSpace(c.Query("sig"))

		if filePath == "" || tenantIDStr == "" || expiresStr == "" || sig == "" {
			logger.Warnf(ctx, "[Router] /files/presigned missing params: client_ip=%s ua=%q file_path=%q tenant_id=%q expires=%q has_sig=%v",
				clientIP, userAgent, filePath, tenantIDStr, expiresStr, sig != "")
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameters"})
			return
		}
		if strings.Contains(filePath, "..") {
			logger.Warnf(ctx, "[Router] /files/presigned rejected path traversal: client_ip=%s file_path=%q", clientIP, filePath)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
			return
		}

		tenantID, err := strconv.ParseUint(tenantIDStr, 10, 64)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned invalid tenant_id: client_ip=%s tenant_id=%q err=%v", clientIP, tenantIDStr, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id"})
			return
		}

		// Verify HMAC signature and expiry. Logged at Warn because every 403
		// here is a signal worth investigating: either the URL was tampered
		// with, the IM platform cached an expired URL, or SYSTEM_AES_KEY was
		// rotated without invalidating in-flight links.
		if !secutils.VerifyFileURLSig(filePath, tenantID, expiresStr, sig) {
			logger.Warnf(ctx, "[Router] /files/presigned sig invalid or expired: client_ip=%s ua=%q tenant_id=%d file_path=%q expires=%s",
				clientIP, userAgent, tenantID, filePath, expiresStr)
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired signature"})
			return
		}

		provider := types.ParseProviderScheme(filePath)
		tenant, err := tenantService.GetTenantByID(ctx, tenantID)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned tenant lookup failed: client_ip=%s tenant_id=%d err=%v", clientIP, tenantID, err)
			c.Status(http.StatusNotFound)
			return
		}

		fileSvc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned resolve file service failed: client_ip=%s tenant_id=%d provider=%s err=%v",
				clientIP, tenantID, provider, err)
			c.Status(http.StatusBadRequest)
			return
		}

		contentType, inline := secutils.SafeContentTypeByFilename(filePath)

		// HEAD short-circuits the body read. We still need to confirm the
		// object exists, but we use a 0-byte content length and skip io.Copy.
		// Skipping GetFile entirely for HEAD would risk reporting 200 for a
		// signed URL that no longer points at a real object; that mismatch
		// would make subsequent GETs from the same client mysteriously fail.
		reader, err := fileSvc.GetFile(ctx, filePath)
		if err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned get file failed: client_ip=%s tenant_id=%d provider=%s path=%q err=%v",
				clientIP, tenantID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}
		defer reader.Close()

		c.Header("Content-Type", contentType)
		c.Header("X-Content-Type-Options", "nosniff")
		if !inline {
			c.Header("Content-Disposition", "attachment")
		}
		c.Header("Cache-Control", "public, max-age=86400")
		if c.Request.Method == http.MethodHead {
			c.Status(http.StatusOK)
			return
		}
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, reader); err != nil {
			logger.Warnf(ctx, "[Router] /files/presigned write response failed: client_ip=%s tenant_id=%d err=%v", clientIP, tenantID, err)
		}
	}
}

// servePresignedPreview registers an Admin-only diagnostic endpoint that
// returns the presigned HTTP URL that *would be* generated for a given
// storage path by the calling tenant's current storage config — exactly the
// URL an IM channel would embed in a reply. Operators can paste the result
// into a 4G/mobile browser to verify public reachability without having to
// send a real message through an IM bot.
//
// Route:
//   - GET /api/v1/files/presigned-preview?file_path=<provider://...>
func servePresignedPreview(r *gin.Engine, cfg *config.Config) {
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	absDir, _ := filepath.Abs(baseDir)

	// This route is registered on the engine root, NOT the /api/v1 group,
	// so the APIKeyGate never runs for it. RequireRole short-circuits
	// API-key principals (deferring to that absent gate), which would let
	// any valid key past the Admin check. Deny API keys explicitly first.
	r.GET("/api/v1/files/presigned-preview",
		middleware.DenyAPIKeyPrincipal(),
		middleware.RequireRole(types.TenantRoleAdmin, cfg),
		func(c *gin.Context) {
			ctx := c.Request.Context()
			filePath := strings.TrimSpace(c.Query("file_path"))
			if filePath == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameter: file_path"})
				return
			}
			if strings.Contains(filePath, "..") {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
				return
			}

			tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
			if tenant == nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: workspace context missing"})
				return
			}

			provider := types.ParseProviderScheme(filePath)
			fileSvc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":    err.Error(),
					"provider": provider,
					"hint":     "workspace storage config is missing or incomplete for this provider",
				})
				return
			}

			httpURL, err := fileSvc.GetFileURL(ctx, filePath)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":    err.Error(),
					"provider": resolvedProvider,
					"hint":     "GetFileURL failed; for local storage this usually means APP_EXTERNAL_URL is unset",
				})
				return
			}

			// Detect the "no-op" case where local storage falls back to the
			// provider:// path because APP_EXTERNAL_URL is missing. Surfacing
			// this explicitly is the whole point of the endpoint.
			rewritten := httpURL != filePath
			hint := ""
			if !rewritten {
				hint = "URL unchanged; for local storage set APP_EXTERNAL_URL to enable presigned HTTP URLs"
			}

			c.JSON(http.StatusOK, gin.H{
				"file_path": filePath,
				"provider":  resolvedProvider,
				"url":       httpURL,
				"rewritten": rewritten,
				"hint":      hint,
			})
		})
}

// RegisterDataSourceRoutes 注册数据源相关的路由
//
// Data sources hold external service credentials (Feishu/Notion/Yuque)
// and trigger sync jobs that mutate KB content tenant-wide. Reads are
// Viewer+; everything else (CRUD, validation, sync control, credential
// subresource) is Admin+.
func RegisterDataSourceRoutes(
	r *gin.RouterGroup,
	handler *handler.DataSourceHandler,
	credHandler *handler.DataSourceCredentialsHandler,
	g *rbacGuards,
) {
	// Data source routes
	ds := g.apiKeyGroup(r.Group("/datasource"), apiKeyManageDataSources(apiKeyFullAccess()))
	{
		// Get available connector types — Viewer+
		ds.GET("/types", g.Viewer(), handler.GetAvailableConnectors)

		// Validate credentials without persistence (for "Test Connection" button) — Admin+
		ds.POST("/validate-credentials", g.Admin(), handler.ValidateCredentials)

		// CRUD operations
		ds.POST("", g.Admin(), handler.CreateDataSource)
		ds.GET("", g.Viewer(), handler.ListDataSources)
		ds.GET("/:id", g.Viewer(), handler.GetDataSource)
		ds.PUT("/:id", g.Admin(), handler.UpdateDataSource)
		ds.DELETE("/:id", g.Admin(), handler.DeleteDataSource)

		// Credential subresource. Single logical field "credentials" because
		// connector credentials are a per-connector atomic map (see
		// internal/handler/datasource_credentials.go). — Admin+
		ds.PUT("/:id/credentials", g.Admin(), credHandler.Put)
		ds.DELETE("/:id/credentials/:field", g.Admin(), credHandler.DeleteField)

		// Connection and resource management — Admin+
		ds.POST("/:id/validate", g.Admin(), handler.ValidateConnection)
		ds.GET("/:id/resources", g.Admin(), handler.ListAvailableResources)
		ds.POST("/:id/resource-ancestors", g.Admin(), handler.ResolveResourceAncestors)

		// Sync management — Admin+
		ds.POST("/:id/sync", g.Admin(), handler.ManualSync)
		ds.POST("/:id/pause", g.Admin(), handler.PauseDataSource)
		ds.POST("/:id/resume", g.Admin(), handler.ResumeDataSource)

		// Sync logs — Viewer+ (read-only audit trail)
		ds.GET("/:id/logs", g.Viewer(), handler.GetSyncLogs)
		ds.GET("/logs/:log_id", g.Viewer(), handler.GetSyncLog)
	}
}

// RegisterWeKnoraCloudRoutes 注册 WeKnoraCloud 初始化路由
// RegisterWeKnoraCloudRoutes registers the WeKnoraCloud credential
// management endpoints. SaveCredentials persists external SaaS keys
// for the tenant (Admin+), Status is a low-risk readiness probe (Viewer+).
func RegisterWeKnoraCloudRoutes(r *gin.RouterGroup, handler *handler.WeKnoraCloudHandler, g *rbacGuards) {
	g.apiKeyRoute(r, http.MethodPost, "/weknoracloud/credentials", apiKeyManageModels(apiKeyFullAccess()), g.Admin(), handler.SaveCredentials)
	g.apiKeyRoute(r, http.MethodGet, "/models/weknoracloud/status", apiKeyManageModels(apiKeyFullAccess()), g.Viewer(), handler.Status)
}

// RegisterWikiPageRoutes registers wiki page related routes.
//
// Wiki pages are KB content (wiki mode): reads are Viewer+ and gated by
// KBAccessRead (own / org-shared / via shared agent), matching FAQ /
// chunk / tag read routes. Content mutations (create/update/delete) and
// maintenance actions (rebuild-links, auto-fix, change issue status)
// honour per-KB ownership via OwnedWikiKBOrAdmin (PR 5, #1303): the URL
// :kb_id resolves directly to the owning KB so a Contributor who owns
// the KB can manage its wiki, while a non-owner Contributor gets 403.
func RegisterWikiPageRoutes(r *gin.RouterGroup, wikiHandler *handler.WikiPageHandler, g *rbacGuards) {
	wiki := g.apiKeyGroup(r.Group("/knowledgebase/:kb_id/wiki"), apiKeyIngest(apiKeyFullAccess()))
	wikiRead := wiki.With(apiKeyRetrieve(apiKeyFullAccess()))
	{
		// Page CRUD
		wikiRead.GET("/pages", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.ListPages)
		wiki.POST("/pages", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.CreatePage)
		wiki.PUT("/move-page", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.MovePage)
		wikiRead.GET("/pages/*slug", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.GetPage)
		wiki.PUT("/pages/*slug", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.UpdatePage)
		wiki.DELETE("/pages/*slug", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.DeletePage)

		// Folder tree (directory nodes)
		wikiRead.GET("/folders", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.ListFolders)
		wiki.POST("/folders", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.CreateFolder)
		wiki.PUT("/folders/:folder_id", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.UpdateFolder)
		wiki.DELETE("/folders/:folder_id", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.DeleteFolder)

		// Special pages
		wikiRead.GET("/index", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.GetIndex)
		wikiRead.GET("/log", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.GetLog)

		// Graph and stats
		wikiRead.GET("/graph", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.GetGraph)
		wikiRead.GET("/stats", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.GetStats)

		// Search and maintenance
		wikiRead.GET("/search", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.SearchPages)
		wiki.POST("/rebuild-links", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.RebuildLinks)
		wikiRead.GET("/lint", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.Lint)
		wiki.POST("/auto-fix", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.AutoFix)

		// Issues
		wikiRead.GET("/issues", g.Viewer(), g.KBAccessRead("kb_id"), wikiHandler.ListIssues)
		wiki.PUT("/issues/:issue_id/status", g.OwnedWikiKBOrAdmin(), g.KBAccessWrite("kb_id"), wikiHandler.UpdateIssueStatus)
	}
}
