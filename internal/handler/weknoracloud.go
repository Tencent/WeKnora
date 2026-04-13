package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// WeKnoraCloudHandler 处理 WeKnoraCloud 初始化请求
type WeKnoraCloudHandler struct {
	svc interfaces.WeKnoraCloudService
}

// NewWeKnoraCloudHandler 构造函数
func NewWeKnoraCloudHandler(svc interfaces.WeKnoraCloudService) *WeKnoraCloudHandler {
	return &WeKnoraCloudHandler{svc: svc}
}

type initializeWeKnoraCloudRequest struct {
	AppID     string `json:"app_id"     binding:"required"`
	AppSecret string `json:"app_secret" binding:"required"`
}

// Initialize POST /api/v1/models/weknoracloud/initialize
func (h *WeKnoraCloudHandler) Initialize(c *gin.Context) {
	var req initializeWeKnoraCloudRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.svc.Initialize(c.Request.Context(), req.AppID, req.AppSecret)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Status GET /api/v1/models/weknoracloud/status
// 检查当前租户的 WeKnoraCloud 凭证是否完好，如需重新初始化则返回 needs_reinit=true
func (h *WeKnoraCloudHandler) Status(c *gin.Context) {
	result, err := h.svc.CheckStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
