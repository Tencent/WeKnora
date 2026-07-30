package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterChunkFeedbackRoutes 注册知识库片段反馈相关的路由
func RegisterChunkFeedbackRoutes(r *gin.RouterGroup, h *handler.ChunkFeedbackHandler, g *rbacGuards) {
	// 反馈提交路由 - 任何登录用户都可以提交反馈
	feedback := r.Group("/feedback")
	{
		feedback.POST("/chunk", g.Viewer(), h.SubmitFeedback)
	}

	// 片段统计路由 - Viewer+ 可以查看
	chunks := r.Group("/chunks")
	{
		chunks.GET("/:chunk_id/stats", g.Viewer(), h.GetChunkStats)
		chunks.GET("/:chunk_id/weight-logs", g.Viewer(), h.GetChunkWeightLogs)
		// 重置片段反馈 - 需要 Admin 权限
		chunks.POST("/feedback/reset", g.Admin(), h.ResetChunkFeedback)
	}

	// 知识库片段统计路由
	kbChunks := r.Group("/knowledge-bases/:kb_id/chunks")
	{
		kbChunks.GET("/stats", g.Viewer(), h.ListChunksByStats)
		kbChunks.POST("/batch-adjust-weights", g.Admin(), h.BatchAdjustWeights)
	}

	// 知识库反馈汇总统计
	kb := r.Group("/knowledge-bases")
	{
		kb.GET("/:kb_id/feedback-summary", g.Viewer(), h.GetFeedbackSummary)
	}
}
