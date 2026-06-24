package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

func RegisterProcessingDashboardRoutes(
	r *gin.RouterGroup,
	h *handler.ProcessingDashboardHandler,
	g *rbacGuards,
) {
	if h == nil {
		return
	}
	group := r.Group("/knowledge-processing", g.Viewer())
	group.GET("/dashboard", h.GetDashboard)
	group.GET("/stages/:stage/items", h.ListStageItems)
	group.GET("/knowledge/:id", g.KBAccessReadFromKnowledgeIDParam("id"), h.GetKnowledgeProcessingDetail)
}
