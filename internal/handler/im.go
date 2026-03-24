package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// validIMPlatforms is the set of supported IM platforms.
var validIMPlatforms = map[string]bool{"wecom": true, "feishu": true, "slack": true, "telegram": true}

// IMHandler handles IM platform callback requests and channel CRUD.
type IMHandler struct {
	imService *im.Service
}

// NewIMHandler creates a new IM handler.
func NewIMHandler(imService *im.Service) *IMHandler {
	return &IMHandler{
		imService: imService,
	}
}

// ── Channel CRUD handlers ──

// CreateIMChannel creates a new IM channel for an agent.
func (h *IMHandler) CreateIMChannel(c *gin.Context) {
	agentID := c.Param("id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent_id is required"})
		return
	}

	tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Platform        string         `json:"platform" binding:"required"`
		Name            string         `json:"name"`
		Mode            string         `json:"mode"`
		OutputMode      string         `json:"output_mode"`
		KnowledgeBaseID string         `json:"knowledge_base_id"`
		Credentials     types.JSON     `json:"credentials"`
		Enabled         *bool          `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !validIMPlatforms[req.Platform] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "platform must be 'wecom', 'feishu', 'slack' or 'telegram'"})
		return
	}

	channel := &im.IMChannel{
		TenantID:        tenantID,
		AgentID:         agentID,
		Platform:        req.Platform,
		Name:            req.Name,
		Mode:            req.Mode,
		OutputMode:      req.OutputMode,
		KnowledgeBaseID: req.KnowledgeBaseID,
		Credentials:     req.Credentials,
		Enabled:         true,
	}
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}
	if channel.Mode == "" {
		channel.Mode = "websocket"
	}
	if channel.OutputMode == "" {
		channel.OutputMode = "stream"
	}
	if channel.Credentials == nil {
		channel.Credentials = types.JSON("{}")
	}

	if err := h.imService.CreateChannel(channel); err != nil {
		logger.Errorf(c.Request.Context(), "[IM] Create channel failed: %v", err)
		if strings.HasPrefix(err.Error(), "duplicate_bot:") {
			c.JSON(http.StatusConflict, gin.H{"error": strings.TrimPrefix(err.Error(), "duplicate_bot: ")})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create channel"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": channel})
}

// ListIMChannels lists all IM channels for an agent.
func (h *IMHandler) ListIMChannels(c *gin.Context) {
	agentID := c.Param("id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent_id is required"})
		return
	}

	tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	channels, err := h.imService.ListChannelsByAgent(agentID, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list channels"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": channels})
}

// UpdateIMChannel updates an IM channel.
func (h *IMHandler) UpdateIMChannel(c *gin.Context) {
	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel id is required"})
		return
	}

	tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	channel, err := h.imService.GetChannelByIDAndTenant(channelID, tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
		return
	}

	var req struct {
		Name            *string    `json:"name"`
		Mode            *string    `json:"mode"`
		OutputMode      *string    `json:"output_mode"`
		KnowledgeBaseID *string    `json:"knowledge_base_id"`
		Credentials     types.JSON `json:"credentials"`
		Enabled         *bool      `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != nil {
		channel.Name = *req.Name
	}
	if req.Mode != nil {
		channel.Mode = *req.Mode
	}
	if req.OutputMode != nil {
		channel.OutputMode = *req.OutputMode
	}
	if req.KnowledgeBaseID != nil {
		channel.KnowledgeBaseID = *req.KnowledgeBaseID
	}
	if req.Credentials != nil {
		channel.Credentials = req.Credentials
	}
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}

	if err := h.imService.UpdateChannel(channel); err != nil {
		logger.Errorf(c.Request.Context(), "[IM] Update channel failed: %v", err)
		if strings.HasPrefix(err.Error(), "duplicate_bot:") {
			c.JSON(http.StatusConflict, gin.H{"error": strings.TrimPrefix(err.Error(), "duplicate_bot: ")})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update channel"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": channel})
}

// DeleteIMChannel deletes an IM channel.
func (h *IMHandler) DeleteIMChannel(c *gin.Context) {
	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel id is required"})
		return
	}

	tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.imService.DeleteChannel(channelID, tenantID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete channel"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ToggleIMChannel toggles the enabled state of an IM channel.
func (h *IMHandler) ToggleIMChannel(c *gin.Context) {
	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel id is required"})
		return
	}

	tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	channel, err := h.imService.ToggleChannel(channelID, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to toggle channel"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": channel})
}

// ── Callback handlers ──

// IMCallback handles IM platform callback requests for a specific channel.
// Route: POST /api/v1/im/callback/:channel_id
func (h *IMHandler) IMCallback(c *gin.Context) {
	ctx := c.Request.Context()
	channelID := c.Param("channel_id")

	adapter, channel, ok := h.imService.GetChannelAdapter(channelID)
	if !ok {
		// Try loading from DB
		ch, err := h.imService.GetChannelByID(channelID)
		if err != nil {
			logger.Errorf(ctx, "[IM] Channel not found for callback: %s", channelID)
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			return
		}
		if err := h.imService.StartChannel(ch); err != nil {
			logger.Errorf(ctx, "[IM] Failed to start channel for callback: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "channel not available"})
			return
		}
		adapter, channel, ok = h.imService.GetChannelAdapter(channelID)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "channel not available"})
			return
		}
	}

	if !channel.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "channel is disabled"})
		return
	}

	// Handle URL verification
	if adapter.HandleURLVerification(c) {
		return
	}

	// Verify callback signature
	if err := adapter.VerifyCallback(c); err != nil {
		logger.Errorf(ctx, "[IM] Callback verification failed for channel %s: %v", channelID, err)
		c.JSON(http.StatusForbidden, gin.H{"error": "verification failed"})
		return
	}

	// Parse the callback message
	msg, err := adapter.ParseCallback(c)
	if err != nil {
		logger.Errorf(ctx, "[IM] Parse callback failed for channel %s: %v", channelID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse failed"})
		return
	}

	// If nil, it's a non-message event - just acknowledge
	if msg == nil {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Respond immediately to avoid platform timeout
	c.JSON(http.StatusOK, gin.H{"success": true})

	// Detach from gin request context
	asyncCtx := context.WithoutCancel(ctx)

	// Process message asynchronously
	go func() {
		if err := h.imService.HandleMessage(asyncCtx, msg, channelID); err != nil {
			logger.Errorf(asyncCtx, "[IM] Handle message error for channel %s: %v", channelID, err)
		}
	}()
}
