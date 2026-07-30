package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterFeedbackRoutes registers message feedback and chunk governance endpoints.
func RegisterFeedbackRoutes(r *gin.RouterGroup, handler *handler.FeedbackHandler, g *rbacGuards) {
	if handler == nil {
		return
	}
	// Policies are declared so the global API-key gate has a complete route
	// map; mutating service methods still reject non-web-user principals.
	sessions := g.apiKeyGroup(r.Group("/sessions"), apiKeyFullAccess())
	sessions.PUT("/:session_id/messages/:message_id/feedback", g.Viewer(), handler.PutMessageFeedback)

	chunkRead := g.apiKeyGroup(r.Group("/chunks"), apiKeyFullAccess())
	chunkRead.GET("/by-id/:id/feedback", g.Viewer(), g.KBAccessReadFromChunkIDParam("id"), handler.GetChunkFeedbackDetails)

	kb := g.apiKeyGroup(r.Group("/knowledge-bases"), apiKeyFullAccess())
	kb.POST("/:id/chunks/:chunk_id/feedback/reset",
		g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.ResetChunkFeedback)

	// Governance is intentionally JWT-only. API keys are denied by the
	// default-deny route authorizer because audit history is an interactive
	// owner/admin surface.
	governance := r.Group(
		"/knowledge-bases/:id/chunk-feedback",
		g.OwnedKBOrAdmin(),
		g.KBAccessRead("id"),
	)
	governance.GET("", handler.ListChunkFeedback)
	governance.GET("/:chunk_id/history", handler.ListChunkFeedbackHistory)
	governance.GET("/:chunk_id", handler.GetChunkFeedbackGovernanceDetails)
	governance.POST(
		"/:chunk_id/reset",
		g.KBAccessWrite("id"),
		handler.ResetChunkFeedbackGovernance,
	)
}
