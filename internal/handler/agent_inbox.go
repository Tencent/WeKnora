package handler

import (
	"net/http"
	"strings"

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
	types.InboxTypeUserViewedPage:  true,
	types.InboxTypeUserSwitchedTab: true,
	types.InboxTypeUserErrorToast:  true,
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
