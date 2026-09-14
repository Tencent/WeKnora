package router

import (
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterLearningRoutes registers Web-only endpoints for personal learning.
// Deliberately not an apiKeyGroup: learning records belong to a human, and
// neither a full-access nor a platform key may inherit that person's profile.
func RegisterLearningRoutes(r *gin.RouterGroup, h *handler.LearningHandler, g *rbacGuards) {
	if h == nil {
		return
	}
	routes := r.Group("/learning", g.Viewer(), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if !tools.LearningWebCallerAllowed(c.Request.Context()) {
			c.AbortWithStatusJSON(
				403,
				gin.H{
					"success": false,
					"error": gin.H{
						"code":    "learning_forbidden",
						"message": "Learning requires a Web user in their active workspace.",
					},
				},
			)
			return
		}
		c.Next()
	})
	routes.GET("/settings", h.GetSettings)
	routes.PUT("/settings", h.SetEnabled)
	routes.GET("/overview", h.Overview)
	routes.GET("/nodes/:id", h.Node)
	routes.POST("/nodes/:id/view", h.RecordView)
	routes.GET("/recommendations", h.Recommendations)
	routes.POST("/overlay", h.Overlay)
	routes.POST("/question-sets", h.PrepareQuiz)
	routes.GET("/question-sets/:id", h.GetQuiz)
	routes.POST("/attempts", h.SubmitAnswer)
	routes.GET("/export", h.Export)
	routes.DELETE("/profile", h.Clear)
}
