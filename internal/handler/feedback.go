package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// FeedbackHandler handles HTTP requests for the like/dislike feedback feature.
type FeedbackHandler struct {
	FeedbackService interfaces.FeedbackService
}

// NewFeedbackHandler creates a new FeedbackHandler.
func NewFeedbackHandler(feedbackService interfaces.FeedbackService) *FeedbackHandler {
	return &FeedbackHandler{FeedbackService: feedbackService}
}

// SubmitFeedback godoc
// @Summary      提交点赞/点踩
// @Description  用户对问答回复提交点赞、点踩或取消评价。系统自动将评价归因到回复引用的所有知识库片段。
// @Tags         反馈
// @Accept       json
// @Produce      json
// @Param        body  body      types.FeedbackRequest  true  "反馈请求"
// @Success      200   {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback [post]
func (h *FeedbackHandler) SubmitFeedback(c *gin.Context) {
	ctx := c.Request.Context()

	var req types.FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.MessageID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message_id is required"})
		return
	}
	if req.FeedbackType != types.FeedbackLike && req.FeedbackType != types.FeedbackDislike && req.FeedbackType != types.FeedbackNone {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid feedback_type, must be 'like', 'dislike', or 'none'"})
		return
	}

	logger.Infof(ctx, "Submitting feedback: message_id=%s, type=%s", secutils.SanitizeForLog(req.MessageID), req.FeedbackType)

	fb, err := h.FeedbackService.SubmitFeedback(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to submit feedback: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// When feedback is cancelled (FeedbackNone), service returns nil feedback
	// with no error. Return a clear confirmation to the client.
	if fb == nil {
		c.JSON(http.StatusOK, gin.H{"feedback": nil, "cancelled": true})
		return
	}

	c.JSON(http.StatusOK, gin.H{"feedback": fb})
}

// GetFeedback godoc
// @Summary      获取用户对消息的反馈
// @Description  获取当前用户对指定消息的点赞/点踩状态
// @Tags         反馈
// @Produce      json
// @Param        message_id  path  string  true  "消息ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/{message_id} [get]
func (h *FeedbackHandler) GetFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	messageID := c.Param("message_id")

	fb, err := h.FeedbackService.GetFeedback(ctx, messageID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"feedback": fb})
}

// ListChunkFeedbackStats godoc
// @Summary      知识库片段反馈统计
// @Description  分页查看知识库片段的累计点赞、点踩、好评率、关联会话数等数据，支持按好评率筛选低质片段
// @Tags         反馈
// @Produce      json
// @Param        knowledge_base_id  query     string   false  "知识库ID（可选筛选）"
// @Param        page               query     int      false  "页码"  default(1)
// @Param        page_size          query     int      false  "每页数量"  default(20)
// @Param        min_approval       query     number   false  "最低好评率筛选（0-1）"
// @Param        max_approval       query     number   false  "最高好评率筛选（0-1）"
// @Param        needs_optimization query     bool     false  "仅返回待优化片段"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/chunks/stats [get]
func (h *FeedbackHandler) ListChunkFeedbackStats(c *gin.Context) {
	ctx := c.Request.Context()

	kbID := c.Query("knowledge_base_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	minApproval, _ := strconv.ParseFloat(c.DefaultQuery("min_approval", "0"), 64)
	maxApproval, _ := strconv.ParseFloat(c.DefaultQuery("max_approval", "0"), 64)
	needsOptOnly := c.Query("needs_optimization") == "true"

	stats, total, err := h.FeedbackService.ListChunkFeedbackStats(ctx, kbID, page, pageSize, minApproval, maxApproval, needsOptOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":     stats,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetChunkFeedbackStats godoc
// @Summary      单个片段反馈统计
// @Description  查看单个知识库片段的反馈统计详情（含点踩原因聚合）
// @Tags         反馈
// @Produce      json
// @Param        chunk_id  path  string  true  "片段ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/chunks/{chunk_id}/stats [get]
func (h *FeedbackHandler) GetChunkFeedbackStats(c *gin.Context) {
	ctx := c.Request.Context()
	chunkID := c.Param("chunk_id")

	stats, err := h.FeedbackService.GetChunkFeedbackStats(ctx, chunkID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

// ListWeightLogs godoc
// @Summary      权重变更日志
// @Description  分页查看片段召回权重的变更日志，可按片段筛选或查看全部
// @Tags         反馈
// @Produce      json
// @Param        chunk_id    query  string  false  "片段ID（不传则查看全部）"
// @Param        page        query  int     false  "页码"  default(1)
// @Param        page_size   query  int     false  "每页数量"  default(20)
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/weight-logs [get]
func (h *FeedbackHandler) ListWeightLogs(c *gin.Context) {
	ctx := c.Request.Context()

	chunkID := c.Query("chunk_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var logs []*types.ChunkWeightLog
	var total int64
	var err error

	if chunkID != "" {
		logs, total, err = h.FeedbackService.ListWeightLogs(ctx, chunkID, page, pageSize)
	} else {
		logs, total, err = h.FeedbackService.ListAllWeightLogs(ctx, page, pageSize)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":     logs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// AdminResetChunkFeedback godoc
// @Summary      管理员重置片段反馈数据
// @Description  重置指定片段的点赞数、点踩数、好评率、召回权重为默认值
// @Tags         反馈
// @Accept       json
// @Produce      json
// @Param        chunk_id  path  string  true  "片段ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/chunks/{chunk_id}/reset [post]
func (h *FeedbackHandler) AdminResetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	chunkID := c.Param("chunk_id")

	userID, _ := types.UserIDFromContext(ctx)

	if err := h.FeedbackService.AdminResetChunkFeedback(ctx, chunkID, userID); err != nil {
		logger.Errorf(ctx, "Admin reset chunk feedback failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "chunk feedback reset successfully"})
}

// AdminSetChunkWeight godoc
// @Summary      管理员手动设置片段权重
// @Description  手动设置指定片段的召回权重
// @Tags         反馈
// @Accept       json
// @Produce      json
// @Param        chunk_id  path  string  true  "片段ID"
// @Param        body      body  object  true  "权重设置请求 {weight: number}"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/chunks/{chunk_id}/weight [put]
func (h *FeedbackHandler) AdminSetChunkWeight(c *gin.Context) {
	ctx := c.Request.Context()
	chunkID := c.Param("chunk_id")

	var body struct {
		Weight float64 `json:"weight"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.Weight < 0 || body.Weight > 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "weight must be between 0 and 10"})
		return
	}

	userID, _ := types.UserIDFromContext(ctx)

	if err := h.FeedbackService.AdminSetChunkWeight(ctx, chunkID, body.Weight, userID); err != nil {
		logger.Errorf(ctx, "Admin set chunk weight failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "chunk weight updated successfully"})
}

// GetFeedbackThresholds godoc
// @Summary      获取权重调整阈值配置
// @Description  返回当前好评率→召回权重的阈值配置
// @Tags         反馈
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /feedback/thresholds [get]
func (h *FeedbackHandler) GetFeedbackThresholds(c *gin.Context) {
	ctx := c.Request.Context()
	thresholds := h.FeedbackService.GetThresholds(ctx)
	c.JSON(http.StatusOK, gin.H{"thresholds": thresholds})
}
