package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterFeedbackRoutes registers answer feedback and chunk quality routes.
func RegisterFeedbackRoutes(r *gin.RouterGroup, feedbackHandler *handler.ChunkFeedbackHandler, g *rbacGuards) {
	if feedbackHandler == nil {
		return
	}

	// Any signed-in workspace member can manage their own answer feedback.
	r.POST("/feedback", g.Viewer(), feedbackHandler.SubmitFeedback)
	r.DELETE("/feedback", g.Viewer(), feedbackHandler.CancelFeedback)
	r.GET("/feedback/dislike-reasons", g.Viewer(), feedbackHandler.GetDislikeReasons)
	r.GET("/feedback/user-feedback", g.Viewer(), feedbackHandler.GetUserFeedback)

	// Chunk-quality governance is tenant-admin only.
	r.GET("/chunks/low-quality", g.Admin(), feedbackHandler.ListLowQualityChunks)
	r.GET("/chunks/feedback-overview", g.Admin(), feedbackHandler.GetFeedbackOverview)
	r.GET("/admin/chunks/:chunk_id/stats", g.Admin(), feedbackHandler.GetChunkStats)
	r.POST("/admin/chunks/:chunk_id/reset-feedback", g.Admin(), feedbackHandler.ResetChunkFeedback)
	r.GET("/admin/chunks/:chunk_id/weight-logs", g.Admin(), feedbackHandler.GetChunkWeightLogs)
}
