package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// AgentInboxHandler receives UI-side agent-inbox signals from the
// knowledge-base view (view tracking, error toasts). See
// works/agent-sidebar-inbox/plan.md §4.3: views are captured by the
// frontend because only it knows the semantic view context (slug, tab);
// mutations are captured by the write-op middleware instead.
//
// Authorization model mirrors the favorites handler: (user, tenant) always
// come from the auth context, the KB id comes from the route and the caller
// must at least be a Viewer of that KB (route guard). Events are stored with
// the caller's identity — a user can never write into someone else's trail.
type AgentInboxHandler struct {
	inbox interfaces.AgentInboxService
}

// NewAgentInboxHandler constructs the handler.
func NewAgentInboxHandler(inbox interfaces.AgentInboxService) *AgentInboxHandler {
	return &AgentInboxHandler{inbox: inbox}
}

// inboxUIContext resolves (userID, tenantID) like favoriteContext.
func inboxUIContext(c *gin.Context) (string, uint64, bool) {
	uidVal, ok := c.Get(types.UserIDContextKey.String())
	if !ok {
		_ = c.Error(apperrors.NewUnauthorizedError("user ID not found"))
		return "", 0, false
	}
	userID, _ := uidVal.(string)
	if userID == "" {
		_ = c.Error(apperrors.NewUnauthorizedError("user ID not found"))
		return "", 0, false
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		_ = c.Error(apperrors.NewUnauthorizedError("workspace ID not found"))
		return "", 0, false
	}
	return userID, tenantID, true
}

// inboxUIEventRequest is the accepted UI signal body. Extra keys in detail
// are preserved into the payload so the frontend can enrich events without
// a backend change per field.
type inboxUIEventRequest struct {
	Type   string         `json:"type"`
	Detail map[string]any `json:"detail"`
}

// allowedUIEventTypes bounds what the UI can inject. Anything else is
// rejected — the inbox is curated, not a client-side free-for-all.
var allowedUIEventTypes = map[string]bool{
	types.InboxTypeUserViewedPage:     true,
	types.InboxTypeUserSwitchedTab:    true,
	types.InboxTypeUserErrorToast:     true,
	types.InboxTypeUserViewedDocument: true,
	types.InboxTypeUserViewedImage:    true,
	types.InboxTypeUserSwitchedKB:     true,
}

// PostUIEvent godoc
// @Summary      Report a UI signal to the agent inbox
// @Description  Records one UI-side event (page view, tab switch, error toast) for the agent inbox
// @Tags         AgentInbox
// @Param        id    path  string                 true  "Knowledge base ID"
// @Param        body  body  handler.inboxUIEventRequest true "Event"
// @Success      200   {object}  map[string]interface{}
// @Router       /knowledge-bases/{id}/ui-events [post]
func (h *AgentInboxHandler) PostUIEvent(c *gin.Context) {
	userID, tenantID, ok := inboxUIContext(c)
	if !ok {
		return
	}
	kbID := c.Param("id")
	if kbID == "" {
		_ = c.Error(apperrors.NewBadRequestError("knowledge base id is required"))
		return
	}

	var req inboxUIEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid ui event body: " + err.Error()))
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if !allowedUIEventTypes[req.Type] {
		_ = c.Error(apperrors.NewBadRequestError("ui event type is not accepted: " + req.Type))
		return
	}

	payload := types.JSONMap{}
	for k, v := range req.Detail {
		payload[k] = v
	}
	// dedup_key drives the policy debounce (consecutive views of the same
	// page collapse); default it to the slug for view events.
	if _, ok := payload["dedup_key"]; !ok && req.Type == types.InboxTypeUserViewedPage {
		if slug, _ := payload["slug"].(string); slug != "" {
			payload["dedup_key"] = slug
		}
	}

	h.inbox.Ingest(c.Request.Context(), &types.AgentInboxEvent{
		TenantID: tenantID,
		KBID:     kbID,
		UserID:   userID,
		Type:     req.Type,
		Payload:  payload,
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// inboxMarkReadRequest is the mark-read body.
type inboxMarkReadRequest struct {
	IDs []string `json:"ids"`
}

// inboxCallerUserID extracts the caller's user id; the panel shows their
// own user-scoped events plus tenant-wide ones (user_id=”).
func inboxCallerUserID(c *gin.Context) string {
	v, _ := c.Get(types.UserIDContextKey.String())
	s, _ := v.(string)
	return s
}

// parseQueryInt reads an int query param with a fallback default.
func parseQueryInt(c *gin.Context, key string, def int) int {
	raw := c.Query(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// splitCSVQuery splits a comma-separated query param into trimmed non-empty parts.
func splitCSVQuery(c *gin.Context, key string) []string {
	raw := c.Query(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ListInboxEvents godoc
// @Summary      Read the agent inbox (user-facing panel)
// @Description  Returns recent events visible to the caller: their own
// @Description  user-scoped events plus tenant-wide ones (parse lifecycle,
// @Description  external ingest). Read-only — nothing is marked read.
// @Tags         AgentInbox
// @Param        since_minutes  query  int    false  "Lookback window minutes (default 1440, max 10080)"
// @Param        types          query  string false  "Comma-separated event type filter"
// @Param        kb_ids         query  string false  "Comma-separated KB scope filter"
// @Param        unread_only    query  bool   false  "Only unread events"
// @Param        limit          query  int    false  "Max events (default 50, max 100)"
// @Success      200  {object}  map[string]interface{}
// @Router       /agent-inbox/events [get]
func (h *AgentInboxHandler) ListInboxEvents(c *gin.Context) {
	_, tenantID, ok := inboxUIContext(c)
	if !ok {
		return
	}

	sinceMinutes := parseQueryInt(c, "since_minutes", 24*60)
	if sinceMinutes <= 0 || sinceMinutes > 7*24*60 {
		sinceMinutes = 24 * 60
	}
	limit := parseQueryInt(c, "limit", 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	since := time.Now().Add(-time.Duration(sinceMinutes) * time.Minute)

	f := types.InboxQueryFilters{
		TenantID:   tenantID,
		UserID:     inboxCallerUserID(c),
		Types:      splitCSVQuery(c, "types"),
		KBIDs:      splitCSVQuery(c, "kb_ids"),
		Since:      &since,
		Limit:      limit,
		UnreadOnly: c.Query("unread_only") == "true",
	}
	// markRead=false: the panel is read-only; marking happens through the
	// explicit /read endpoint so a refresh never silently consumes unread.
	digests, unread, err := h.inbox.Check(c.Request.Context(), f, false)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "events": digests, "unread": unread})
}

// MarkInboxRead godoc
// @Summary      Mark inbox events as read
// @Tags         AgentInbox
// @Param        body  body  handler.inboxMarkReadRequest true "Event IDs"
// @Success      200   {object}  map[string]interface{}
// @Router       /agent-inbox/events/read [post]
func (h *AgentInboxHandler) MarkInboxRead(c *gin.Context) {
	if _, _, ok := inboxUIContext(c); !ok {
		return
	}
	var req inboxMarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		_ = c.Error(apperrors.NewBadRequestError("ids is required"))
		return
	}
	if err := h.inbox.MarkRead(c.Request.Context(), req.IDs); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
