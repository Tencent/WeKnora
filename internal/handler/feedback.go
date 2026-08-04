package handler

import (
	stderrors "errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// FeedbackHandler handles Q&A answer feedback and the admin chunk feedback
// statistics surfaces.
type FeedbackHandler struct {
	FeedbackService interfaces.ChunkFeedbackService
}

// NewFeedbackHandler creates a new feedback handler.
func NewFeedbackHandler(feedbackService interfaces.ChunkFeedbackService) *FeedbackHandler {
	return &FeedbackHandler{FeedbackService: feedbackService}
}

// submitFeedbackRequest is the payload for rating an assistant answer.
type submitFeedbackRequest struct {
	// Rating is "like" or "dislike".
	Rating string `json:"rating" binding:"required"`
	// Reason is the optional dislike reason.
	Reason string `json:"reason"`
}

// SubmitFeedback godoc
// @Summary      提交问答回复评价
// @Description  对某条助手回复提交点赞/点踩，评价会归因到该回复引用的所有知识库片段
// @Tags         消息
// @Accept       json
// @Produce      json
// @Param        session_id  path  string  true  "会话ID"
// @Param        message_id  path  string  true  "消息ID"
// @Param        body        body  submitFeedbackRequest  true  "评价内容"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{message_id}/feedback [post]
func (h *FeedbackHandler) SubmitFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("session_id")
	messageID := c.Param("id")
	userID, _ := types.UserIDFromContext(ctx)

	var req submitFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("invalid feedback payload: " + err.Error()))
		return
	}

	rating := types.ChunkFeedbackRating(req.Rating)
	if !rating.Valid() {
		c.Error(errors.NewBadRequestError("rating must be \"like\" or \"dislike\""))
		return
	}

	if err := h.FeedbackService.SubmitFeedback(ctx, userID, sessionID, messageID, rating, req.Reason); err != nil {
		mapFeedbackError(c, err, "submit feedback")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CancelFeedback godoc
// @Summary      取消问答回复评价
// @Description  取消当前用户对某条助手回复的点赞/点踩，并同步更新关联片段的计数
// @Tags         消息
// @Produce      json
// @Param        session_id  path  string  true  "会话ID"
// @Param        message_id  path  string  true  "消息ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{message_id}/feedback [delete]
func (h *FeedbackHandler) CancelFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("session_id")
	messageID := c.Param("id")
	userID, _ := types.UserIDFromContext(ctx)

	if err := h.FeedbackService.CancelFeedback(ctx, userID, sessionID, messageID); err != nil {
		mapFeedbackError(c, err, "cancel feedback")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetChunkFeedbackStats godoc
// @Summary      片段反馈统计
// @Description  按知识库片段维度查看累计点赞、点踩、好评率、关联会话数等数据，支持按好评率筛选
// @Tags         知识库
// @Produce      json
// @Param        knowledge_base_id  query  string   false  "知识库ID"
// @Param        knowledge_id      query  string   false  "文档ID"
// @Param        min_approval_rate query  number   false  "最小好评率(0-1)"
// @Param        max_approval_rate query  number   false  "最大好评率(0-1)"
// @Param        needs_optimization query boolean  false  "仅看待优化片段"
// @Param        keyword           query  string   false  "内容关键字"
// @Param        sort_by           query  string   false  "排序字段"
// @Param        sort_order        query  string   false  "asc/desc"
// @Param        page              query  int      false  "页码"
// @Param        page_size         query  int      false  "每页数量"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/chunk-feedback/stats [get]
func (h *FeedbackHandler) GetChunkFeedbackStats(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)

	params := &interfaces.ChunkFeedbackStatsParams{
		TenantID:        tenantID,
		KnowledgeBaseID: c.Query("knowledge_base_id"),
		KnowledgeID:     c.Query("knowledge_id"),
		Keyword:         c.Query("keyword"),
		SortBy:          c.Query("sort_by"),
		SortOrder:       c.Query("sort_order"),
		Page:            parsePositiveInt(c.Query("page"), 1),
		PageSize:        parsePositiveInt(c.Query("page_size"), 20),
	}
	if v := c.Query("min_approval_rate"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			c.Error(errors.NewBadRequestError("invalid min_approval_rate"))
			return
		}
		params.MinApprovalRate = &f
	}
	if v := c.Query("max_approval_rate"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			c.Error(errors.NewBadRequestError("invalid max_approval_rate"))
			return
		}
		params.MaxApprovalRate = &f
	}
	if v := c.Query("needs_optimization"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			c.Error(errors.NewBadRequestError("invalid needs_optimization"))
			return
		}
		params.NeedsOptimization = &b
	}

	stats, total, err := h.FeedbackService.GetChunkFeedbackStats(ctx, params)
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to load chunk feedback stats: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
		"total":   total,
	})
}

// GetChunkFeedbackDetail godoc
// @Summary      片段反馈详情
// @Description  查看单个片段的点踩原因聚合、关联问答会话等信息
// @Tags         知识库
// @Produce      json
// @Param        chunk_id  path  string  true  "片段ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/chunk-feedback/stats/{chunk_id} [get]
func (h *FeedbackHandler) GetChunkFeedbackDetail(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	chunkID := c.Param("chunk_id")

	detail, err := h.FeedbackService.GetChunkFeedbackDetail(ctx, tenantID, chunkID)
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to load chunk feedback detail: " + err.Error()))
		return
	}
	if detail == nil {
		c.Error(errors.NewNotFoundError("chunk not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}

// ListWeightLogs godoc
// @Summary      片段权重变更日志
// @Description  查看片段召回权重的变更历史及触发来源
// @Tags         知识库
// @Produce      json
// @Param        chunk_id  query  string  false  "片段ID"
// @Param        source    query  string  false  "触发来源(feedback/manual_reset/manual_adjust)"
// @Param        page      query  int     false  "页码"
// @Param        page_size query  int     false  "每页数量"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/chunk-feedback/weight-logs [get]
func (h *FeedbackHandler) ListWeightLogs(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)

	logs, total, err := h.FeedbackService.ListWeightLogs(
		ctx, tenantID,
		c.Query("chunk_id"), c.Query("source"),
		parsePositiveInt(c.Query("page"), 1),
		parsePositiveInt(c.Query("page_size"), 20),
	)
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to load weight logs: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": logs, "total": total})
}

// resetChunkFeedbackRequest is the payload for the admin reset action.
type resetChunkFeedbackRequest struct {
	// ChunkIDs lists the chunks whose feedback/weight should be reset.
	ChunkIDs []string `json:"chunk_ids" binding:"required"`
}

// ResetChunkFeedback godoc
// @Summary      重置片段反馈与权重
// @Description  管理员手动清零片段的点赞/点踩数据并将召回权重恢复为默认值
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        body  body  resetChunkFeedbackRequest  true  "片段ID列表"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/chunk-feedback/reset [post]
func (h *FeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)

	var req resetChunkFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("chunk_ids is required"))
		return
	}
	if len(req.ChunkIDs) == 0 {
		c.Error(errors.NewBadRequestError("chunk_ids must not be empty"))
		return
	}
	if err := h.FeedbackService.ResetChunkFeedback(ctx, tenantID, req.ChunkIDs, userID); err != nil {
		c.Error(errors.NewInternalServerError("failed to reset chunk feedback: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetConfig godoc
// @Summary      获取片段反馈配置
// @Description  查看好评率阈值、权重调整步长等配置
// @Tags         知识库
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/chunk-feedback/config [get]
func (h *FeedbackHandler) GetConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	cfg, err := h.FeedbackService.GetConfig(ctx, tenantID)
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to load feedback config: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}

// UpdateConfig godoc
// @Summary      更新片段反馈配置
// @Description  配置权重调整阈值与步长
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        body  body  types.ChunkFeedbackConfig  true  "配置项"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/chunk-feedback/config [put]
func (h *FeedbackHandler) UpdateConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)

	var cfg types.ChunkFeedbackConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.Error(errors.NewBadRequestError("invalid config payload"))
		return
	}
	cfg.TenantID = tenantID
	if err := h.FeedbackService.UpdateConfig(ctx, &cfg); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// mapFeedbackError translates service errors into HTTP responses.
func mapFeedbackError(c *gin.Context, err error, action string) {
	switch {
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		c.Error(errors.NewNotFoundError("message not found"))
	default:
		c.Error(errors.NewBadRequestError(action + " failed: " + err.Error()))
	}
}

// parsePositiveInt parses a positive int with a fallback.
func parsePositiveInt(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
