package handler

import (
	stderrors "errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MessageFeedbackHandler handles HTTP requests for answer like/dislike
// feedback and the per-chunk feedback statistics surfaces.
type MessageFeedbackHandler struct {
	FeedbackService interfaces.MessageFeedbackService
}

// NewMessageFeedbackHandler creates a new message feedback handler instance
func NewMessageFeedbackHandler(feedbackService interfaces.MessageFeedbackService) *MessageFeedbackHandler {
	return &MessageFeedbackHandler{FeedbackService: feedbackService}
}

// UpsertMessageFeedbackRequest is the body of the rating endpoint.
type UpsertMessageFeedbackRequest struct {
	// Rating: "like", "dislike" or "none" (cancels an existing rating)
	Rating string `json:"rating" binding:"required"`
	// Preset dislike reason codes (only with rating "dislike")
	Reasons []string `json:"reasons"`
	// Free-form dislike comment (only with rating "dislike")
	Comment string `json:"comment"`
}

// UpsertMessageFeedback godoc
// @Summary      提交/取消消息点赞点踩
// @Description  对指定会话中的 AI 回复提交点赞/点踩（含点踩原因），rating 为 none 时取消评价；反馈自动归因到该回复引用的知识库片段
// @Tags         消息
// @Accept       json
// @Produce      json
// @Param        session_id  path      string                        true  "会话ID"
// @Param        id          path      string                        true  "消息ID"
// @Param        request     body      UpsertMessageFeedbackRequest  true  "反馈内容"
// @Success      200         {object}  map[string]interface{}        "反馈结果"
// @Failure      400         {object}  errors.AppError               "请求参数错误"
// @Failure      404         {object}  errors.AppError               "会话或消息不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{id}/feedback [put]
func (h *MessageFeedbackHandler) UpsertMessageFeedback(c *gin.Context) {
	ctx := c.Request.Context()

	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("id"))

	var request UpsertMessageFeedbackRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse feedback request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Upserting message feedback, session ID: %s, message ID: %s, rating: %s",
		sessionID, messageID, secutils.SanitizeForLog(request.Rating))

	feedback, err := h.FeedbackService.UpsertFeedback(
		ctx, sessionID, messageID, request.Rating, request.Reasons, request.Comment,
	)
	if err != nil {
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			c.Error(appErr)
			return
		}
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			// Non-owner / wrong-tenant lookups surface as ErrSessionNotFound
			// (see LoadMessages); map to 404 like the other message routes.
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"message_id": messageID,
			"rating":     feedback.Rating,
			"reasons":    feedback.Reasons,
			"comment":    feedback.Comment,
		},
	})
}

// ListChunkFeedbackStats godoc
// @Summary      片段反馈统计
// @Description  按知识库列出片段的累计点赞/点踩/好评率/召回权重/待优化标记/点踩原因聚合/关联问答会话数，支持排序与筛选
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id                  path      string  true   "知识库ID"
// @Param        page                query     int     false  "页码"      default(1)
// @Param        page_size           query     int     false  "每页数量"  default(20)
// @Param        sort_by             query     string  false  "排序字段：like_count|dislike_count|positive_rate|recall_weight|total"  default(dislike_count)
// @Param        order               query     string  false  "排序方向：asc|desc"  default(desc)
// @Param        min_total           query     int     false  "最少评价数"  default(1)
// @Param        min_rate            query     number  false  "好评率下限（0-1）"
// @Param        max_rate            query     number  false  "好评率上限（0-1）"
// @Param        needs_optimization  query     bool    false  "仅看待优化片段"
// @Success      200                 {object}  map[string]interface{}  "统计列表"
// @Failure      404                 {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Router       /knowledge-bases/{id}/feedback/stats [get]
func (h *MessageFeedbackHandler) ListChunkFeedbackStats(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	query := &interfaces.ChunkFeedbackStatsQuery{
		Pagination:            &pagination,
		SortBy:                c.DefaultQuery("sort_by", "dislike_count"),
		Order:                 c.DefaultQuery("order", "desc"),
		NeedsOptimizationOnly: c.Query("needs_optimization") == "true",
	}
	if raw := c.Query("min_total"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			query.MinTotal = v
		}
	}
	if v, ok := parseRateQuery(c, "min_rate"); ok {
		query.MinRate = &v
	}
	if v, ok := parseRateQuery(c, "max_rate"); ok {
		query.MaxRate = &v
	}

	result, err := h.FeedbackService.ListChunkStats(ctx, kbID, query)
	if err != nil {
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// ListChunkWeightLogs godoc
// @Summary      片段权重变更日志
// @Description  按知识库（可选按片段）分页查看召回权重变更日志，包含触发来源（feedback/reset/config）
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "知识库ID"
// @Param        chunk_id   query     string  false  "片段ID"
// @Param        page       query     int     false  "页码"      default(1)
// @Param        page_size  query     int     false  "每页数量"  default(20)
// @Success      200        {object}  map[string]interface{}  "日志列表"
// @Failure      404        {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Router       /knowledge-bases/{id}/feedback/weight-logs [get]
func (h *MessageFeedbackHandler) ListChunkWeightLogs(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	chunkID := secutils.SanitizeForLog(c.Query("chunk_id"))

	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	result, err := h.FeedbackService.ListWeightLogs(ctx, kbID, chunkID, &pagination)
	if err != nil {
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// ResetKnowledgeBaseFeedback godoc
// @Summary      重置知识库反馈数据
// @Description  推进反馈纪元并清零该知识库全部片段的点赞/点踩计数、好评率与召回权重（写入 reset 权重日志）；历史评价与引用关系保留，仅不再计入统计
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {object}  map[string]interface{}  "重置结果"
// @Failure      404  {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Router       /knowledge-bases/{id}/feedback [delete]
func (h *MessageFeedbackHandler) ResetKnowledgeBaseFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	logger.Infof(ctx, "Resetting knowledge base feedback, KB ID: %s", kbID)

	resetChunks, err := h.FeedbackService.ResetKnowledgeBaseFeedback(ctx, kbID)
	if err != nil {
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"reset_chunks": resetChunks,
		},
	})
}

// parseRateQuery parses an optional 0..1 float query parameter.
func parseRateQuery(c *gin.Context, name string) (float64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, false
	}
	return v, true
}
