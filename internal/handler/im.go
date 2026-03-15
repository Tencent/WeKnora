package handler

import (
	"context"
	"net/http"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// IMHandler handles IM platform callback requests.
type IMHandler struct {
	imService *im.Service
	config    *config.Config
}

// NewIMHandler creates a new IM handler.
func NewIMHandler(imService *im.Service, cfg *config.Config) *IMHandler {
	return &IMHandler{
		imService: imService,
		config:    cfg,
	}
}

// WeComCallback handles WeCom callback requests (both URL verification and message events).
func (h *IMHandler) WeComCallback(c *gin.Context) {
	ctx := c.Request.Context()

	adapter, ok := h.imService.GetAdapter(im.PlatformWeCom)
	if !ok {
		logger.Error(ctx, "[IM] WeCom adapter not registered")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WeCom integration not enabled"})
		return
	}

	// Handle URL verification (GET request)
	if adapter.HandleURLVerification(c) {
		return
	}

	// Verify callback signature
	if err := adapter.VerifyCallback(c); err != nil {
		logger.Errorf(ctx, "[IM] WeCom callback verification failed: %v", err)
		c.JSON(http.StatusForbidden, gin.H{"error": "verification failed"})
		return
	}

	// Parse the callback message
	msg, err := adapter.ParseCallback(c)
	if err != nil {
		logger.Errorf(ctx, "[IM] WeCom parse callback failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse failed"})
		return
	}

	// If nil, it's a non-message event (e.g., system event) - just acknowledge
	if msg == nil {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Respond immediately to avoid WeCom timeout, process asynchronously
	c.JSON(http.StatusOK, gin.H{"success": true})

	// Get config for this platform
	wecomCfg := h.config.IM.WeCom

	// Detach from gin request context to prevent cancellation after HTTP response.
	asyncCtx := context.WithoutCancel(ctx)

	// Process message asynchronously
	go func() {
		if err := h.imService.HandleMessage(asyncCtx, msg, wecomCfg.TenantID, wecomCfg.AgentID, wecomCfg.KnowledgeBases); err != nil {
			logger.Errorf(asyncCtx, "[IM] WeCom handle message error: %v", err)
		}
	}()
}

// FeishuCallback handles Feishu callback requests (both URL verification and message events).
func (h *IMHandler) FeishuCallback(c *gin.Context) {
	ctx := c.Request.Context()

	adapter, ok := h.imService.GetAdapter(im.PlatformFeishu)
	if !ok {
		logger.Error(ctx, "[IM] Feishu adapter not registered")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Feishu integration not enabled"})
		return
	}

	// Handle URL verification (challenge)
	if adapter.HandleURLVerification(c) {
		return
	}

	// Verify callback
	if err := adapter.VerifyCallback(c); err != nil {
		logger.Errorf(ctx, "[IM] Feishu callback verification failed: %v", err)
		c.JSON(http.StatusForbidden, gin.H{"error": "verification failed"})
		return
	}

	// Parse the callback message
	msg, err := adapter.ParseCallback(c)
	if err != nil {
		logger.Errorf(ctx, "[IM] Feishu parse callback failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse failed"})
		return
	}

	if msg == nil {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Respond immediately
	c.JSON(http.StatusOK, gin.H{"success": true})

	// Get config
	feishuCfg := h.config.IM.Feishu

	// Detach from gin request context to prevent cancellation after HTTP response.
	asyncCtx := context.WithoutCancel(ctx)

	// Process asynchronously
	go func() {
		if err := h.imService.HandleMessage(asyncCtx, msg, feishuCfg.TenantID, feishuCfg.AgentID, feishuCfg.KnowledgeBases); err != nil {
			logger.Errorf(asyncCtx, "[IM] Feishu handle message error: %v", err)
		}
	}()
}
