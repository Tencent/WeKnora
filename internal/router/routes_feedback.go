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
	// Feedback and governance are human actions. Raw groups deliberately stay
	// undeclared in the API-key authorizer, whose default is deny.
	sessions := r.Group("/sessions")
	// Existing PUT session routes use :id. Gin requires one wildcard name per
	// radix-tree position, so reuse it to keep full-router registration valid.
	sessions.PUT("/:id/messages/:message_id/feedback", g.Viewer(), handler.PutMessageFeedback)

	chunkRead := r.Group("/chunks")
	chunkRead.GET(
		"/by-id/:id/feedback",
		g.Admin(), g.KBAccessReadFromChunkIDParam("id"), handler.GetChunkFeedbackDetails,
	)

	kb := r.Group("/knowledge-bases")
	kb.POST("/:id/chunks/:chunk_id/feedback/reset",
		g.Admin(), g.KBAccessWrite("id"), handler.ResetChunkFeedback)
}
