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
var validIMPlatforms = map[string]bool{
	"wecom": true, "feishu": true, "slack": true, "telegram": true, "dingtalk": true, "mattermost": true,
	"wechat": true,
}

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
		Platform        string     `json:"platform" binding:"required"`
		Name            string     `json:"name"`
		Mode            string     `json:"mode"`
		OutputMode      string     `json:"output_mode"`
		KnowledgeBaseID string     `json:"knowledge_base_id"`
		Credentials     types.JSON `json:"credentials"`
		Enabled         *bool      `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !validIMPlatforms[req.Platform] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "platform must be 'wecom', 'feishu', 'slack', 'telegram', 'dingtalk', 'mattermost' or 'wechat'"})
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
	// WeChat uses long-polling mode and full output only
	if req.Platform == "wechat" {
		channel.Mode = "longpoll"
		channel.OutputMode = "full"
	} else {
		if channel.Mode == "" {
			if channel.Platform == "mattermost" {
				channel.Mode = "webhook"
			} else {
				channel.Mode = "websocket"
			}
		}
		if channel.OutputMode == "" {
			channel.OutputMode = "stream"
		}
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

// ListAllIMChannels lists every IM channel in the current tenant, across
// agents, for the cross-agent overview page. Credentials are intentionally
// NOT included in the response — callers that need credentials must use the
// per-agent endpoint (GET /agents/:id/im-channels).
func (h *IMHandler) ListAllIMChannels(c *gin.Context) {
	tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	channels, err := h.imService.ListChannelsByTenant(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list channels"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": channels})
}

// UpdateIMChannel updates an IM channel.
//
// UpdateIMChannel godoc
// @Summary      更新 IM 渠道
// @Description  更新指定 IM 渠道的名称、模式、知识库、凭证或启用状态
// @Tags         IM 渠道
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "渠道 ID"
// @Param        request  body      map[string]interface{}  true  "更新字段（name/mode/output_mode/knowledge_base_id/credentials/enabled）"
// @Success      200      {object}  map[string]interface{}  "更新后的渠道"
// @Failure      400      {object}  map[string]interface{}  "请求参数错误"
// @Failure      404      {object}  map[string]interface{}  "渠道不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /im-channels/{id} [put]
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
//
// DeleteIMChannel godoc
// @Summary      删除 IM 渠道
// @Description  删除指定 IM 渠道
// @Tags         IM 渠道
// @Produce      json
// @Param        id   path      string                  true  "渠道 ID"
// @Success      200  {object}  map[string]interface{}  "success: true"
// @Failure      400  {object}  map[string]interface{}  "请求参数错误"
// @Failure      404  {object}  map[string]interface{}  "渠道不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /im-channels/{id} [delete]
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
//
// ToggleIMChannel godoc
// @Summary      启用/停用 IM 渠道
// @Description  切换指定 IM 渠道的启用状态
// @Tags         IM 渠道
// @Produce      json
// @Param        id   path      string                  true  "渠道 ID"
// @Success      200  {object}  map[string]interface{}  "更新后的渠道"
// @Failure      400  {object}  map[string]interface{}  "请求参数错误"
// @Failure      404  {object}  map[string]interface{}  "渠道不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /im-channels/{id}/toggle [post]
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
//
// IMCallback godoc
// @Summary      IM 平台回调
// @Description  接收各 IM 平台的事件回调；走平台自身签名校验，不使用 API Key
// @Tags         IM 回调
// @Accept       json
// @Produce      json
// @Param        channel_id  path      string                  true  "渠道 ID"
// @Success      200         {object}  map[string]interface{}  "处理结果"
// @Failure      400         {object}  map[string]interface{}  "请求参数错误"
// @Failure      401         {object}  map[string]interface{}  "签名校验失败"
// @Router       /im/callback/{channel_id} [get]
// @Router       /im/callback/{channel_id} [post]
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

	logger.Infof(ctx, "[IM] Callback received platform=%s path_channel_id=%s", channel.Platform, channelID)

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
		if channel.Platform == "mattermost" {
			logger.Infof(ctx, "[IM] Mattermost callback ignored (no message): path_channel_id=%s — check: (1) trigger word must be the *first word* of the post; (2) if channel+trigger are both set, post must be in that channel; (3) bot_user_id must not match the sender", channelID)
		} else {
			logger.Infof(ctx, "[IM] Callback parsed no message to process platform=%s path_channel_id=%s", channel.Platform, channelID)
		}
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
