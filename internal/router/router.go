package router

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"

	_ "github.com/Tencent/WeKnora/docs" // swagger docs
)

// RouterParams 라우터 매개변수
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
	SessionHandler        *session.Handler
	MessageHandler        *handler.MessageHandler
	ModelHandler          *handler.ModelHandler
	EvaluationHandler     *handler.EvaluationHandler
	AuthHandler           *handler.AuthHandler
	InitializationHandler *handler.InitializationHandler
	SystemHandler         *handler.SystemHandler
	MCPServiceHandler     *handler.MCPServiceHandler
	WebSearchHandler      *handler.WebSearchHandler
	FAQHandler            *handler.FAQHandler
	TagHandler            *handler.TagHandler
	CustomAgentHandler    *handler.CustomAgentHandler
}

// NewRouter 새 라우터 생성
func NewRouter(params RouterParams) *gin.Engine {
	r := gin.New()

	// CORS 미들웨어는 가장 앞에 위치해야 함
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length", "Access-Control-Allow-Origin"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 기본 미들웨어 (인증 불필요)
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Recovery())
	r.Use(middleware.ErrorHandler())

	// 상태 확인 (인증 불필요)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Swagger API 문서 (비-프로덕션 환경에서만 활성화)
	// GIN_MODE 환경 변수로 판단: release 모드에서는 Swagger 비활성화
	if gin.Mode() != gin.ReleaseMode {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler,
			ginSwagger.DefaultModelsExpandDepth(-1), // 기본적으로 모델 접기
			ginSwagger.DocExpansion("list"),         // 확장 모드: "list"(태그 확장), "full"(전체 확장), "none"(전체 접기)
			ginSwagger.DeepLinking(true),            // 딥 링킹 활성화
			ginSwagger.PersistAuthorization(true),   // 인증 정보 유지
		))
	}

	// 인증 미들웨어
	r.Use(middleware.Auth(params.TenantService, params.UserService, params.Config))

	// OpenTelemetry 추적 미들웨어 추가
	r.Use(middleware.TracingMiddleware())

	// 인증이 필요한 API 라우트
	v1 := r.Group("/api/v1")
	{
		RegisterAuthRoutes(v1, params.AuthHandler)
		RegisterTenantRoutes(v1, params.TenantHandler)
		RegisterKnowledgeBaseRoutes(v1, params.KBHandler)
		RegisterKnowledgeTagRoutes(v1, params.TagHandler)
		RegisterKnowledgeRoutes(v1, params.KnowledgeHandler)
		RegisterFAQRoutes(v1, params.FAQHandler)
		RegisterChunkRoutes(v1, params.ChunkHandler)
		RegisterSessionRoutes(v1, params.SessionHandler)
		RegisterChatRoutes(v1, params.SessionHandler)
		RegisterMessageRoutes(v1, params.MessageHandler)
		RegisterModelRoutes(v1, params.ModelHandler)
		RegisterEvaluationRoutes(v1, params.EvaluationHandler)
		RegisterInitializationRoutes(v1, params.InitializationHandler)
		RegisterSystemRoutes(v1, params.SystemHandler)
		RegisterMCPServiceRoutes(v1, params.MCPServiceHandler)
		RegisterWebSearchRoutes(v1, params.WebSearchHandler)
		RegisterCustomAgentRoutes(v1, params.CustomAgentHandler)
	}

	return r
}

// RegisterChunkRoutes 청크 관련 라우트 등록
func RegisterChunkRoutes(r *gin.RouterGroup, handler *handler.ChunkHandler) {
	// 청크 라우트 그룹
	chunks := r.Group("/chunks")
	{
		// 청크 목록 조회
		chunks.GET("/:knowledge_id", handler.ListKnowledgeChunks)
		// chunk_id로 단일 청크 조회 (knowledge_id 불필요)
		chunks.GET("/by-id/:id", handler.GetChunkByIDOnly)
		// 청크 삭제
		chunks.DELETE("/:knowledge_id/:id", handler.DeleteChunk)
		// 지식 하위 모든 청크 삭제
		chunks.DELETE("/:knowledge_id", handler.DeleteChunksByKnowledgeID)
		// 청크 정보 업데이트
		chunks.PUT("/:knowledge_id/:id", handler.UpdateChunk)
		// 단일 생성 질문 삭제 (질문 ID 사용)
		chunks.DELETE("/by-id/:id/questions", handler.DeleteGeneratedQuestion)
	}
}

// RegisterKnowledgeRoutes 지식 관련 라우트 등록
func RegisterKnowledgeRoutes(r *gin.RouterGroup, handler *handler.KnowledgeHandler) {
	// 지식베이스 하위 지식 라우트 그룹
	kb := r.Group("/knowledge-bases/:id/knowledge")
	{
		// 파일에서 지식 생성
		kb.POST("/file", handler.CreateKnowledgeFromFile)
		// URL에서 지식 생성
		kb.POST("/url", handler.CreateKnowledgeFromURL)
		// 수동 마크다운 입력
		kb.POST("/manual", handler.CreateManualKnowledge)
		// 지식베이스 하위 지식 목록 조회
		kb.GET("", handler.ListKnowledge)
	}

	// 지식 라우트 그룹
	k := r.Group("/knowledge")
	{
		// 지식 일괄 조회
		k.GET("/batch", handler.GetKnowledgeBatch)
		// 지식 상세 조회
		k.GET("/:id", handler.GetKnowledge)
		// 지식 삭제
		k.DELETE("/:id", handler.DeleteKnowledge)
		// 지식 업데이트
		k.PUT("/:id", handler.UpdateKnowledge)
		// 수동 마크다운 지식 업데이트
		k.PUT("/manual/:id", handler.UpdateManualKnowledge)
		// 지식 파일 다운로드
		k.GET("/:id/download", handler.DownloadKnowledgeFile)
		// 이미지 청크 정보 업데이트
		k.PUT("/image/:id/:chunk_id", handler.UpdateImageInfo)
		// 지식 태그 일괄 업데이트
		k.PUT("/tags", handler.UpdateKnowledgeTagBatch)
		// 지식 검색
		k.GET("/search", handler.SearchKnowledge)
	}
}

// RegisterFAQRoutes FAQ 관련 라우트 등록
func RegisterFAQRoutes(r *gin.RouterGroup, handler *handler.FAQHandler) {
	if handler == nil {
		return
	}
	faq := r.Group("/knowledge-bases/:id/faq")
	{
		faq.GET("/entries", handler.ListEntries)
		faq.GET("/entries/export", handler.ExportEntries)
		faq.GET("/entries/:entry_id", handler.GetEntry)
		faq.POST("/entries", handler.UpsertEntries)
		faq.POST("/entry", handler.CreateEntry)
		faq.PUT("/entries/:entry_id", handler.UpdateEntry)
		// 통합 일괄 업데이트 API - is_enabled, is_recommended, tag_id 지원
		faq.PUT("/entries/fields", handler.UpdateEntryFieldsBatch)
		faq.PUT("/entries/tags", handler.UpdateEntryTagBatch)
		faq.DELETE("/entries", handler.DeleteEntries)
		faq.POST("/search", handler.SearchFAQ)
	}
	// FAQ 가져오기 진행 상황 라우트 (지식베이스 범위 외부)
	faqImport := r.Group("/faq/import")
	{
		faqImport.GET("/progress/:task_id", handler.GetImportProgress)
	}
}

// RegisterKnowledgeBaseRoutes 지식베이스 관련 라우트 등록
func RegisterKnowledgeBaseRoutes(r *gin.RouterGroup, handler *handler.KnowledgeBaseHandler) {
	// 지식베이스 라우트 그룹
	kb := r.Group("/knowledge-bases")
	{
		// 지식베이스 생성
		kb.POST("", handler.CreateKnowledgeBase)
		// 지식베이스 목록 조회
		kb.GET("", handler.ListKnowledgeBases)
		// 지식베이스 상세 조회
		kb.GET("/:id", handler.GetKnowledgeBase)
		// 지식베이스 업데이트
		kb.PUT("/:id", handler.UpdateKnowledgeBase)
		// 지식베이스 삭제
		kb.DELETE("/:id", handler.DeleteKnowledgeBase)
		// 하이브리드 검색
		kb.GET("/:id/hybrid-search", handler.HybridSearch)
		// 지식베이스 복사
		kb.POST("/copy", handler.CopyKnowledgeBase)
		// 지식베이스 복사 진행 상황 조회
		kb.GET("/copy/progress/:task_id", handler.GetKBCloneProgress)
	}
}

// RegisterKnowledgeTagRoutes 지식베이스 태그 관련 라우트 등록
func RegisterKnowledgeTagRoutes(r *gin.RouterGroup, tagHandler *handler.TagHandler) {
	if tagHandler == nil {
		return
	}
	kbTags := r.Group("/knowledge-bases/:id/tags")
	{
		kbTags.GET("", tagHandler.ListTags)
		kbTags.POST("", tagHandler.CreateTag)
		kbTags.PUT("/:tag_id", tagHandler.UpdateTag)
		kbTags.DELETE("/:tag_id", tagHandler.DeleteTag)
	}
}

// RegisterMessageRoutes 메시지 관련 라우트 등록
func RegisterMessageRoutes(r *gin.RouterGroup, handler *handler.MessageHandler) {
	// 메시지 라우트 그룹
	messages := r.Group("/messages")
	{
		// 이전 메시지 로드 (위로 스크롤 로딩용)
		messages.GET("/:session_id/load", handler.LoadMessages)
		// 메시지 삭제
		messages.DELETE("/:session_id/:id", handler.DeleteMessage)
	}
}

// RegisterSessionRoutes 세션 관련 라우트 등록
func RegisterSessionRoutes(r *gin.RouterGroup, handler *session.Handler) {
	sessions := r.Group("/sessions")
	{
		sessions.POST("", handler.CreateSession)
		sessions.GET("/:id", handler.GetSession)
		sessions.GET("", handler.GetSessionsByTenant)
		sessions.PUT("/:id", handler.UpdateSession)
		sessions.DELETE("/:id", handler.DeleteSession)
		sessions.POST("/:session_id/generate_title", handler.GenerateTitle)
		sessions.POST("/:session_id/stop", handler.StopSession)
		// 활성 스트림 계속 수신
		sessions.GET("/continue-stream/:session_id", handler.ContinueStream)
	}
}

// RegisterChatRoutes 채팅 관련 라우트 등록
func RegisterChatRoutes(r *gin.RouterGroup, handler *session.Handler) {
	knowledgeChat := r.Group("/knowledge-chat")
	{
		knowledgeChat.POST("/:session_id", handler.KnowledgeQA)
	}

	// 에이전트 기반 채팅
	agentChat := r.Group("/agent-chat")
	{
		agentChat.POST("/:session_id", handler.AgentQA)
	}

	// 신규 지식 검색 인터페이스 (session_id 불필요)
	knowledgeSearch := r.Group("/knowledge-search")
	{
		knowledgeSearch.POST("", handler.SearchKnowledge)
	}
}

// RegisterTenantRoutes 테넌트 관련 라우트 등록
func RegisterTenantRoutes(r *gin.RouterGroup, handler *handler.TenantHandler) {
	// 모든 테넌트 조회 라우트 추가 (크로스 테넌트 권한 필요)
	r.GET("/tenants/all", handler.ListAllTenants)
	// 테넌트 검색 라우트 추가 (크로스 테넌트 권한 필요, 페이징 및 검색 지원)
	r.GET("/tenants/search", handler.SearchTenants)
	// 테넌트 라우트 그룹
	tenantRoutes := r.Group("/tenants")
	{
		tenantRoutes.POST("", handler.CreateTenant)
		tenantRoutes.GET("/:id", handler.GetTenant)
		tenantRoutes.PUT("/:id", handler.UpdateTenant)
		tenantRoutes.DELETE("/:id", handler.DeleteTenant)
		tenantRoutes.GET("", handler.ListTenants)

		// 일반적인 KV 구성 관리 (테넌트 수준)
		// 테넌트 ID는 인증 컨텍스트에서 가져옴
		tenantRoutes.GET("/kv/:key", handler.GetTenantKV)
		tenantRoutes.PUT("/kv/:key", handler.UpdateTenantKV)
	}
}

// RegisterModelRoutes 모델 관련 라우트 등록
func RegisterModelRoutes(r *gin.RouterGroup, handler *handler.ModelHandler) {
	// 모델 라우트 그룹
	models := r.Group("/models")
	{
		// 모델 공급업체 목록 조회
		models.GET("/providers", handler.ListModelProviders)
		// 모델 생성
		models.POST("", handler.CreateModel)
		// 모델 목록 조회
		models.GET("", handler.ListModels)
		// 단일 모델 조회
		models.GET("/:id", handler.GetModel)
		// 모델 업데이트
		models.PUT("/:id", handler.UpdateModel)
		// 모델 삭제
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

// RegisterAuthRoutes 인증 관련 라우트 등록
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
	// 초기화 인터페이스
	r.GET("/initialization/config/:kbId", handler.GetCurrentConfigByKB)
	r.POST("/initialization/initialize/:kbId", handler.InitializeByKB)
	r.PUT("/initialization/config/:kbId", handler.UpdateKBConfig) // 모델 ID만 전달하는 새로운 간소화된 인터페이스

	// Ollama 관련 인터페이스
	r.GET("/initialization/ollama/status", handler.CheckOllamaStatus)
	r.GET("/initialization/ollama/models", handler.ListOllamaModels)
	r.POST("/initialization/ollama/models/check", handler.CheckOllamaModels)
	r.POST("/initialization/ollama/models/download", handler.DownloadOllamaModel)
	r.GET("/initialization/ollama/download/progress/:taskId", handler.GetDownloadProgress)
	r.GET("/initialization/ollama/download/tasks", handler.ListDownloadTasks)

	// 원격 API 관련 인터페이스
	r.POST("/initialization/remote/check", handler.CheckRemoteModel)
	r.POST("/initialization/embedding/test", handler.TestEmbeddingModel)
	r.POST("/initialization/rerank/check", handler.CheckRerankModel)
	r.POST("/initialization/multimodal/test", handler.TestMultimodalFunction)

	r.POST("/initialization/extract/text-relation", handler.ExtractTextRelations)
	r.POST("/initialization/extract/fabri-tag", handler.FabriTag)
	r.POST("/initialization/extract/fabri-text", handler.FabriText)
}

// RegisterSystemRoutes 시스템 정보 라우트 등록
func RegisterSystemRoutes(r *gin.RouterGroup, handler *handler.SystemHandler) {
	systemRoutes := r.Group("/system")
	{
		systemRoutes.GET("/info", handler.GetSystemInfo)
		systemRoutes.GET("/minio/buckets", handler.ListMinioBuckets)
	}
}

// RegisterMCPServiceRoutes MCP 서비스 라우트 등록
func RegisterMCPServiceRoutes(r *gin.RouterGroup, handler *handler.MCPServiceHandler) {
	mcpServices := r.Group("/mcp-services")
	{
		// MCP 서비스 생성
		mcpServices.POST("", handler.CreateMCPService)
		// MCP 서비스 목록 조회
		mcpServices.GET("", handler.ListMCPServices)
		// ID로 MCP 서비스 조회
		mcpServices.GET("/:id", handler.GetMCPService)
		// MCP 서비스 업데이트
		mcpServices.PUT("/:id", handler.UpdateMCPService)
		// MCP 서비스 삭제
		mcpServices.DELETE("/:id", handler.DeleteMCPService)
		// MCP 서비스 연결 테스트
		mcpServices.POST("/:id/test", handler.TestMCPService)
		// MCP 서비스 도구 조회
		mcpServices.GET("/:id/tools", handler.GetMCPServiceTools)
		// MCP 서비스 리소스 조회
		mcpServices.GET("/:id/resources", handler.GetMCPServiceResources)
	}
}

// RegisterWebSearchRoutes 웹 검색 라우트 등록
func RegisterWebSearchRoutes(r *gin.RouterGroup, webSearchHandler *handler.WebSearchHandler) {
	// 웹 검색 공급자
	webSearch := r.Group("/web-search")
	{
		// 사용 가능한 공급자 조회
		webSearch.GET("/providers", webSearchHandler.GetProviders)
	}
}

// RegisterCustomAgentRoutes 사용자 정의 에이전트 라우트 등록
func RegisterCustomAgentRoutes(r *gin.RouterGroup, agentHandler *handler.CustomAgentHandler) {
	agents := r.Group("/agents")
	{
		// 플레이스홀더 정의 조회 (충돌 방지를 위해 /:id 앞에 있어야 함)
		agents.GET("/placeholders", agentHandler.GetPlaceholders)
		// 사용자 정의 에이전트 생성
		agents.POST("", agentHandler.CreateAgent)
		// 모든 에이전트 목록 조회 (내장 포함)
		agents.GET("", agentHandler.ListAgents)
		// ID로 에이전트 조회
		agents.GET("/:id", agentHandler.GetAgent)
		// 에이전트 업데이트
		agents.PUT("/:id", agentHandler.UpdateAgent)
		// 에이전트 삭제
		agents.DELETE("/:id", agentHandler.DeleteAgent)
		// 에이전트 복사
		agents.POST("/:id/copy", agentHandler.CopyAgent)
	}
}
