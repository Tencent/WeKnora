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
	TenantMemberService          interfaces.TenantMemberService
	TenantMemberHandler          *handler.TenantMemberHandler
	TenantInvitationHandler      *handler.TenantInvitationHandler
	AuditLogHandler              *handler.AuditLogHandler
	AuditLogService              interfaces.AuditLogService
	ChunkHandler                 *handler.ChunkHandler
	SessionHandler               *session.Handler
	MessageHandler               *handler.MessageHandler
	ModelHandler                 *handler.ModelHandler
	ModelCredentialsHandler      *handler.ModelCredentialsHandler
	EvaluationHandler            *handler.EvaluationHandler
	AuthHandler                  *handler.AuthHandler
	InitializationHandler        *handler.InitializationHandler
	SystemHandler                *handler.SystemHandler
	MCPServiceHandler            *handler.MCPServiceHandler
	MCPCredentialsHandler        *handler.MCPCredentialsHandler
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
	DataSourceHandler            *handler.DataSourceHandler
	DataSourceCredentialsHandler *handler.DataSourceCredentialsHandler
	WeKnoraCloudHandler          *handler.WeKnoraCloudHandler
	WikiPageHandler              *handler.WikiPageHandler
}

// NewRouter 创建新的路由
func NewRouter(params RouterParams) *gin.Engine {
	r := gin.New()
	r.ContextWithFallback = true

	// CORS 中间件应放在最前面
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", "X-Request-ID"},
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

	// 前端静态文件（仅 Lite 版本内嵌前端）
	if handler.Edition == "lite" {
		serveFrontendStatic(r)
	}

	// IM 回调路由（在认证中间件之前注册，使用各平台自身的签名验证）
	RegisterIMRoutes(r, params.IMHandler)

	// 认证中间件
	r.Use(middleware.Auth(params.TenantService, params.UserService, params.TenantMemberService, params.Config))

	// 文件服务：统一代理本地/MinIO/COS/TOS存储后端（需要认证）
	serveFiles(r, params.FileService)

	// Presigned file access: no auth required, signature-verified.
	servePresignedFiles(r, params.TenantService)

	// 添加OpenTelemetry追踪中间件
	// r.Use(middleware.TracingMiddleware())

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

		RegisterAuthRoutes(v1, params.AuthHandler)
		RegisterTenantRoutes(v1, params.TenantHandler, params.TenantMemberHandler, params.TenantInvitationHandler, params.AuditLogHandler, rbacGuards)
		RegisterMyInvitationRoutes(v1, params.TenantInvitationHandler)
		RegisterKnowledgeBaseRoutes(v1, params.KBHandler, rbacGuards)
		RegisterKnowledgeTagRoutes(v1, params.TagHandler, rbacGuards)
		RegisterKnowledgeRoutes(v1, params.KnowledgeHandler, rbacGuards)
		RegisterFAQRoutes(v1, params.FAQHandler, rbacGuards)
		RegisterChunkRoutes(v1, params.ChunkHandler, rbacGuards)
		RegisterSessionRoutes(v1, params.SessionHandler, rbacGuards)
		RegisterChatRoutes(v1, params.SessionHandler, rbacGuards)
		RegisterMessageRoutes(v1, params.MessageHandler, rbacGuards)
		RegisterModelRoutes(v1, params.ModelHandler, params.ModelCredentialsHandler, rbacGuards)
		RegisterEvaluationRoutes(v1, params.EvaluationHandler, rbacGuards)
		RegisterInitializationRoutes(v1, params.InitializationHandler, rbacGuards)
		RegisterSystemRoutes(v1, params.SystemHandler, rbacGuards)
		RegisterSystemAdminRoutes(v1, params.SystemHandler, rbacGuards)
		RegisterMCPServiceRoutes(v1, params.MCPServiceHandler, params.MCPCredentialsHandler, rbacGuards)
		RegisterWebSearchRoutes(v1, params.WebSearchHandler, rbacGuards)
		RegisterWebSearchProviderRoutes(v1, params.WebSearchProviderHandler, params.WebSearchCredentialsHandler, rbacGuards)
		RegisterVectorStoreRoutes(v1, params.VectorStoreHandler, rbacGuards)
		RegisterCustomAgentRoutes(v1, params.CustomAgentHandler, rbacGuards)
		RegisterUserFavoriteRoutes(v1, params.UserFavoriteHandler, rbacGuards)
		RegisterSkillRoutes(v1, params.SkillHandler, rbacGuards)
		RegisterOrganizationRoutes(v1, params.OrganizationHandler, rbacGuards)
		RegisterIMChannelRoutes(v1, params.IMHandler, rbacGuards)
		RegisterDataSourceRoutes(v1, params.DataSourceHandler, params.DataSourceCredentialsHandler, rbacGuards)
		RegisterWeKnoraCloudRoutes(v1, params.WeKnoraCloudHandler, rbacGuards)
		RegisterWikiPageRoutes(v1, params.WikiPageHandler, rbacGuards)
		RegisterChunkerDebugRoutes(v1, rbacGuards)
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
	r.POST("/chunker/preview", g.Viewer(), handler.PreviewChunking)
}

// RegisterChunkRoutes 注册分块相关的路由
//
// Mutating routes addressed via :knowledge_id inherit per-KB ownership
// from the owning knowledge entry's KB (PR 5, #1303); the chain hop is
// shared with RegisterKnowledgeRoutes via OwnedChunkKBOrAdmin so the
// same "creator-of-the-KB OR Admin+" rule applies to chunk edits.
func RegisterChunkRoutes(r *gin.RouterGroup, handler *handler.ChunkHandler, g *rbacGuards) {
	// 分块路由组
	chunks := r.Group("/chunks")
	{
		// 获取分块列表 — Viewer+ 且对父 KB 有 read 权限（own / shared / via shared agent）
		chunks.GET("/:knowledge_id", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("knowledge_id"), handler.ListKnowledgeChunks)
		// 通过chunk_id获取单个chunk（不需要knowledge_id） — Viewer+ 且对父 KB 有 read 权限
		chunks.GET("/by-id/:id", g.Viewer(), g.KBAccessReadFromChunkIDParam("id"), handler.GetChunkByIDOnly)
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
	// 知识库下的知识路由组（URL :id is the KB id）
	kb := r.Group("/knowledge-bases/:id/knowledge")
	{
		kb.POST("/file", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateKnowledgeFromFile)
		kb.POST("/url", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateKnowledgeFromURL)
		kb.POST("/manual", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateManualKnowledge)
		kb.GET("", g.Viewer(), g.KBAccessRead("id"), handler.ListKnowledge)
		// Clearing all contents under a KB is a destructive op; gate
		// behind Admin instead of Contributor.
		kb.DELETE("", g.Admin(), g.KBAccessWrite("id"), handler.ClearKnowledgeBaseContents)
	}

	// 知识路由组（URL :id is a knowledge id; the guard walks it to the parent KB）
	k := r.Group("/knowledge")
	{
		// Cross-knowledge endpoints (no :id) can't be gated on a single
		// KB — they accept arbitrary knowledge IDs and the handler must
		// fan out the access check itself. So /batch and /search keep
		// the role-only floor; /move and /batch-delete stay Contributor.
		k.GET("/batch", g.Viewer(), handler.GetKnowledgeBatch)
		k.GET("/:id", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.GetKnowledge)
		k.DELETE("/:id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.DeleteKnowledge)
		k.PUT("/:id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.UpdateKnowledge)
		k.PUT("/manual/:id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.UpdateManualKnowledge)
		k.POST("/:id/reparse", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.ReparseKnowledge)
		k.GET("/:id/download", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.DownloadKnowledgeFile)
		k.GET("/:id/preview", g.Viewer(), g.KBAccessReadFromKnowledgeIDParam("id"), handler.PreviewKnowledgeFile)
		k.PUT("/image/:id/:chunk_id", g.OwnedKnowledgeKBOrAdmin(), g.KBAccessWriteFromKnowledgeIDParam("id"), handler.UpdateImageInfo)
		// Batch / cross-KB ops stay Contributor-gated: there is no
		// single owning KB to walk back to. A future PR could add a
		// "must own every targeted KB" guard if the requirement
		// surfaces.
		k.PUT("/tags", g.Contributor(), handler.UpdateKnowledgeTagBatch)
		k.GET("/search", g.Viewer(), handler.SearchKnowledge)
		k.POST("/batch-delete", g.Contributor(), handler.BatchDeleteKnowledge)
		k.POST("/move", g.Contributor(), handler.MoveKnowledge)
		k.GET("/move/progress/:task_id", g.Viewer(), handler.GetKnowledgeMoveProgress)
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
	faq := r.Group("/knowledge-bases/:id/faq")
	{
		// KBAccessRead/Write resolve own/shared/agent-visible access and
		// rewrite the request's tenant context — handler no longer
		// carries an effectiveCtxForKB helper.
		faq.GET("/entries", g.Viewer(), g.KBAccessRead("id"), handler.ListEntries)
		faq.GET("/entries/export", g.Viewer(), g.KBAccessRead("id"), handler.ExportEntries)
		faq.GET("/entries/:entry_id", g.Viewer(), g.KBAccessRead("id"), handler.GetEntry)
		faq.POST("/entries", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpsertEntries)
		faq.POST("/entry", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateEntry)
		faq.PUT("/entries/:entry_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateEntry)
		faq.POST("/entries/:entry_id/similar-questions", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.AddSimilarQuestions)
		// Unified batch update API - supports is_enabled, is_recommended, tag_id
		faq.PUT("/entries/fields", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateEntryFieldsBatch)
		faq.PUT("/entries/tags", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateEntryTagBatch)
		faq.DELETE("/entries", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.DeleteEntries)
		faq.POST("/search", g.Viewer(), g.KBAccessRead("id"), handler.SearchFAQ)
		// FAQ import result display status
		faq.PUT("/import/last-result/display", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateLastImportResultDisplayStatus)
	}
	// FAQ import progress route (outside of knowledge-base scope) — Viewer+
	faqImport := r.Group("/faq/import")
	{
		faqImport.GET("/progress/:task_id", g.Viewer(), handler.GetImportProgress)
	}
}

// RegisterKnowledgeBaseRoutes 注册知识库相关的路由
func RegisterKnowledgeBaseRoutes(r *gin.RouterGroup, handler *handler.KnowledgeBaseHandler, g *rbacGuards) {
	// 知识库路由组
	kb := r.Group("/knowledge-bases")
	{
		// 创建知识库 — Contributor+ (no :id, role-only floor)
		kb.POST("", g.Contributor(), handler.CreateKnowledgeBase)
		// 获取知识库列表 — Viewer+ (no :id, role-only floor)
		kb.GET("", g.Viewer(), handler.ListKnowledgeBases)
		// 获取知识库详情 — Viewer+ 且对 KB 有 read 权限
		kb.GET("/:id", g.Viewer(), g.KBAccessRead("id"), handler.GetKnowledgeBase)
		// 更新知识库 — 创建者本人 OR Admin+ 且对 KB 有 write 权限
		kb.PUT("/:id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateKnowledgeBase)
		// 删除知识库 — 创建者本人 OR Admin+ 且对 KB 有 write 权限
		kb.DELETE("/:id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.DeleteKnowledgeBase)
		// 置顶/取消置顶知识库 — 创建者本人 OR Admin+ 且对 KB 有 write 权限
		// Pin state is now per-(user, kb) (migration 000050). Anyone with
		// at least Viewer-level read access to the KB — including users
		// who reached it via a shared agent — may pin it for themselves;
		// no edit permission is required. The OwnedKBOrAdmin guard was
		// removed accordingly. The route still requires KB read access
		// so callers can't poke at KBs they can't see.
		kb.PUT("/:id/pin", g.Viewer(), g.KBAccessRead("id"), handler.TogglePinKnowledgeBase)
		// 混合搜索 — Viewer+ 且对 KB 有 read 权限 (read-only)
		kb.GET("/:id/hybrid-search", g.Viewer(), g.KBAccessRead("id"), handler.HybridSearch)
		// 拷贝知识库 — Contributor+ (副本归调用者所有；不需要原 KB 的所有权)
		kb.POST("/copy", g.Contributor(), handler.CopyKnowledgeBase)
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
	kbTags := r.Group("/knowledge-bases/:id/tags")
	{
		// KBAccessRead/Write resolve own/shared/agent-visible access and
		// rewrite the request's tenant context to the effective tenant
		// for the duration of the handler — so the handler no longer
		// needs its own effectiveCtxForKB helper.
		kbTags.GET("", g.Viewer(), g.KBAccessRead("id"), tagHandler.ListTags)
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
	messages := r.Group("/messages")
	{
		messages.POST("/search", g.Viewer(), handler.SearchMessages)
		messages.GET("/chat-history-stats", g.Viewer(), handler.GetChatHistoryKBStats)
		messages.GET("/:session_id/load", g.Viewer(), handler.LoadMessages)
		messages.DELETE("/:session_id/:id", g.Viewer(), handler.DeleteMessage)
	}
}

// RegisterSessionRoutes 注册路由。
//
// Sessions are per-user resources; the handler enforces user ownership.
// We gate at Viewer+ to keep non-members out once RBAC is on, matching
// the message routes above. A future refactor can introduce
// per-session ownership in the middleware layer the same way KB/agent
// routes do today.
func RegisterSessionRoutes(r *gin.RouterGroup, handler *session.Handler, g *rbacGuards) {
	sessions := r.Group("/sessions", g.Viewer())
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
	}
}

// RegisterChatRoutes 注册路由。Chat endpoints are tenant-member usage
// surfaces; Viewer+ is sufficient because per-session/per-agent
// authorisation is enforced inside the handlers.
func RegisterChatRoutes(r *gin.RouterGroup, handler *session.Handler, g *rbacGuards) {
	knowledgeChat := r.Group("/knowledge-chat", g.Viewer())
	{
		knowledgeChat.POST("/:session_id", handler.KnowledgeQA)
	}

	// Agent-based chat
	agentChat := r.Group("/agent-chat", g.Viewer())
	{
		agentChat.POST("/:session_id", handler.AgentQA)
	}

	// 新增知识检索接口，不需要session_id
	knowledgeSearch := r.Group("/knowledge-search", g.Viewer())
	{
		knowledgeSearch.POST("", handler.SearchKnowledge)
	}
}

// RegisterTenantRoutes 注册租户相关的路由
//
// Tenant-internal RBAC for /tenants/:id:
//   - GET   /:id          Viewer+ (read tenant settings)
//   - PUT   /:id          Owner+ (mutate tenant config)
//   - DELETE /:id         Owner+ (also normally a CanAccessAllTenants op)
//   - POST  /:id/api-key  Owner+ (rotating the tenant API key is sensitive)
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

	// 租户路由组
	tenantRoutes := r.Group("/tenants")
	{
		// 创建租户对所有已登录用户开放：用户可以为自己再开一个工作区，
		// handler 内部会调 EnsureOwner 把调用者写成新租户的 Owner。
		// 跨租户超管走同一个端点，但能携带 storage_quota / status 等
		// 全字段（见 handler.CreateTenant 内部分支）。
		// 安全说明：这里不挂 g.CrossTenant()，因为 self-service 创建
		// 不需要跨租户特权；handler 也不读写 X-Tenant-ID 指向的现有
		// 租户，所以越过 PathTenantMatch 守卫不会扩大攻击面。
		tenantRoutes.POST("", handler.CreateTenant)
		tenantRoutes.GET("", handler.ListTenants)

		// Generic KV configuration management (tenant-level). Tenant ID
		// is obtained from authentication context; the URL :key is a
		// config key, not a tenant ID, so these stay outside the
		// PathTenantMatch group.
		tenantRoutes.GET("/kv/:key", g.Viewer(), handler.GetTenantKV)
		tenantRoutes.PUT("/kv/:key", g.Admin(), handler.UpdateTenantKV)

		// Per-tenant endpoints share PathTenantMatch at the group level.
		tenantByID := tenantRoutes.Group("/:id", g.PathTenantMatch())
		{
			tenantByID.GET("", g.Viewer(), handler.GetTenant)
			tenantByID.PUT("", g.Owner(), handler.UpdateTenant)
			tenantByID.DELETE("", g.Owner(), handler.DeleteTenant)
			tenantByID.POST("/api-key", g.Owner(), handler.ResetAPIKey)

			// Tenant member management (PR 3 of #1303). Listing is
			// Viewer+ so any active member can see the roster; mutation
			// is Owner+ because membership changes are the highest-impact
			// tenant op. /:id/leave is Viewer+ — any member can quit on
			// their own; the service still rejects when it would leave
			// the tenant without an Owner.
			if memberHandler != nil {
				tenantByID.GET("/members", g.Viewer(), memberHandler.ListMembers)
				tenantByID.POST("/members", g.Owner(), memberHandler.AddMember)
				tenantByID.PUT("/members/:user_id", g.Owner(), memberHandler.UpdateMemberRole)
				tenantByID.DELETE("/members/:user_id", g.Owner(), memberHandler.RemoveMember)
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
				tenantByID.GET("/invitations", g.Viewer(), invitationHandler.ListTenantInvitations)
				tenantByID.POST("/invitations", g.Owner(), invitationHandler.CreateInvitation)
				tenantByID.DELETE("/invitations/:inv_id", g.Owner(), invitationHandler.RevokeInvitation)
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
	// 模型路由组
	models := r.Group("/models")
	{
		// 获取模型厂商列表 — Viewer+
		models.GET("/providers", g.Viewer(), handler.ListModelProviders)
		// 创建模型 — Admin+
		models.POST("", g.Admin(), handler.CreateModel)
		// 获取模型列表 — Viewer+
		models.GET("", g.Viewer(), handler.ListModels)
		// 获取单个模型 — Viewer+
		models.GET("/:id", g.Viewer(), handler.GetModel)
		// 更新模型 — Admin+
		models.PUT("/:id", g.Admin(), handler.UpdateModel)
		// 删除模型 — Admin+
		models.DELETE("/:id", g.Admin(), handler.DeleteModel)
		// Per-field credential subresource (see internal/handler/model_credentials.go) — Admin+
		models.PUT("/:id/credentials", g.Admin(), credHandler.Put)
		models.DELETE("/:id/credentials/:field", g.Admin(), credHandler.DeleteField)
	}
}

// RegisterEvaluationRoutes registers evaluation endpoints. Running an
// evaluation drives LLM calls (cost) and reads from KBs across the
// tenant; gate to Admin+ until product asks for a finer-grained
// matrix.
func RegisterEvaluationRoutes(r *gin.RouterGroup, handler *handler.EvaluationHandler, g *rbacGuards) {
	evaluationRoutes := r.Group("/evaluation")
	{
		evaluationRoutes.POST("/", g.Admin(), handler.Evaluation)
		evaluationRoutes.GET("/", g.Viewer(), handler.GetEvaluationResult)
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
func RegisterAuthRoutes(r *gin.RouterGroup, handler *handler.AuthHandler) {
	r.POST("/auth/register", handler.Register)
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
	r.GET("/auth/me", handler.GetCurrentUser)
	r.PUT("/auth/me/preferences", handler.UpdateMyPreferences)
	r.POST("/auth/change-password", handler.ChangePassword)
}

func RegisterInitializationRoutes(r *gin.RouterGroup, handler *handler.InitializationHandler, g *rbacGuards) {
	// 初始化接口
	// GetCurrentConfigByKB 是只读，Viewer+ 即可。
	r.GET("/initialization/config/:kbId", g.Viewer(), handler.GetCurrentConfigByKB)
	// InitializeByKB / UpdateKBConfig 都是改 KB 的核心模型/storage 配置 —
	// 跟 PUT /knowledge-bases/:id 同等敏感，挂同款 OwnedKB 矩阵。
	r.POST("/initialization/initialize/:kbId", g.OwnedKBOrAdminFromKbIDParam(), handler.InitializeByKB)
	r.PUT("/initialization/config/:kbId", g.OwnedKBOrAdminFromKbIDParam(), handler.UpdateKBConfig)

	// Ollama / 远程 API / 抽取等系统级检测/下载操作。这些不绑某个 KB，
	// 但会改租户级模型配置或拉远端模型 — 一律 Admin+。Viewer+ 的检测
	// 入口已经在 settings 页面隐藏，但服务端仍要兜底。
	r.GET("/initialization/ollama/status", g.Viewer(), handler.CheckOllamaStatus)
	r.GET("/initialization/ollama/models", g.Viewer(), handler.ListOllamaModels)
	r.POST("/initialization/ollama/models/check", g.Admin(), handler.CheckOllamaModels)
	r.POST("/initialization/ollama/models/download", g.Admin(), handler.DownloadOllamaModel)
	r.GET("/initialization/ollama/download/progress/:taskId", g.Viewer(), handler.GetDownloadProgress)
	r.GET("/initialization/ollama/download/tasks", g.Viewer(), handler.ListDownloadTasks)

	// 远程API相关接口
	r.POST("/initialization/remote/check", g.Admin(), handler.CheckRemoteModel)
	r.POST("/initialization/embedding/test", g.Admin(), handler.TestEmbeddingModel)
	r.POST("/initialization/rerank/check", g.Admin(), handler.CheckRerankModel)
	r.POST("/initialization/asr/check", g.Admin(), handler.CheckASRModel)
	r.POST("/initialization/multimodal/test", g.Admin(), handler.TestMultimodalFunction)

	r.POST("/initialization/extract/text-relation", g.Admin(), handler.ExtractTextRelations)
	r.POST("/initialization/extract/fabri-tag", g.Admin(), handler.FabriTag)
	r.POST("/initialization/extract/fabri-text", g.Admin(), handler.FabriText)
}

// RegisterSystemRoutes registers system information routes
//
// Reads (GetSystemInfo / ListParserEngines / GetStorageEngineStatus)
// are gated to Viewer+ — any tenant member can see "is the parser
// reachable". The /*-check / /reconnect endpoints actively probe
// remote services with tenant credentials and could trigger network
// fanout, so they're Admin+.
func RegisterSystemRoutes(r *gin.RouterGroup, handler *handler.SystemHandler, g *rbacGuards) {
	systemRoutes := r.Group("/system")
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
func RegisterSystemAdminRoutes(r *gin.RouterGroup, handler *handler.SystemHandler, g *rbacGuards) {
	// Apply SystemAdmin() at the group level — every route below inherits
	// the guard, so adding new endpoints can't accidentally drop the gate.
	adminRoutes := r.Group("/system/admin", g.SystemAdmin())
	{
		// P0: SystemAdmin role management
		adminRoutes.POST("/promote", handler.PromoteUserToSystemAdmin)
		adminRoutes.POST("/revoke", handler.RevokeSystemAdmin)
		adminRoutes.GET("/list", handler.ListSystemAdmins)

		// P1: platform-wide system settings (DB-backed runtime tunables).
		// Reads return raw model rows / arrays (no `gin.H{"data":...}`
		// wrapping), matching the project's axios interceptor convention
		// — see frontend/src/utils/request.ts:97.
		adminRoutes.GET("/settings", handler.ListSystemSettings)
		adminRoutes.GET("/settings/:key", handler.GetSystemSetting)
		adminRoutes.PUT("/settings/:key", handler.UpdateSystemSetting)
		adminRoutes.DELETE("/settings/:key", handler.ResetSystemSetting)

		// Bulk action — write the current default-quota setting onto
		// every existing tenant. Lives under /tenants instead of
		// /settings because it changes tenants, not the setting row.
		adminRoutes.POST(
			"/tenants/apply-default-storage-quota",
			handler.ApplyDefaultStorageQuotaToAllTenants,
		)
	}
}

func RegisterMCPServiceRoutes(
	r *gin.RouterGroup,
	handler *handler.MCPServiceHandler,
	credHandler *handler.MCPCredentialsHandler,
	g *rbacGuards,
) {
	mcpServices := r.Group("/mcp-services")
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
	}

	agentTool := r.Group("/agent")
	{
		// Resolving a pending tool-approval is gated to tenant members
		// (Viewer+). The approval card surfaces inside an agent chat the
		// caller initiated — restricting it to Admin+ blocks the only
		// people who actually have context to approve, so the gate is
		// kept at "anyone in the tenant" instead.
		agentTool.POST("/tool-approvals/:pending_id", g.Viewer(), handler.ResolveToolApproval)
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
	providers := r.Group("/web-search-providers")
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
	stores := r.Group("/vector-stores")
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
	agents := r.Group("/agents")
	{
		// Get placeholder definitions (must be before /:id to avoid conflict) — Viewer+
		agents.GET("/placeholders", g.Viewer(), agentHandler.GetPlaceholders)
		// List smart-reasoning agent type presets (rag-qa / wiki-qa / hybrid / custom) — Viewer+
		agents.GET("/type-presets", g.Viewer(), agentHandler.GetAgentTypePresets)
		// Create custom agent — Contributor+
		agents.POST("", g.Contributor(), agentHandler.CreateAgent)
		// List all agents (including built-in) — Viewer+
		agents.GET("", g.Viewer(), agentHandler.ListAgents)
		// Get agent by ID — Viewer+
		agents.GET("/:id", g.Viewer(), agentHandler.GetAgent)
		// Update agent — creator OR Admin+
		agents.PUT("/:id", g.OwnedAgentOrAdmin(), agentHandler.UpdateAgent)
		// Delete agent — creator OR Admin+
		agents.DELETE("/:id", g.OwnedAgentOrAdmin(), agentHandler.DeleteAgent)
		// Copy agent — Contributor+ (copy is owned by the caller)
		agents.POST("/:id/copy", g.Contributor(), agentHandler.CopyAgent)
	}
	// Registered outside the group to avoid Gin route conflict with /agents/:id/shares in organization routes
	r.GET("/agents/:id/suggested-questions", g.Viewer(), agentHandler.GetSuggestedQuestions)
}

// RegisterUserFavoriteRoutes wires the per-user starred-resource endpoints.
//
// Authorization: the handler always derives (user_id, tenant_id) from the
// auth context — there is no admin-style "see another user's favorites"
// path — so a Viewer floor is the right gate. The endpoints intentionally
// don't follow the OwnedXOrAdmin pattern: favorites aren't owned by the
// resource's creator, they're owned by the user *doing* the starring.
func RegisterUserFavoriteRoutes(r *gin.RouterGroup, h *handler.UserResourceFavoriteHandler, g *rbacGuards) {
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
	orgs := r.Group("/organizations")
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
	// 同等敏感，所以挂同款 OwnedKBOrAdmin 矩阵。Viewer 在自己租户里
	// 也不能私自把 KB 暴露出去。
	kbShares := r.Group("/knowledge-bases/:id/shares")
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
	// GET 同样走 OwnedAgentOrAdmin：service.ListSharesByAgent 没有 owner
	// 校验（与 ListSharesByKnowledgeBase 不对称），如果路由层只挂 Viewer
	// 任何同租户成员都能枚举他人 agent 的分享去向——这里把 owner 校验
	// 兜底到路由层，匹配 POST/DELETE 的矩阵。
	agentShares := r.Group("/agents/:id/shares")
	{
		agentShares.POST("", g.OwnedAgentOrAdmin(), orgHandler.ShareAgent)
		agentShares.GET("", g.OwnedAgentOrAdmin(), orgHandler.ListAgentShares)
		agentShares.DELETE("/:share_id", g.OwnedAgentOrAdmin(), orgHandler.RemoveAgentShare)
	}

	// Shared knowledge bases route — Viewer+
	r.GET("/shared-knowledge-bases", g.Viewer(), orgHandler.ListSharedKnowledgeBases)
	// Shared agents route — Viewer+
	r.GET("/shared-agents", g.Viewer(), orgHandler.ListSharedAgents)
	// "Disable by me" 是租户级偏好（写到 tenant_disabled_shared_agents），
	// 影响整个租户在会话下拉里看到的 agent 列表。任何 Viewer 改这个表就
	// 等于替整个租户做决定 — 必须 Admin+ 才允许调整。
	r.POST("/shared-agents/disabled", g.Admin(), orgHandler.SetSharedAgentDisabledByMe)
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
	agentChannels := r.Group("/agents/:id/im-channels")
	{
		agentChannels.POST("", g.Admin(), imHandler.CreateIMChannel)
		agentChannels.GET("", g.Viewer(), imHandler.ListIMChannels)
	}

	// Channel operations by channel ID
	channels := r.Group("/im-channels")
	{
		channels.GET("", g.Viewer(), imHandler.ListAllIMChannels)
		channels.PUT("/:id", g.Admin(), imHandler.UpdateIMChannel)
		channels.DELETE("/:id", g.Admin(), imHandler.DeleteIMChannel)
		channels.POST("/:id/toggle", g.Admin(), imHandler.ToggleIMChannel)
	}

	// WeChat QR code login (requires authentication) — Admin+: a successful
	// scan binds a personal WeChat account to the tenant.
	wechatGroup := r.Group("/wechat")
	{
		wechatGroup.POST("/qrcode", g.Admin(), imHandler.WeChatGetQRCode)
		wechatGroup.POST("/qrcode/status", g.Admin(), imHandler.WeChatPollQRCodeStatus)
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

func serveFiles(r getRouteRegistrar, globalFileService interfaces.FileService) {
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

	logger.Infof(context.Background(), "[Router] Serving files from /files (local base: %s)", absDir)

	r.GET("/files", func(c *gin.Context) {
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: tenant context missing"})
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

		ext := filepath.Ext(filePath)
		contentType := "application/octet-stream"
		switch strings.ToLower(ext) {
		case ".png":
			contentType = "image/png"
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		case ".bmp":
			contentType = "image/bmp"
		case ".svg":
			contentType = "image/svg+xml"
		case ".pdf":
			contentType = "application/pdf"
		case ".csv":
			contentType = "text/csv; charset=utf-8"
		}

		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=86400")
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, reader); err != nil {
			logger.Warnf(context.Background(), "[Router] /files write response failed: %v", err)
		}
	})
}

// servePresignedFiles serves files via HMAC-signed URLs without requiring authentication.
// This is used by IM channels to serve images that are embedded in bot replies.
//
// Route:
//   - /api/v1/files/presigned?file_path=<provider://...>&tenant_id=<id>&expires=<unix>&sig=<hmac>
func servePresignedFiles(r *gin.Engine, tenantService interfaces.TenantService) {
	baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
	if baseDir == "" {
		baseDir = "/data/files"
	}
	absDir, _ := filepath.Abs(baseDir)

	r.GET("/api/v1/files/presigned", func(c *gin.Context) {
		filePath := strings.TrimSpace(c.Query("file_path"))
		tenantIDStr := strings.TrimSpace(c.Query("tenant_id"))
		expiresStr := strings.TrimSpace(c.Query("expires"))
		sig := strings.TrimSpace(c.Query("sig"))

		if filePath == "" || tenantIDStr == "" || expiresStr == "" || sig == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameters"})
			return
		}
		if strings.Contains(filePath, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
			return
		}

		tenantID, err := strconv.ParseUint(tenantIDStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id"})
			return
		}

		// Verify HMAC signature and expiry.
		if !secutils.VerifyFileURLSig(filePath, tenantID, expiresStr, sig) {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired signature"})
			return
		}

		// Resolve the file service for this tenant.
		provider := types.ParseProviderScheme(filePath)
		tenant, err := tenantService.GetTenantByID(c.Request.Context(), tenantID)
		if err != nil {
			logger.Warnf(context.Background(), "[Router] /files/presigned tenant lookup failed: tenant_id=%d err=%v", tenantID, err)
			c.Status(http.StatusNotFound)
			return
		}

		fileSvc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, absDir)
		if err != nil {
			logger.Warnf(context.Background(), "[Router] /files/presigned resolve file service failed: tenant_id=%d provider=%s err=%v", tenantID, provider, err)
			c.Status(http.StatusBadRequest)
			return
		}

		reader, err := fileSvc.GetFile(c.Request.Context(), filePath)
		if err != nil {
			logger.Warnf(context.Background(), "[Router] /files/presigned get file failed: tenant_id=%d provider=%s path=%q err=%v", tenantID, resolvedProvider, filePath, err)
			c.Status(http.StatusNotFound)
			return
		}
		defer reader.Close()

		ext := filepath.Ext(filePath)
		contentType := "application/octet-stream"
		switch strings.ToLower(ext) {
		case ".png":
			contentType = "image/png"
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		case ".bmp":
			contentType = "image/bmp"
		case ".svg":
			contentType = "image/svg+xml"
		case ".pdf":
			contentType = "application/pdf"
		}

		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=86400")
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, reader); err != nil {
			logger.Warnf(context.Background(), "[Router] /files/presigned write response failed: %v", err)
		}
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
	ds := r.Group("/datasource")
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
	r.POST("/weknoracloud/credentials", g.Admin(), handler.SaveCredentials)
	r.GET("/models/weknoracloud/status", g.Viewer(), handler.Status)
}

// RegisterWikiPageRoutes registers wiki page related routes.
//
// Wiki pages are KB content (wiki mode): reads are Viewer+, content
// mutations (create/update/delete) and maintenance actions
// (rebuild-links, auto-fix, change issue status) honour per-KB
// ownership via OwnedWikiKBOrAdmin (PR 5, #1303): the URL :kb_id
// resolves directly to the owning KB so a Contributor who owns the KB
// can manage its wiki, while a non-owner Contributor gets 403.
func RegisterWikiPageRoutes(r *gin.RouterGroup, wikiHandler *handler.WikiPageHandler, g *rbacGuards) {
	wiki := r.Group("/knowledgebase/:kb_id/wiki")
	{
		// Page CRUD
		wiki.GET("/pages", g.Viewer(), wikiHandler.ListPages)
		wiki.POST("/pages", g.OwnedWikiKBOrAdmin(), wikiHandler.CreatePage)
		wiki.GET("/pages/*slug", g.Viewer(), wikiHandler.GetPage)
		wiki.PUT("/pages/*slug", g.OwnedWikiKBOrAdmin(), wikiHandler.UpdatePage)
		wiki.DELETE("/pages/*slug", g.OwnedWikiKBOrAdmin(), wikiHandler.DeletePage)

		// Special pages
		wiki.GET("/index", g.Viewer(), wikiHandler.GetIndex)
		wiki.GET("/log", g.Viewer(), wikiHandler.GetLog)

		// Graph and stats
		wiki.GET("/graph", g.Viewer(), wikiHandler.GetGraph)
		wiki.GET("/stats", g.Viewer(), wikiHandler.GetStats)

		// Search and maintenance
		wiki.GET("/search", g.Viewer(), wikiHandler.SearchPages)
		wiki.POST("/rebuild-links", g.OwnedWikiKBOrAdmin(), wikiHandler.RebuildLinks)
		wiki.GET("/lint", g.Viewer(), wikiHandler.Lint)
		wiki.POST("/auto-fix", g.OwnedWikiKBOrAdmin(), wikiHandler.AutoFix)

		// Issues
		wiki.GET("/issues", g.Viewer(), wikiHandler.ListIssues)
		wiki.PUT("/issues/:issue_id/status", g.OwnedWikiKBOrAdmin(), wikiHandler.UpdateIssueStatus)
	}
}
