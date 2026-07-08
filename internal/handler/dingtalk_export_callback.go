package handler

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type DingTalkExportCallbackHandler struct {
	service interfaces.DingTalkExportService
	token   string
}

func NewDingTalkExportCallbackHandler(
	service interfaces.DingTalkExportService,
) *DingTalkExportCallbackHandler {
	return &DingTalkExportCallbackHandler{
		service: service,
		token:   strings.TrimSpace(os.Getenv("DINGTALK_EXPORT_CALLBACK_TOKEN")),
	}
}

func (h *DingTalkExportCallbackHandler) HandleExportFinish(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dingtalk export callback is not configured"})
		return
	}
	if h.token == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dingtalk export callback token is not configured"})
		return
	}
	if !h.validToken(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid callback token"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}
	if err := h.service.HandleExportFinishEvent(c.Request.Context(), body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *DingTalkExportCallbackHandler) validToken(c *gin.Context) bool {
	return c.Query("token") == h.token || c.GetHeader("X-DingTalk-Token") == h.token
}
