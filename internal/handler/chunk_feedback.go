package handler

import (
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// ChunkFeedbackHandler handles chunk feedback related HTTP requests
type ChunkFeedbackHandler struct {
	feedbackService interfaces.ChunkFeedbackServiceInterface
}

// NewChunkFeedbackHandler creates a new ChunkFeedbackHandler instance
func NewChunkFeedbackHandler(feedbackService interfaces.ChunkFeedbackServiceInterface) *ChunkFeedbackHandler {
	return &ChunkFeedbackHandler{feedbackService: feedbackService}
}

// SubmitFeedbackRequest represents the request body for submitting feedback
type SubmitFeedbackRequest struct {
	SessionID           string                    `json:"session_id" binding:"required"`
	MessageID          string                    `json:"message_id" binding:"required"`
	FeedbackType       types.FeedbackType       `json:"feedback_type" binding:"required"`
	DislikeReason      *types.DislikeReason     `json:"dislike_reason"`
	DislikeReasonDetail *string                  `json:"dislike_reason_detail"`
}

// SubmitFeedback godoc
// @Summary      提交问答回复反馈
// @Description  用户对AI问答回复进行点赞/点踩，反馈会归因到引用的知识库片段
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        request  body      SubmitFeedbackRequest  true  "反馈请求参数"
// @Success      200      {object}  map[string]interface{}  "提交成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      401      {object}  errors.AppError         "未授权"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/feedback/chunk [post]
func (h *ChunkFeedbackHandler) SubmitFeedback(c *gin.Context) {
	ctx := c.Request.Context()

	var req SubmitFeedbackRequest
	if err := c.ShouldBind(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	userID, _ := c.Get(string(types.UserIDContextKey))
	userIDStr := ""
	if userID != nil {
		userIDStr = userID.(string)
	}

	logger.Infof(ctx, "[ChunkFeedback] Submitting feedback: user=%s, message=%s, type=%s",
		userIDStr, req.MessageID, req.FeedbackType)

	if err := h.feedbackService.SubmitFeedback(ctx, userIDStr, req.SessionID, req.MessageID,
		req.FeedbackType, req.DislikeReason, req.DislikeReasonDetail, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Feedback submitted successfully",
	})
}

// GetChunkStatsRequest represents the request for getting chunk stats
type GetChunkStatsRequest struct {
	ChunkID string `form:"chunk_id" binding:"required" uri:"chunk_id"`
}

// GetChunkStats godoc
// @Summary      获取片段统计信息
// @Description  获取指定知识库片段的点赞、点踩、好评率等统计数据
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        chunk_id  path      string  true  "片段ID"
// @Success      200      {object}  map[string]interface{}  "片段统计信息"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      404      {object}  errors.AppError         "片段不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/chunks/{chunk_id}/stats [get]
func (h *ChunkFeedbackHandler) GetChunkStats(c *gin.Context) {
	ctx := c.Request.Context()

	var req GetChunkStatsRequest
	if err := c.ShouldBindUri(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	stats, err := h.feedbackService.GetChunkStats(ctx, req.ChunkID, tenantID)
	if err != nil {
		if err.Error() == "chunk not found" {
			c.Error(errors.NewNotFoundError("Chunk not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// ListChunksByStatsRequest represents the request for listing chunks by stats
type ListChunksByStatsRequest struct {
	KnowledgeBaseID   string   `form:"-" uri:"kb_id"`
	Keyword           string   `form:"keyword"`
	MinLikeRate       *float64 `form:"min_like_rate"`
	MaxLikeRate       *float64 `form:"max_like_rate"`
	PendingOptimization *bool   `form:"pending_optimization"`
	SortBy            string   `form:"sort_by"`
	SortOrder         string   `form:"sort_order"`
	Page              int      `form:"page"`
	PageSize          int      `form:"page_size"`
}

// ListChunksByStats godoc
// @Summary      按统计信息筛选片段
// @Description  支持按好评率、待优化状态等条件筛选知识库片段
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        kb_id  path      string  true  "知识库ID"
// @Param        keyword  query    string  false  "关键词搜索"
// @Param        min_like_rate  query  number  false  "最低好评率"
// @Param        max_like_rate  query  number  false  "最高好评率"
// @Param        pending_optimization  query  bool  false  "仅显示待优化"
// @Param        sort_by  query    string  false  "排序字段: like_count, dislike_count, like_rate, recall_weight"
// @Param        sort_order  query  string  false  "排序方向: asc, desc"
// @Param        page  query    int  false  "页码"
// @Param        page_size  query  int  false  "每页数量"
// @Success      200      {object}  map[string]interface{}  "片段列表"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/knowledge-bases/{kb_id}/chunks/stats [get]
func (h *ChunkFeedbackHandler) ListChunksByStats(c *gin.Context) {
	ctx := c.Request.Context()

	var req ListChunksByStatsRequest
	if err := c.ShouldBindUri(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	params := &interfaces.ListChunksByStatsParams{
		Keyword:             req.Keyword,
		MinLikeRate:         req.MinLikeRate,
		MaxLikeRate:         req.MaxLikeRate,
		PendingOptimization: req.PendingOptimization,
		SortBy:             req.SortBy,
		SortOrder:          req.SortOrder,
		Page:               req.Page,
		PageSize:           req.PageSize,
	}

	chunks, total, err := h.feedbackService.ListChunksByStats(ctx, req.KnowledgeBaseID, params, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": chunks,
			"total": total,
			"page":  req.Page,
			"page_size": req.PageSize,
		},
	})
}

// GetChunkWeightLogs godoc
// @Summary      获取片段权重变更日志
// @Description  获取指定片段的权重变更历史记录
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        chunk_id  path      string  true  "片段ID"
// @Param        limit  query    int  false  "每页数量，默认20"
// @Param        offset  query    int  false  "偏移量"
// @Success      200      {object}  map[string]interface{}  "权重变更日志"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/chunks/{chunk_id}/weight-logs [get]
func (h *ChunkFeedbackHandler) GetChunkWeightLogs(c *gin.Context) {
	ctx := c.Request.Context()

	chunkID := c.Param("chunk_id")
	if chunkID == "" {
		c.Error(errors.NewBadRequestError("chunk_id is required"))
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	tenantID := types.MustTenantIDFromContext(ctx)

	logs, total, err := h.feedbackService.GetChunkWeightLogs(ctx, chunkID, limit, offset, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": logs,
			"total": total,
		},
	})
}

// ResetChunkFeedbackRequest represents the request for resetting chunk feedback
type ResetChunkFeedbackRequest struct {
	ChunkID string `json:"chunk_id" binding:"required"`
}

// ResetChunkFeedback godoc
// @Summary      重置片段评价数据
// @Description  管理员手动重置片段的点赞、点踩数据和权重
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        request  body      ResetChunkFeedbackRequest  true  "重置请求参数"
// @Success      200      {object}  map[string]interface{}  "重置成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      403      {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/chunks/feedback/reset [post]
func (h *ChunkFeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()

	var req ResetChunkFeedbackRequest
	if err := c.ShouldBind(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	userID, _ := c.Get(string(types.UserIDContextKey))
	userIDStr := ""
	if userID != nil {
		userIDStr = userID.(string)
	}

	logger.Infof(ctx, "[ChunkFeedback] Reset feedback for chunk %s by user %s", req.ChunkID, userIDStr)

	if err := h.feedbackService.ResetChunkFeedback(ctx, req.ChunkID, userIDStr, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Chunk feedback reset successfully",
	})
}

// GetFeedbackSummary godoc
// @Summary      获取知识库反馈汇总
// @Description  获取指定知识库的整体反馈统计数据
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        kb_id  path      string  true  "知识库ID"
// @Success      200      {object}  map[string]interface{}  "反馈汇总统计"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/knowledge-bases/{kb_id}/feedback-summary [get]
func (h *ChunkFeedbackHandler) GetFeedbackSummary(c *gin.Context) {
	ctx := c.Request.Context()

	kbID := c.Param("kb_id")
	if kbID == "" {
		c.Error(errors.NewBadRequestError("kb_id is required"))
		return
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	summary, err := h.feedbackService.GetFeedbackSummary(ctx, kbID, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    summary,
	})
}

// BatchAdjustWeights godoc
// @Summary      批量调整权重
// @Description  触发批量权重调整，基于当前的好评率重新计算所有片段权重
// @Tags         知识库片段反馈
// @Accept       json
// @Produce      json
// @Param        kb_id  path      string  true  "知识库ID"
// @Success      200      {object}  map[string]interface{}  "调整成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/knowledge-bases/{kb_id}/chunks/batch-adjust-weights [post]
func (h *ChunkFeedbackHandler) BatchAdjustWeights(c *gin.Context) {
	ctx := c.Request.Context()

	kbID := c.Param("kb_id")
	if kbID == "" {
		c.Error(errors.NewBadRequestError("kb_id is required"))
		return
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	if err := h.feedbackService.BatchAdjustWeights(ctx, kbID, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Batch weight adjustment completed",
	})
}
