package middleware

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// Agent-inbox user-action capture (agent-sidebar-inbox §4.3).
//
// Write operations are captured at the routing layer via a route→semantic
// action catalog: one middleware over the API group, zero per-handler
// emission. A route not in the catalog contributes nothing (fail-safe:
// forgetting to add a new route means no inbox event, never a wrong one).
// View events are NOT captured here — GETs are too noisy and only the
// frontend knows the semantic view context; they arrive via the ui-events
// endpoint instead.

// InboxRouteAction maps one API route pattern to a semantic inbox event.
// Path is the gin route pattern (e.g. "/api/v1/knowledge-bases/:id/knowledge/file").
type InboxRouteAction struct {
	Method    string
	Path      string
	EventType string
	// KBParam names the path parameter carrying the KB id. Empty for
	// routes where the KB must be resolved from another param — such
	// routes are not supported by the MVP catalog.
	KBParam string
}

// InboxUserActionCatalog is the MVP catalog. Keep it small and high-value;
// every entry must be a mutation a hot-starting agent benefits from knowing.
var InboxUserActionCatalog = []InboxRouteAction{
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge-bases/:id/knowledge/file",
		EventType: types.InboxTypeUserUploadedDoc, KBParam: "id",
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge-bases/:id/knowledge/url",
		EventType: types.InboxTypeUserUploadedDoc, KBParam: "id",
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge-bases/:id/knowledge/manual",
		EventType: types.InboxTypeUserUploadedDoc, KBParam: "id",
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge/:id/reparse",
		EventType: types.InboxTypeUserReparseDoc, KBParam: "",
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge/batch-reparse",
		EventType: types.InboxTypeUserReparseDoc, KBParam: "",
	},
	{
		Method: http.MethodPut, Path: "/api/v1/chunks/:knowledge_id/:id",
		EventType: types.InboxTypeUserEditChunk, KBParam: "",
	},
}

// inboxActionIndex is method+path → action, built once.
var inboxActionIndex = func() map[string]InboxRouteAction {
	idx := make(map[string]InboxRouteAction, len(InboxUserActionCatalog))
	for _, a := range InboxUserActionCatalog {
		idx[a.Method+" "+a.Path] = a
	}
	return idx
}()

// AgentInboxUserAction captures user write operations into the agent inbox.
// Best-effort by design: the inbox never affects the proxied request.
func AgentInboxUserAction(inbox interfaces.AgentInboxService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Only successful mutations are signals; a 4xx/5xx is either noise
		// or already surfaced to the user as a toast (captured client-side).
		if c.Writer.Status() >= http.StatusBadRequest {
			return
		}
		action, ok := inboxActionIndex[c.Request.Method+" "+c.FullPath()]
		if !ok || inbox == nil {
			return
		}

		userID, _ := c.Get(types.UserIDContextKey.String())
		userStr, _ := userID.(string)
		tenantID := c.GetUint64(types.TenantIDContextKey.String())
		if tenantID == 0 {
			return
		}

		kbID := ""
		if action.KBParam != "" {
			kbID = c.Param(action.KBParam)
		}
		payload := types.JSONMap{
			"route":     c.FullPath(),
			"method":    c.Request.Method,
			"dedup_key": "", // mutations are never debounced
		}
		// Attach the document id when the route carries one, so the agent
		// can follow up on the exact document ("user reparsed X").
		for _, p := range []string{"id", "knowledge_id"} {
			if v := c.Param(p); v != "" && p != action.KBParam {
				payload["knowledge_id"] = v
				break
			}
		}

		inbox.Ingest(c.Request.Context(), &types.AgentInboxEvent{
			TenantID: tenantID,
			KBID:     kbID,
			UserID:   userStr,
			Type:     action.EventType,
			Payload:  payload,
		})
	}
}
