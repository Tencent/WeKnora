package handler

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/meetingorchestration"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

type MeetingOrchestrationAPI interface {
	Start(context.Context) (model.MeetingOrchestrationJob, error)
	GetJob(context.Context, string) (model.MeetingOrchestrationJob, error)
	GetCurrent(context.Context) (*meetingorchestration.Projection, *model.MeetingOrchestrationCurrent, error)
}
type MeetingOrchestrationHandler struct{ service MeetingOrchestrationAPI }

func NewMeetingOrchestrationHandler(service MeetingOrchestrationAPI) *MeetingOrchestrationHandler {
	return &MeetingOrchestrationHandler{service: service}
}
func (h *MeetingOrchestrationHandler) Generate(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "会议主题簇服务尚未配置"})
		return
	}
	job, err := h.service.Start(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "启动会议主题簇生成失败"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": job})
}
func (h *MeetingOrchestrationHandler) Job(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "会议主题簇服务尚未配置"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "任务 ID 不能为空"})
		return
	}
	job, err := h.service.GetJob(c.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取会议任务状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": job})
}
func (h *MeetingOrchestrationHandler) Current(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "会议主题簇服务尚未配置"})
		return
	}
	doc, current, err := h.service.GetCurrent(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "读取会议主题簇失败"})
		return
	}
	if doc == nil {
		c.JSON(http.StatusOK, gin.H{"data": nil, "status": "empty"})
		return
	}
	status := "ready"
	if doc.Statistics.SkippedVideos > 0 {
		status = "partial"
	}
	c.JSON(http.StatusOK, gin.H{"data": doc, "current": current, "status": status})
}
