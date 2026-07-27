package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type SystemAgentCollectionHandler struct {
	service   interfaces.AgentCollectionService
	agents    interfaces.CustomAgentService
	audit     interfaces.AuditLogService
	exportDir string
}

func NewSystemAgentCollectionHandler(
	service interfaces.AgentCollectionService,
	agents interfaces.CustomAgentService,
	audit interfaces.AuditLogService,
) *SystemAgentCollectionHandler {
	return &SystemAgentCollectionHandler{service: service, agents: agents, audit: audit}
}

func (h *SystemAgentCollectionHandler) ListProfiles(c *gin.Context) {
	filter, err := agentCollectionFilterFromQuery(c)
	if err != nil {
		collectionError(c, http.StatusBadRequest, err.Error())
		return
	}
	page, err := h.service.ListProfiles(c.Request.Context(), filter)
	if err != nil {
		collectionError(c, http.StatusInternalServerError, "failed to list collection profiles")
		return
	}
	summary, err := h.service.SummarizeProfiles(c.Request.Context(), filter)
	if err != nil {
		collectionError(c, http.StatusInternalServerError, "failed to summarize collection profiles")
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, profile := range page.Items {
		items = append(items, h.profileView(c.Request.Context(), profile, false))
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "total": page.Total, "page": page.Page, "page_size": page.PageSize, "summary": summary,
	})
}

func (h *SystemAgentCollectionHandler) GetProfile(c *gin.Context) {
	profile, err := h.service.GetProfileByID(c.Request.Context(), c.Param("profile_id"))
	if err != nil {
		collectionError(c, http.StatusNotFound, "collection profile not found")
		return
	}
	h.auditAction(c, types.AuditActionAgentCollectionViewed, profile, nil)
	c.JSON(http.StatusOK, h.profileView(c.Request.Context(), profile, true))
}

func (h *SystemAgentCollectionHandler) ListHistory(c *gin.Context) {
	page, err := optionalPositiveInt(c.Query("page"), 1)
	if err != nil {
		collectionError(c, http.StatusBadRequest, "invalid page")
		return
	}
	pageSize, err := optionalPositiveInt(c.Query("page_size"), 20)
	if err != nil {
		collectionError(c, http.StatusBadRequest, "invalid page_size")
		return
	}
	profile, err := h.service.GetProfileByID(c.Request.Context(), c.Param("profile_id"))
	if err != nil {
		collectionError(c, http.StatusNotFound, "collection profile not found")
		return
	}
	history, err := h.service.ListHistory(c.Request.Context(), profile.ID, page, pageSize)
	if err != nil {
		collectionError(c, http.StatusInternalServerError, "failed to list collection history")
		return
	}
	h.auditAction(c, types.AuditActionAgentCollectionViewed, profile, gin.H{"view": "history"})
	c.JSON(http.StatusOK, history)
}

func (h *SystemAgentCollectionHandler) UpdateField(c *gin.Context) {
	var body struct {
		Value  any    `json:"value"`
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&body) != nil || strings.TrimSpace(body.Reason) == "" {
		collectionError(c, http.StatusBadRequest, "value and reason are required")
		return
	}
	profile, agent, ok := h.profileAndAgent(c)
	if !ok {
		return
	}
	actor, _ := types.UserIDFromContext(c.Request.Context())
	updated, err := h.service.UpdateAsSystemAdmin(c.Request.Context(), types.SystemAdminCollectionUpdateInput{
		ProfileID: profile.ID, Config: agent.Config, FieldKey: c.Param("field_key"), Value: body.Value,
		ActorUserID: actor, ChangeReason: strings.TrimSpace(body.Reason),
	})
	if err != nil {
		collectionError(c, http.StatusBadRequest, err.Error())
		return
	}
	h.auditAction(c, types.AuditActionAgentCollectionUpdated, profile, gin.H{
		"field_key": c.Param("field_key"), "reason": strings.TrimSpace(body.Reason),
	})
	c.JSON(http.StatusOK, updated)
}

func (h *SystemAgentCollectionHandler) PurgeProfile(c *gin.Context) {
	var body struct {
		Confirmation string `json:"confirmation"`
		Reason       string `json:"reason"`
	}
	profileID := c.Param("profile_id")
	if c.ShouldBindJSON(&body) != nil || body.Confirmation != profileID || strings.TrimSpace(body.Reason) == "" {
		collectionError(c, http.StatusBadRequest, "profile id confirmation and reason are required")
		return
	}
	profile, err := h.service.GetProfileByID(c.Request.Context(), profileID)
	if err != nil {
		collectionError(c, http.StatusNotFound, "collection profile not found")
		return
	}
	if err := h.service.PurgeProfile(c.Request.Context(), profileID); err != nil {
		collectionError(c, http.StatusInternalServerError, "failed to purge collection profile")
		return
	}
	h.auditAction(c, types.AuditActionAgentCollectionPurged, profile, gin.H{"reason": strings.TrimSpace(body.Reason)})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *SystemAgentCollectionHandler) profileAndAgent(c *gin.Context) (*types.AgentCollectionProfile, *types.CustomAgent, bool) {
	profile, err := h.service.GetProfileByID(c.Request.Context(), c.Param("profile_id"))
	if err != nil {
		collectionError(c, http.StatusNotFound, "collection profile not found")
		return nil, nil, false
	}
	agent, err := h.agents.GetAgentByIDAndTenant(c.Request.Context(), profile.AgentID, profile.AgentTenantID)
	if err != nil {
		collectionError(c, http.StatusNotFound, "collection agent not found")
		return nil, nil, false
	}
	return profile, agent, true
}

func (h *SystemAgentCollectionHandler) profileView(
	ctx context.Context,
	profile *types.AgentCollectionProfile,
	detail bool,
) gin.H {
	view := gin.H{
		"id": profile.ID, "tenant_id": profile.TenantID, "agent_tenant_id": profile.AgentTenantID,
		"agent_id": profile.AgentID, "user_id": profile.UserID, "schema_version": profile.SchemaVersion,
		"values": profile.Values, "required_total": profile.RequiredTotal,
		"completed_required": profile.CompletedRequired, "is_complete": profile.IsComplete,
		"created_at": profile.CreatedAt, "updated_at": profile.UpdatedAt,
		"agent_name": "", "fields": []types.AgentCollectionField{},
	}
	if agent, err := h.agents.GetAgentByIDAndTenant(ctx, profile.AgentID, profile.AgentTenantID); err == nil {
		view["agent_name"] = agent.Name
		view["fields"] = agent.Config.CollectionFields
		view["collection_goal"] = agent.Config.CollectionGoal
	}
	if detail {
		view["inactive_values"] = profile.InactiveValues
	}
	return view
}

func (h *SystemAgentCollectionHandler) auditAction(
	c *gin.Context,
	action types.AuditAction,
	profile *types.AgentCollectionProfile,
	details gin.H,
) {
	if h.audit == nil || profile == nil {
		return
	}
	detailsJSON, _ := json.Marshal(details)
	actor, _ := types.UserIDFromContext(c.Request.Context())
	_ = h.audit.Log(c.Request.Context(), &types.AuditLog{
		TenantID: 0, ActorUserID: actor, ActorRole: "system_admin", Action: action,
		TargetType: "agent_collection_profile", TargetID: profile.ID, TargetUserID: profile.UserID,
		RequestPath: c.Request.URL.Path, RequestMethod: c.Request.Method,
		Outcome: types.AuditOutcomeSuccess, Details: types.JSON(detailsJSON),
	})
}

func collectionError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": message})
}
