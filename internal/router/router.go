package router

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// RouterParams 路由参数
type RouterParams struct {
	dig.In

	Config                *config.Config
	UserService           interfaces.UserService
	KBService             interfaces.KnowledgeBaseService
	KnowledgeService      interfaces.KnowledgeService
	ChunkService          interfaces.ChunkService
	SessionService        interfaces.SessionService
	MessageService        interfaces.MessageService
	ModelService          interfaces.ModelService
	EvaluationService     interfaces.EvaluationService
	KBHandler             *handler.KnowledgeBaseHandler
	KnowledgeHandler      *handler.KnowledgeHandler
	TenantHandler         *handler.TenantHandler
	TenantService         interfaces.TenantService
	ChunkHandler          *handler.ChunkHandler
	SessionHandler        *handler.SessionHandler
	MessageHandler        *handler.MessageHandler
	ModelHandler          *handler.ModelHandler
	EvaluationHandler     *handler.EvaluationHandler
	AuthHandler           *handler.AuthHandler
	InitializationHandler *handler.InitializationHandler
	SystemHandler         *handler.SystemHandler
}

// NewRouter 创建新的路由
func NewRouter(params RouterParams) *gin.Engine {
	r := gin.New()

	// CORS 中间件应放在最前面
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length", "Access-Control-Allow-Origin"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 其他中间件
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.Auth(params.TenantService, params.UserService, params.Config))

	// 添加OpenTelemetry追踪中间件
	r.Use(middleware.TracingMiddleware())

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 需要认证的API路由
	v1 := r.Group("/api/v1")
	{
		RegisterAuthRoutes(v1, params.AuthHandler)
		RegisterTenantRoutes(v1, params.TenantHandler)
		RegisterKnowledgeBaseRoutes(v1, params.KBHandler)
		RegisterKnowledgeRoutes(v1, params.KnowledgeHandler)
		RegisterChunkRoutes(v1, params.ChunkHandler)
		RegisterSessionRoutes(v1, params.SessionHandler)
		RegisterChatRoutes(v1, params.SessionHandler)
		RegisterMessageRoutes(v1, params.MessageHandler)
		RegisterModelRoutes(v1, params.ModelHandler)
		RegisterEvaluationRoutes(v1, params.EvaluationHandler)
		RegisterInitializationRoutes(v1, params.InitializationHandler)
		RegisterSystemRoutes(v1, params.SystemHandler)
	}

	return r
}

// RegisterChunkRoutes 注册分块相关的路由
func RegisterChunkRoutes(r *gin.RouterGroup, handler *handler.ChunkHandler) {
	// 分块路由组
	chunks := r.Group("/chunks")
	{
		// 获取分块列表
		chunks.GET("/:knowledge_id", handler.ListKnowledgeChunks)
		// 删除分块
		chunks.DELETE("/:knowledge_id/:id", handler.DeleteChunk)
		// 删除知识下的所有分块
		chunks.DELETE("/:knowledge_id", handler.DeleteChunksByKnowledgeID)
		// 更新分块信息
		chunks.PUT("/:knowledge_id/:id", handler.UpdateChunk)
	}
}

// RegisterKnowledgeRoutes 注册知识相关的路由
func RegisterKnowledgeRoutes(r *gin.RouterGroup, handler *handler.KnowledgeHandler) {
	// 知识库下的知识路由组
	kb := r.Group("/knowledge-bases/:id/knowledge")
	{
		// 从文件创建知识
		kb.POST("/file", handler.CreateKnowledgeFromFile)
		// 从URL创建知识
		kb.POST("/url", handler.CreateKnowledgeFromURL)
		// 获取知识库下的知识列表
		kb.GET("", handler.ListKnowledge)
	}

	// 知识路由组
	k := r.Group("/knowledge")
	{
		// 批量获取知识
		k.GET("/batch", handler.GetKnowledgeBatch)
		// 获取知识详情
		k.GET("/:id", handler.GetKnowledge)
		// 删除知识
		k.DELETE("/:id", handler.DeleteKnowledge)
		// 更新知识
		k.PUT("/:id", handler.UpdateKnowledge)
		// 获取知识文件
		k.GET("/:id/download", handler.DownloadKnowledgeFile)
		// 更新图像分块信息
		k.PUT("/image/:id/:chunk_id", handler.UpdateImageInfo)
	}
}

// RegisterKnowledgeBaseRoutes 注册知识库相关的路由
func RegisterKnowledgeBaseRoutes(r *gin.RouterGroup, handler *handler.KnowledgeBaseHandler) {
	// 知识库路由组
	kb := r.Group("/knowledge-bases")
	{
		// 创建知识库
		kb.POST("", handler.CreateKnowledgeBase)
		// 获取知识库列表
		kb.GET("", handler.ListKnowledgeBases)
		// 获取知识库详情
		kb.GET("/:id", handler.GetKnowledgeBase)
		// 更新知识库
		kb.PUT("/:id", handler.UpdateKnowledgeBase)
		// 删除知识库
		kb.DELETE("/:id", handler.DeleteKnowledgeBase)
		// 混合搜索
		kb.GET("/:id/hybrid-search", handler.HybridSearch)
		// 拷贝知识库
		kb.POST("/copy", handler.CopyKnowledgeBase)
	}
}

// RegisterMessageRoutes 注册消息相关的路由
func RegisterMessageRoutes(r *gin.RouterGroup, handler *handler.MessageHandler) {
	// 消息路由组
	messages := r.Group("/messages")
	{
		// 加载更早的消息，用于向上滚动加载
		messages.GET("/:session_id/load", handler.LoadMessages)
		// 删除消息
		messages.DELETE("/:session_id/:id", handler.DeleteMessage)
	}
}

// RegisterSessionRoutes 注册路由
func RegisterSessionRoutes(r *gin.RouterGroup, handler *handler.SessionHandler) {
	sessions := r.Group("/sessions")
	{
		sessions.POST("", handler.CreateSession)
		sessions.GET("/:id", handler.GetSession)
		sessions.GET("", handler.GetSessionsByTenant)
		sessions.PUT("/:id", handler.UpdateSession)
		sessions.DELETE("/:id", handler.DeleteSession)
		sessions.POST("/:session_id/generate_title", handler.GenerateTitle)
		// 继续接收活跃流
		sessions.GET("/continue-stream/:session_id", handler.ContinueStream)
	}
}

// RegisterChatRoutes 注册路由
func RegisterChatRoutes(r *gin.RouterGroup, handler *handler.SessionHandler) {
	knowledgeChat := r.Group("/knowledge-chat")
	{
		knowledgeChat.POST("/:session_id", handler.KnowledgeQA)
	}

	// 新增知识检索接口，不需要session_id
	knowledgeSearch := r.Group("/knowledge-search")
	{
		knowledgeSearch.POST("", handler.SearchKnowledge)
	}
}

// RegisterTenantRoutes 注册租户相关的路由
func RegisterTenantRoutes(r *gin.RouterGroup, handler *handler.TenantHandler) {
	// 租户路由组
	tenantRoutes := r.Group("/tenants")
	{
		tenantRoutes.POST("", handler.CreateTenant)
		tenantRoutes.GET("/:id", handler.GetTenant)
		tenantRoutes.PUT("/:id", handler.UpdateTenant)
		tenantRoutes.DELETE("/:id", handler.DeleteTenant)
		tenantRoutes.GET("", handler.ListTenants)
	}
}

// RegisterModelRoutes 注册模型相关的路由
func RegisterModelRoutes(r *gin.RouterGroup, handler *handler.ModelHandler) {
	// 模型路由组
	models := r.Group("/models")
	{
		// 创建模型
		models.POST("", handler.CreateModel)
		// 获取模型列表
		models.GET("", handler.ListModels)
		// 获取单个模型
		models.GET("/:id", handler.GetModel)
		// 更新模型
		models.PUT("/:id", handler.UpdateModel)
		// 删除模型
		models.DELETE("/:id", handler.DeleteModel)
	}
}

func RegisterEvaluationRoutes(r *gin.RouterGroup, handler *handler.EvaluationHandler) {
	evaluationRoutes := r.Group("/evaluation")
	{
		evaluationRoutes.POST("/", handler.Evaluation)
		evaluationRoutes.GET("/", handler.GetEvaluationResult)
	}
}

// RegisterAuthRoutes registers authentication routes
func RegisterAuthRoutes(r *gin.RouterGroup, handler *handler.AuthHandler) {
	r.POST("/auth/register", handler.Register)
	r.POST("/auth/login", handler.Login)
	r.POST("/auth/refresh", handler.RefreshToken)
	r.GET("/auth/validate", handler.ValidateToken)
	r.POST("/auth/logout", handler.Logout)
	r.GET("/auth/me", handler.GetCurrentUser)
	r.POST("/auth/change-password", handler.ChangePassword)
}

func RegisterInitializationRoutes(r *gin.RouterGroup, handler *handler.InitializationHandler) {
	// 初始化接口
	r.GET("/initialization/config/:kbId", handler.GetCurrentConfigByKB)
	r.POST("/initialization/initialize/:kbId", handler.InitializeByKB)

	// Ollama相关接口
	r.GET("/initialization/ollama/status", handler.CheckOllamaStatus)
	r.GET("/initialization/ollama/models", handler.ListOllamaModels)
	r.POST("/initialization/ollama/models/check", handler.CheckOllamaModels)
	r.POST("/initialization/ollama/models/download", handler.DownloadOllamaModel)
	r.GET("/initialization/ollama/download/progress/:taskId", handler.GetDownloadProgress)
	r.GET("/initialization/ollama/download/tasks", handler.ListDownloadTasks)

	// 远程API相关接口
	r.POST("/initialization/remote/check", handler.CheckRemoteModel)
	r.POST("/initialization/embedding/test", handler.TestEmbeddingModel)
	r.POST("/initialization/rerank/check", handler.CheckRerankModel)
	r.POST("/initialization/multimodal/test", handler.TestMultimodalFunction)

	r.POST("/initialization/extract/text-relation", handler.ExtractTextRelations)
	r.POST("/initialization/extract/fabri-tag", handler.FabriTag)
	r.POST("/initialization/extract/fabri-text", handler.FabriText)
}

// RegisterSystemRoutes registers system information routes
func RegisterSystemRoutes(r *gin.RouterGroup, handler *handler.SystemHandler) {
	systemRoutes := r.Group("/system")
	{
		systemRoutes.GET("/info", handler.GetSystemInfo)
	}
}
