package router

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterAssistantResourceRoutes retains the normal KB guards for attachment imports.
func RegisterAssistantResourceRoutes(r *gin.RouterGroup, h *handler.AssistantResourceHandler, g *rbacGuards) {
	// A web-principal internal dispatch is additionally required by the handler.
	g.apiKeyRoute(r, http.MethodGet, "/assistant/attachments", apiKeyFullAccess(), g.Viewer(), h.ListAttachments)
	g.apiKeyRoute(r, http.MethodGet, "/assistant/attachments/:attachment_id",
		apiKeyFullAccess(), g.Viewer(), h.DownloadAttachment)
	g.apiKeyRoute(r, http.MethodPost, "/knowledge-bases/:id/selected-attachment", apiKeyIngest(apiKeyFullAccess()),
		g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), h.ImportKnowledge)
	g.apiKeyRoute(r, http.MethodPost, "/knowledge-bases/:id/faq/selected-import", apiKeyIngest(apiKeyFullAccess()),
		g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), h.ImportFAQ)
}
