package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterAutomationRoutes registers the caller's automations and in-app
// notifications. An automation runs as the person who made it, and the
// service scopes every route to the caller's own records, so tenant members
// (Viewer+) may use them. They are not declared for API keys (default-deny):
// an automation needs a signed-in owner.
func RegisterAutomationRoutes(r *gin.RouterGroup, h *handler.AutomationHandler, g *rbacGuards) {
	if h == nil {
		return
	}
	automations := r.Group("/automations", g.Viewer())
	{
		automations.GET("", h.ListAutomations)
		automations.POST("", h.CreateAutomation)
		automations.POST("/validate-trigger", h.ValidateTrigger)
		automations.GET("/by-session/:session_id", h.GetAutomationBySession)
		automations.GET("/:id", h.GetAutomation)
		automations.PUT("/:id", h.UpdateAutomation)
		automations.DELETE("/:id", h.DeleteAutomation)
		automations.POST("/:id/pause", h.PauseAutomation)
		automations.POST("/:id/resume", h.ResumeAutomation)
		automations.POST("/:id/run", h.RunAutomation)
		automations.GET("/:id/runs", h.ListAutomationRuns)
		automations.GET("/:id/grants", h.ListAutomationGrants)
		automations.POST("/:id/grants", h.AddAutomationGrant)
		automations.GET("/:id/grant-candidates", h.ListAutomationGrantCandidates)
	}
	runs := r.Group("/automation-runs", g.Viewer())
	{
		runs.GET("/:id", h.GetAutomationRun)
		runs.POST("/:id/cancel", h.CancelAutomationRun)
	}
	notifications := r.Group("/notifications", g.Viewer())
	{
		notifications.GET("", h.ListNotifications)
		notifications.POST("/read", h.MarkNotificationsRead)
		notifications.DELETE("/read", h.ClearReadNotifications)
	}
}
