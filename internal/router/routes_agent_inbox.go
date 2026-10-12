package router

import (
	"context"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// RegisterAgentInboxRoutes wires the agent-inbox endpoints and attaches the
// user-action capture middleware (agent-sidebar-inbox P1).
//
// The ui-events POST is Viewer+ — reporting what page you are looking at is
// a read-level operation scoped to the caller's own identity. The panel
// routes under /agent-inbox/events are read/mark-read only, also Viewer+.
//
// Mount-order warning: the capture middleware is installed with r.Use() on
// the shared /api/v1 group, and gin only applies a middleware to routes
// registered AFTER the Use. This function must therefore be called before
// RegisterKnowledgeRoutes and friends — mounting it after them silently
// disabled the whole catalog once (reparse/upload/chunk events vanished).
func RegisterAgentInboxRoutes(
	r *gin.RouterGroup,
	inboxHandler *handler.AgentInboxHandler,
	inboxService interfaces.AgentInboxService,
	knowledgeService interfaces.KnowledgeService,
	g *rbacGuards,
) {
	if inboxService != nil {
		var resolver middleware.KnowledgeKBResolver
		if knowledgeService != nil {
			resolver = func(ctx context.Context, knowledgeID string) (string, error) {
				kb, err := knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
				if err != nil || kb == nil {
					return "", err
				}
				return kb.KnowledgeBaseID, nil
			}
		}
		r.Use(middleware.AgentInboxUserAction(inboxService, resolver))
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
	// User-facing inbox panel (identity from the auth context). Ingest
	// stays internal: feature code goes through AgentInboxService.Emit and
	// the write-op middleware — there is deliberately no generic HTTP
	// event-injection endpoint, so external callers can never forge inbox
	// content (a dedicated, rate-limited external API can be added later
	// if a real integration needs one).
	inboxAPI := r.Group("/agent-inbox/events")
	{
		inboxAPI.GET("", g.Viewer(), inboxHandler.ListInboxEvents)
		inboxAPI.POST("/read", g.Viewer(), inboxHandler.MarkInboxRead)
	}
}
