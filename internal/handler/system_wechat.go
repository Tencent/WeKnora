package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type weChatSettingsService interface {
	GetSettings(context.Context) (*types.WeChatSettingsResponse, error)
	SaveSettings(context.Context, *types.WeChatSettingsUpdate) (*types.WeChatSettingsResponse, error)
}

// BindSystemHandler shares the runtime configuration service with system administration.
func (h *WeChatAuthHandler) BindSystemHandler(system *SystemHandler) {
	if h != nil && system != nil {
		if settings, ok := h.service.(weChatSettingsService); ok {
			system.weChatSettings = settings
		}
	}
}

// GetWeChatSettings returns metadata only. The group-level SystemAdmin guard
// applies to this endpoint and its update counterpart.
func (h *SystemHandler) GetWeChatSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.weChatSettings == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WeChat settings unavailable"})
		return
	}
	result, err := h.weChatSettings.GetSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WeChat settings unavailable"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// UpdateWeChatSettings saves a complete provider configuration and emits a redacted audit event.
func (h *SystemHandler) UpdateWeChatSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.weChatSettings == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WeChat settings unavailable"})
		return
	}
	var req types.WeChatSettingsUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid WeChat settings"})
		return
	}
	result, err := h.weChatSettings.SaveSettings(c.Request.Context(), &req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrWeChatSettingsConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, service.ErrWeChatUnavailable) {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	// Deliberately omit the request: it contains write-only credentials.
	if h.auditSvc != nil {
		actor, _ := types.UserIDFromContext(c.Request.Context())
		_ = h.auditSvc.Log(c.Request.Context(), &types.AuditLog{
			TenantID: 0, ActorUserID: actor,
			ActorRole: systemAuditActorRole(c.Request.Context()), Action: types.AuditActionWeChatAuthUpdated,
			TargetType: "auth_provider", TargetID: "wechat", Outcome: types.AuditOutcomeSuccess,
		})
	}
	c.JSON(http.StatusOK, result)
}
