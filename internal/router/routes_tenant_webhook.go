package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterTenantWebhookRoutes mounts Owner+ workspace webhook CRUD under
// /tenants/:id/event/... so PathTenantMatch still binds :id as the workspace.
func RegisterTenantWebhookRoutes(
	r *gin.RouterGroup,
	h *handler.WebhookEndpointHandler,
	g *rbacGuards,
) {
	if h == nil || g == nil {
		return
	}
	tenantByID := r.Group("/tenants/:id", g.PathTenantMatch())
	cap := apiKeyManageTenantSettings(apiKeyFullAccess())
	g.apiKeyRoute(tenantByID, http.MethodGet, "/event/webhooks", cap, g.Owner(), h.List)
	g.apiKeyRoute(tenantByID, http.MethodPost, "/event/webhooks", cap, g.Owner(), h.Create)
	g.apiKeyRoute(tenantByID, http.MethodPatch, "/event/webhooks/:hook_id", cap, g.Owner(), h.Update)
	g.apiKeyRoute(tenantByID, http.MethodDelete, "/event/webhooks/:hook_id", cap, g.Owner(), h.Delete)
	g.apiKeyRoute(tenantByID, http.MethodPost, "/event/webhooks/:hook_id/test", cap, g.Owner(), h.Test)
	g.apiKeyRoute(tenantByID, http.MethodGet, "/event/webhooks/:hook_id/deliveries", cap, g.Owner(), h.ListDeliveries)
	g.apiKeyRoute(tenantByID, http.MethodGet, "/event/types", cap, g.Owner(), h.ListTypes)
}

func serveKnowledgeDownloadTickets(r *gin.Engine, h *handler.KnowledgeDownloadTicketHandler) {
	if h == nil {
		return
	}
	r.GET("/api/v1/files/knowledge-download/:id", h.Download)
	r.HEAD("/api/v1/files/knowledge-download/:id", h.Head)
	r.POST("/api/v1/files/knowledge-download/:id/renew", h.Renew)
}
