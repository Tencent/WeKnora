package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/trainingorchestration"
)

type TrainingOrchestrationAPI interface {
	Start(context.Context) (model.TrainingOrchestrationJob, error)
	GetJob(context.Context, string) (model.TrainingOrchestrationJob, error)
	GetCurrent(context.Context) (*trainingorchestration.ProjectionDocument, *model.TrainingOrchestrationCurrent, error)
}

type TrainingOrchestrationHandler struct{ service TrainingOrchestrationAPI }

func NewTrainingOrchestrationHandler(service TrainingOrchestrationAPI) *TrainingOrchestrationHandler {
	return &TrainingOrchestrationHandler{service: service}
}

func (h *TrainingOrchestrationHandler) Generate(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "培训学习路径服务尚未配置"})
		return
	}
	job, err := h.service.Start(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "启动培训学习路径生成失败", "detail": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": job})
}

func (h *TrainingOrchestrationHandler) Job(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "培训学习路径服务尚未配置"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取任务状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": job})
}

func (h *TrainingOrchestrationHandler) Current(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "培训学习路径服务尚未配置"})
		return
	}
	doc, current, err := h.service.GetCurrent(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "读取培训学习路径失败", "detail": err.Error()})
		return
	}
	if doc == nil {
		c.JSON(http.StatusOK, gin.H{"data": nil, "status": "empty"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": doc, "current": current, "status": "ready"})
}
