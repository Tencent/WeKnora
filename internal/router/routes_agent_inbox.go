package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// RegisterAgentInboxRoutes wires the agent-inbox UI signal endpoint and
// attaches the user-action capture middleware (agent-sidebar-inbox P1).
//
// The ui-events POST is Viewer+ — reporting what page you are looking at is
// a read-level operation scoped to the caller's own identity. The capture
// middleware wraps the whole /api/v1 group but only acts on the catalog
// routes (see middleware.InboxUserActionCatalog); unmatched routes pay one
// map lookup.
func RegisterAgentInboxRoutes(
	r *gin.RouterGroup,
	inboxHandler *handler.AgentInboxHandler,
	inboxService interfaces.AgentInboxService,
	g *rbacGuards,
) {
	if inboxService != nil {
		r.Use(middleware.AgentInboxUserAction(inboxService))
	}
	if inboxHandler == nil {
		return
	}
	// Route conflict note: /knowledge-bases/:id/ui-events shares the :id
	// group with KB routes — gin resolves static suffixes fine.
	inbox := r.Group("/knowledge-bases/:id/ui-events")
	{
		inbox.POST("", g.Viewer(), inboxHandler.PostUIEvent)
	}
}
