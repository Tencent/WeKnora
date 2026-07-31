package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
)

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

		
		chatMessages.POST("/:session_id/:id/feedback", g.Viewer(), handler.MessageFeedback)

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
		sessions.POST("/:session_id/attachments", handler.UploadTemporaryDocument)
		sessions.GET("/:id/attachments", handler.ListTemporaryDocuments)
		sessions.GET("/:id/attachments/:attachment_id", handler.GetTemporaryDocument)
		sessions.GET("/:id/attachments/:attachment_id/preview", handler.PreviewTemporaryDocument)
		sessions.DELETE("/:id/attachments/:attachment_id", handler.DeleteTemporaryDocument)
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
