package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// EmbedChannelHandler manages web embed channel CRUD and public embed endpoints.
type EmbedChannelHandler struct {
	embedSvc        interfaces.EmbedChannelService
	sessionService  interfaces.SessionService
	sessionHandler  *session.Handler
	messageHandler  *MessageHandler
}

func NewEmbedChannelHandler(
	embedSvc interfaces.EmbedChannelService,
	sessionService interfaces.SessionService,
	sessionHandler *session.Handler,
	messageHandler *MessageHandler,
) *EmbedChannelHandler {
	return &EmbedChannelHandler{
		embedSvc:       embedSvc,
		sessionService: sessionService,
		sessionHandler: sessionHandler,
		messageHandler: messageHandler,
	}
}

type embedChannelRequest struct {
	Name               string   `json:"name"`
	AgentID            string   `json:"agent_id"`
	Enabled            *bool    `json:"enabled"`
	AllowedOrigins     []string `json:"allowed_origins"`
	WelcomeMessage     string   `json:"welcome_message"`
	RateLimitPerMinute int      `json:"rate_limit_per_minute"`
}

func (h *EmbedChannelHandler) CreateEmbedChannel(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	var req embedChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	originsJSON, _ := json.Marshal(req.AllowedOrigins)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	ch, token, err := h.embedSvc.Create(c.Request.Context(), tenantID, kbID, &types.EmbedChannel{
		Name:               req.Name,
		AgentID:            req.AgentID,
		Enabled:            enabled,
		AllowedOrigins:     originsJSON,
		WelcomeMessage:     req.WelcomeMessage,
		RateLimitPerMinute: req.RateLimitPerMinute,
	})
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    embedChannelResponse(ch, token),
	})
}

func (h *EmbedChannelHandler) ListEmbedChannels(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	rows, err := h.embedSvc.ListByKnowledgeBase(c.Request.Context(), tenantID, kbID)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	data := make([]gin.H, 0, len(rows))
	for _, ch := range rows {
		data = append(data, embedChannelResponse(ch, ""))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func (h *EmbedChannelHandler) UpdateEmbedChannel(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	var req embedChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	originsJSON, _ := json.Marshal(req.AllowedOrigins)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	ch, err := h.embedSvc.Update(c.Request.Context(), tenantID, channelID, &types.EmbedChannel{
		Name:               req.Name,
		AgentID:            req.AgentID,
		Enabled:            enabled,
		AllowedOrigins:     originsJSON,
		WelcomeMessage:     req.WelcomeMessage,
		RateLimitPerMinute: req.RateLimitPerMinute,
	})
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedChannelResponse(ch, "")})
}

func (h *EmbedChannelHandler) DeleteEmbedChannel(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if err := h.embedSvc.Delete(c.Request.Context(), tenantID, channelID); err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *EmbedChannelHandler) RotateEmbedToken(c *gin.Context) {
	channelID := secutils.SanitizeForLog(c.Param("channel_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	ch, token, err := h.embedSvc.RotateToken(c.Request.Context(), tenantID, channelID)
	if err != nil {
		writeEmbedMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedChannelResponse(ch, token)})
}

func (h *EmbedChannelHandler) GetEmbedConfig(c *gin.Context) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.embedSvc.PublicConfig(ch)})
}

func (h *EmbedChannelHandler) CreateEmbedSession(c *gin.Context) {
	ctx := c.Request.Context()
	ch, ok := middleware.EmbedChannelFromContext(ctx)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	title := strings.TrimSpace(ch.Name)
	if title == "" {
		title = "Embed Chat"
	}
	createdSession := &types.Session{
		TenantID:    tenantID,
		Title:       title,
		Description: service.EmbedSessionDescription(ch.ID),
	}
	if userID, ok := types.UserIDFromContext(ctx); ok {
		createdSession.UserID = userID
	}
	created, err := h.sessionService.CreateSession(ctx, createdSession)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": created})
}

func (h *EmbedChannelHandler) EmbedKnowledgeChat(c *gin.Context) {
	h.delegateEmbedChat(c, false)
}

func (h *EmbedChannelHandler) EmbedAgentChat(c *gin.Context) {
	h.delegateEmbedChat(c, true)
}

func (h *EmbedChannelHandler) EmbedLoadMessages(c *gin.Context) {
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	h.messageHandler.LoadMessages(c)
}

func (h *EmbedChannelHandler) delegateEmbedChat(c *gin.Context, agentMode bool) {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if err := h.ensureEmbedSession(c); err != nil {
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	var payload map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
			return
		}
	}
	if payload == nil {
		payload = make(map[string]any)
	}
	payload["knowledge_base_ids"] = []string{ch.KnowledgeBaseID}
	payload["agent_id"] = ch.AgentID
	payload["web_search_enabled"] = false
	payload["enable_memory"] = false
	payload["mcp_service_ids"] = []string{}
	if agentMode {
		payload["agent_enabled"] = true
	} else {
		payload["agent_enabled"] = false
	}
	patched, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare request"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(patched))
	c.Request.ContentLength = int64(len(patched))
	if agentMode && ch.AgentID != types.BuiltinQuickAnswerID {
		h.sessionHandler.AgentQA(c)
		return
	}
	h.sessionHandler.KnowledgeQA(c)
}

func (h *EmbedChannelHandler) ensureEmbedSession(c *gin.Context) error {
	ch, ok := middleware.EmbedChannelFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return apperrors.NewUnauthorizedError("unauthorized")
	}
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return apperrors.NewBadRequestError("session_id is required")
	}
	sess, err := h.sessionService.GetSession(c.Request.Context(), sessionID)
	if err != nil || sess == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return apperrors.NewNotFoundError("session not found")
	}
	marker := service.EmbedSessionDescription(ch.ID)
	if sess.TenantID != ch.TenantID || sess.Description != marker {
		c.JSON(http.StatusForbidden, gin.H{"error": "session not allowed for this embed channel"})
		return apperrors.NewForbiddenError("session not allowed")
	}
	return nil
}

func embedChannelResponse(ch *types.EmbedChannel, publishToken string) gin.H {
	row := gin.H{
		"id":                    ch.ID,
		"tenant_id":             ch.TenantID,
		"knowledge_base_id":     ch.KnowledgeBaseID,
		"agent_id":              ch.AgentID,
		"name":                  ch.Name,
		"enabled":               ch.Enabled,
		"allowed_origins":       ch.AllowedOriginsList(),
		"welcome_message":       ch.WelcomeMessage,
		"rate_limit_per_minute": ch.RateLimitPerMinute,
		"created_at":            ch.CreatedAt,
		"updated_at":            ch.UpdatedAt,
	}
	if publishToken != "" {
		row["publish_token"] = publishToken
	}
	return row
}

func writeEmbedMgmtError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrEmbedChannelNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "embed channel not found"})
	default:
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": appErr.Message})
			return
		}
		logger.Error(c.Request.Context(), "embed channel management failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
	}
}
