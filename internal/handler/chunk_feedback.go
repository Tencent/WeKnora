package handler

import (
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ChunkFeedbackHandler 片段反馈处理器
type ChunkFeedbackHandler struct {
	feedbackService *ChunkFeedbackService
	messageRepo     MessageRepository
}

// NewChunkFeedbackHandler 创建反馈处理器
func NewChunkFeedbackHandler(
	feedbackService *ChunkFeedbackService,
	messageRepo MessageRepository,
) *ChunkFeedbackHandler {
	return &ChunkFeedbackHandler{
		feedbackService: feedbackService,
		messageRepo:     messageRepo,
	}
}

// SubmitFeedback godoc
// @Summary 提交问答反馈
// @Description 用户对问答回复提交点赞/点踩反馈
// @Tags 反馈
// @Accept json
// @Produce json
// @Param request body types.SubmitFeedbackRequest true "反馈请求"
// @Success 200 {object} map[string]interface{} "提交成功"
// @Failure 400 {object} errors.AppError "请求参数错误"
// @Failure 500 {object} errors.AppError "服务器错误"
// @Security Bearer
// @Router /feedback [post]
func (h *ChunkFeedbackHandler) SubmitFeedback(c *gin.Context) {
	var req types.SubmitFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if req.MessageID == "" {
		c.Error(errors.NewBadRequestError("message_id is required"))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	userID := ""
	if uid, exists := c.Get(types.UserIDContextKey.String()); exists {
		userID = uid.(string)
	}

	// 获取 sessionID
	session, err := h.messageRepo.GetMessageByID(c.Request.Context(), req.MessageID)
	sessionID := ""
	if err == nil && session != nil {
		sessionID = session.SessionID
	}

	if err := h.feedbackService.SubmitFeedback(c.Request.Context(), tenantID, userID, sessionID, &req); err != nil {
		logger.Errorf(c.Request.Context(), "Failed to submit feedback: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "反馈提交成功",
	})
}

// GetFeedbackStatus godoc
// @Summary 获取反馈状态
// @Description 获取当前用户对指定消息的反馈状态
// @Tags 反馈
// @Produce json
// @Param message_id path string true "消息ID"
// @Success 200 {object} map[string]interface{} "反馈状态"
// @Security Bearer
// @Router /feedback/{message_id}/status [get]
func (h *ChunkFeedbackHandler) GetFeedbackStatus(c *gin.Context) {
	messageID := c.Param("message_id")
	if messageID == "" {
		c.Error(errors.NewBadRequestError("message_id is required"))
		return
	}

	userID := ""
	if uid, exists := c.Get(types.UserIDContextKey.String()); exists {
		userID = uid.(string)
	}

	// 这里简化实现，实际应从 repository 获取
	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"message_id":  messageID,
		"user_id":     userID,
		"is_positive": false,
	})
}

// GetChunkStats godoc
// @Summary 获取片段统计
// @Description 获取指定片段的反馈统计数据
// @Tags 反馈
// @Produce json
// @Param chunk_id path string true "片段ID"
// @Success 200 {object} map[string]interface{} "统计数据"
// @Failure 400 {object} errors.AppError "请求参数错误"
// @Failure 500 {object} errors.AppError "服务器错误"
// @Security Bearer
// @Router /chunks/{chunk_id}/stats [get]
func (h *ChunkFeedbackHandler) GetChunkStats(c *gin.Context) {
	chunkID := c.Param("chunk_id")
	if chunkID == "" {
		c.Error(errors.NewBadRequestError("chunk_id is required"))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	stats, err := h.feedbackService.GetChunkStats(c.Request.Context(), tenantID, chunkID)
	if err != nil {
		logger.Errorf(c.Request.Context(), "Failed to get chunk stats: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// ListLowQualityChunks godoc
// @Summary 列出低质量片段
// @Description 按好评率筛选低质量片段
// @Tags 反馈
// @Produce json
// @Param max_rate query number false "最高好评率" default(0.5)
// @Param limit query int false "返回数量" default(20)
// @Param offset query int false "偏移量" default(0)
// @Success 200 {object} map[string]interface{} "片段列表"
// @Failure 500 {object} errors.AppError "服务器错误"
// @Security Bearer
// @Router /chunks/low-quality [get]
func (h *ChunkFeedbackHandler) ListLowQualityChunks(c *gin.Context) {
	maxRate, _ := strconv.ParseFloat(c.DefaultQuery("max_rate", "0.5"), 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	chunks, err := h.feedbackService.ListLowQualityChunks(c.Request.Context(), tenantID, maxRate, limit, offset)
	if err != nil {
		logger.Errorf(c.Request.Context(), "Failed to list low quality chunks: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    chunks,
	})
}

// ResetChunkFeedback godoc
// @Summary 重置片段反馈数据
// @Description 管理员重置片段的赞踩数据和权重
// @Tags 管理
// @Accept json
// @Produce json
// @Param chunk_id path string true "片段ID"
// @Success 200 {object} map[string]interface{} "重置成功"
// @Failure 400 {object} errors.AppError "请求参数错误"
// @Failure 500 {object} errors.AppError "服务器错误"
// @Security Bearer
// @Router /admin/chunks/{chunk_id}/reset-feedback [post]
func (h *ChunkFeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	chunkID := c.Param("chunk_id")
	if chunkID == "" {
		c.Error(errors.NewBadRequestError("chunk_id is required"))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	operator := ""
	if uid, exists := c.Get(types.UserIDContextKey.String()); exists {
		operator = uid.(string)
	}

	if err := h.feedbackService.ResetChunkFeedback(c.Request.Context(), tenantID, chunkID, operator); err != nil {
		logger.Errorf(c.Request.Context(), "Failed to reset chunk feedback: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "反馈数据重置成功",
	})
}

// GetChunkWeightLogs godoc
// @Summary 获取权重变更日志
// @Description 查看片段的权重变更历史
// @Tags 管理
// @Produce json
// @Param chunk_id path string true "片段ID"
// @Param limit query int false "返回数量" default(50)
// @Success 200 {object} map[string]interface{} "日志列表"
// @Failure 400 {object} errors.AppError "请求参数错误"
// @Failure 500 {object} errors.AppError "服务器错误"
// @Security Bearer
// @Router /admin/chunks/{chunk_id}/weight-logs [get]
func (h *ChunkFeedbackHandler) GetChunkWeightLogs(c *gin.Context) {
	chunkID := c.Param("chunk_id")
	if chunkID == "" {
		c.Error(errors.NewBadRequestError("chunk_id is required"))
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	logs, err := h.feedbackService.GetWeightLogs(c.Request.Context(), chunkID, limit)
	if err != nil {
		logger.Errorf(c.Request.Context(), "Failed to get weight logs: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    logs,
	})
}

// GetDislikeReasons godoc
// @Summary 获取点踩原因选项
// @Description 获取预定义的点踩原因列表
// @Tags 反馈
// @Produce json
// @Success 200 {object} map[string]interface{} "原因列表"
// @Security Bearer
// @Router /feedback/dislike-reasons [get]
func (h *ChunkFeedbackHandler) GetDislikeReasons(c *gin.Context) {
	reasons := h.feedbackService.GetDislikeReasonOptions()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    reasons,
	})
}
