package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterFeedbackRoutes registers the user-facing answer rating endpoints and
// the admin chunk feedback statistics / weight management endpoints.
//
// Rating an answer is per-session user state: Viewer+ with the chat API-key
// capability, exactly like loading message history. The admin surfaces are
// tenant-wide infrastructure (they expose feedback aggregated across every KB),
// so they are gated at Admin+ for JWT callers.
func RegisterFeedbackRoutes(r *gin.RouterGroup, h *handler.FeedbackHandler, g *rbacGuards) {
	// User-facing rating of an assistant answer inside the caller's own
	// session. Session/message ownership is enforced by the service.
	messages := g.apiKeyGroup(r.Group("/messages"), apiKeyFullAccess())
	feedback := messages.With(apiKeyChat(apiKeyFullAccess()))
	{
		feedback.POST("/:session_id/:id/feedback", g.Viewer(), h.SubmitFeedback)
		feedback.DELETE("/:session_id/:id/feedback", g.Viewer(), h.CancelFeedback)
	}

	// Admin chunk feedback statistics and weight management.
	admin := g.apiKeyGroup(r.Group("/knowledge-bases/chunk-feedback"), apiKeyFullAccess())
	{
		admin.GET("/stats", g.Admin(), h.GetChunkFeedbackStats)
		admin.GET("/stats/:chunk_id", g.Admin(), h.GetChunkFeedbackDetail)
		admin.GET("/weight-logs", g.Admin(), h.ListWeightLogs)
		admin.POST("/reset", g.Admin(), h.ResetChunkFeedback)
		admin.GET("/config", g.Admin(), h.GetConfig)
		admin.PUT("/config", g.Admin(), h.UpdateConfig)
	}
}
