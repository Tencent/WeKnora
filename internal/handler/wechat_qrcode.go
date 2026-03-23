package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/im/wechat"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// qrCodeService is a singleton for WeChat QR code operations.
var qrCodeService = wechat.NewQRCodeService()

// WeChatGetQRCode generates a QR code for WeChat login.
// POST /api/v1/wechat/qrcode
func (h *IMHandler) WeChatGetQRCode(c *gin.Context) {
	ctx := c.Request.Context()

	result, err := qrCodeService.GetLoginQRCode(ctx)
	if err != nil {
		logger.Errorf(ctx, "[WeChat] Failed to generate QR code: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate QR code: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"qrcode_url": result.QRCodeURL,
			"qrcode":     result.QRCode,
		},
	})
}

// WeChatPollQRCodeStatus checks the scan status of a WeChat QR code.
// POST /api/v1/wechat/qrcode/status
func (h *IMHandler) WeChatPollQRCodeStatus(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		QRCode string `json:"qrcode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "qrcode is required"})
		return
	}

	result, err := qrCodeService.PollQRCodeStatus(ctx, req.QRCode)
	if err != nil {
		logger.Errorf(ctx, "[WeChat] Failed to poll QR code status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check QR code status"})
		return
	}

	resp := gin.H{
		"status": result.Status,
	}

	// Only include credentials when login is confirmed
	if result.Status == "confirmed" {
		resp["credentials"] = gin.H{
			"bot_token":     result.BotToken,
			"ilink_bot_id":  result.ILinkBotID,
			"ilink_user_id": result.ILinkUserID,
		}
		// Include baseurl if the server returned one (may override default)
		if result.BaseURL != "" {
			resp["baseurl"] = result.BaseURL
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}
