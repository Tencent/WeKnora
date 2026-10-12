package middleware

import (
	"context"
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
//
// Mount-order warning: gin's Use() only affects routes registered AFTER it.
// RegisterAgentInboxRoutes must therefore run before the knowledge routes —
// mounting it after them silently disabled the whole catalog once already.

// InboxRouteAction maps one API route pattern to a semantic inbox event.
// Path is the gin route pattern (e.g. "/api/v1/knowledge-bases/:id/knowledge/file").
type InboxRouteAction struct {
	Method    string
	Path      string
	EventType string
	// KBParam names the path parameter carrying the KB id. Empty for routes
	// where only a knowledge/document id is present — those resolve the KB
	// through the injected resolver (best-effort, one indexed lookup).
	KBParam string
	// KnowledgeParam names the path parameter carrying the knowledge id
	// (used both for the payload and for KB resolution when KBParam is
	// empty). Defaults to "id" when empty.
	KnowledgeParam string
}

// InboxUserActionCatalog covers the KB mutations a hot-starting agent
// benefits from knowing about. Body-driven routes (batch-delete, move,
// copy) are absent on purpose: the middleware only reads path params, and
// an event without a resolvable KB is invisible to the KB-scoped queries.
var InboxUserActionCatalog = []InboxRouteAction{
	// Document ingest
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
	// Document lifecycle (KB resolved from the knowledge id)
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge/:id/reparse",
		EventType: types.InboxTypeUserReparseDoc,
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge/batch-reparse",
		EventType: types.InboxTypeUserReparseDoc,
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge/:id/cancel-parse",
		EventType: types.InboxTypeUserCancelledParse,
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge/:id/regenerate-summary",
		EventType: types.InboxTypeUserReparseDoc,
	},
	{
		Method: http.MethodDelete, Path: "/api/v1/knowledge/:id",
		EventType: types.InboxTypeUserDeletedDoc,
	},
	{
		Method: http.MethodPut, Path: "/api/v1/knowledge/:id",
		EventType: types.InboxTypeUserUpdatedDoc,
	},
	{
		Method: http.MethodPut, Path: "/api/v1/knowledge/manual/:id",
		EventType: types.InboxTypeUserUpdatedDoc,
	},
	// Chunk content (chunk routes carry the KB directly)
	{
		Method: http.MethodPut, Path: "/api/v1/chunks/:knowledge_id/:id",
		EventType: types.InboxTypeUserEditChunk, KBParam: "knowledge_id",
		KnowledgeParam: "knowledge_id",
	},
	{
		Method: http.MethodDelete, Path: "/api/v1/chunks/:knowledge_id/:id",
		EventType: types.InboxTypeUserEditChunk, KBParam: "knowledge_id",
		KnowledgeParam: "knowledge_id",
	},
	{
		Method: http.MethodDelete, Path: "/api/v1/chunks/:knowledge_id",
		EventType: types.InboxTypeUserEditChunk, KBParam: "knowledge_id",
		KnowledgeParam: "knowledge_id",
	},
	{
		Method: http.MethodPost, Path: "/api/v1/chunks/:knowledge_id/:id/revert",
		EventType: types.InboxTypeUserEditChunk, KBParam: "knowledge_id",
		KnowledgeParam: "knowledge_id",
	},
	{
		Method: http.MethodPut, Path: "/api/v1/knowledge/image/:id/:chunk_id",
		EventType: types.InboxTypeUserEditChunk,
	},
	// Knowledge-base management
	{
		Method: http.MethodPut, Path: "/api/v1/knowledge-bases/:id",
		EventType: types.InboxTypeUserUpdatedKB, KBParam: "id",
	},
	{
		Method: http.MethodDelete, Path: "/api/v1/knowledge-bases/:id",
		EventType: types.InboxTypeUserDeletedKB, KBParam: "id",
	},
	// Wiki pages (KB resolved from the kb_id param)
	{
		Method: http.MethodPost, Path: "/api/v1/knowledgebase/:kb_id/wiki/pages",
		EventType: types.InboxTypeUserEditedWiki, KBParam: "kb_id",
	},
	{
		Method: http.MethodPut, Path: "/api/v1/knowledgebase/:kb_id/wiki/pages/*slug",
		EventType: types.InboxTypeUserEditedWiki, KBParam: "kb_id",
	},
	{
		Method: http.MethodPut, Path: "/api/v1/knowledgebase/:kb_id/wiki/move-page",
		EventType: types.InboxTypeUserEditedWiki, KBParam: "kb_id",
	},
	// FAQ entries
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge-bases/:id/faq/entries",
		EventType: types.InboxTypeUserEditedFAQ, KBParam: "id",
	},
	{
		Method: http.MethodPost, Path: "/api/v1/knowledge-bases/:id/faq/entry",
		EventType: types.InboxTypeUserEditedFAQ, KBParam: "id",
	},
	{
		Method: http.MethodPut, Path: "/api/v1/knowledge-bases/:id/faq/entries/:entry_id",
		EventType: types.InboxTypeUserEditedFAQ, KBParam: "id",
	},
	{
		Method: http.MethodDelete, Path: "/api/v1/knowledge-bases/:id/faq/entries",
		EventType: types.InboxTypeUserEditedFAQ, KBParam: "id",
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

// KnowledgeKBResolver maps a knowledge/document id to its knowledge-base id.
// Best-effort: a failed or empty resolution only degrades the event's KB
// scoping, never the request being proxied.
type KnowledgeKBResolver func(ctx context.Context, knowledgeID string) (string, error)

// AgentInboxUserAction captures user write operations into the agent inbox.
// Best-effort by design: the inbox never affects the proxied request.
func AgentInboxUserAction(
	inbox interfaces.AgentInboxService,
	resolveKnowledgeKB KnowledgeKBResolver,
) gin.HandlerFunc {
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
		knowledgeParam := action.KnowledgeParam
		if knowledgeParam == "" {
			knowledgeParam = "id"
		}
		knowledgeID := c.Param(knowledgeParam)
		if kbID == "" && knowledgeID != "" && resolveKnowledgeKB != nil {
			// One indexed lookup per catalog hit; these are rare user
			// mutations, never hot-path traffic.
			if resolved, err := resolveKnowledgeKB(c.Request.Context(), knowledgeID); err == nil {
				kbID = resolved
			}
		}
		payload := types.JSONMap{
			"route":     c.FullPath(),
			"method":    c.Request.Method,
			"dedup_key": "", // mutations are never debounced
		}
		// Attach the document id when the route carries one, so the agent
		// can follow up on the exact document ("user reparsed X").
		if knowledgeID != "" && knowledgeParam != action.KBParam {
			payload["knowledge_id"] = knowledgeID
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
