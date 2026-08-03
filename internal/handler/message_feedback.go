package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MessageFeedbackHandler exposes the answer like/dislike endpoints required
// by issue #1248. It owns three responsibilities:
//
//   - The "user" surface: like/dislike/cancel on a single assistant message
//     and the dislike-reason metadata that travels with it.
//   - The "admin" surface: per-chunk stats, weight change logs and reset for
//     a single knowledge base, all gated on KB ownership.
//   - The "tenant" surface: forcing a tenant-wide recall-weight recompute
//     after a feedback policy change (called from the retrieval-config save
//     path, not currently wired to a public endpoint — but stays here so the
//     container can expose it for admin tooling if needed).
//
// The user and admin surfaces are deliberately held in the same struct because
// they share the underlying MessageFeedbackService; splitting them across two
// files would force both to duplicate the constructor signature and would
// leave the "reset" + "recompute" pair orphaned.
type MessageFeedbackHandler struct {
	service interfaces.MessageFeedbackService
}

// NewMessageFeedbackHandler creates the handler. The service is the only dep:
// the routes already enforce ownership / role at the middleware layer, so the
// handler does not need to re-check RBAC it doesn't own.
func NewMessageFeedbackHandler(service interfaces.MessageFeedbackService) *MessageFeedbackHandler {
	return &MessageFeedbackHandler{service: service}
}

// SetMessageFeedbackRequest is the JSON body for UpsertFeedback.
type SetMessageFeedbackRequest struct {
	Rating  string   `json:"rating"   binding:"required"`
	Reasons []string `json:"reasons"`
	Comment string   `json:"comment"`
}

// Set godoc
// @Summary      提交/取消问答反馈
// @Description  对助手消息提交点赞、点踩或取消评价。"none" 等同于取消当前评价。
// @Description  rating=dislike 时可附带 reasons（白名单内原因）与 comment。
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        session_id  path  string  true  "会话 ID"
// @Param        message_id  path  string  true  "助手消息 ID"
// @Param        request     body  SetMessageFeedbackRequest  true  "反馈"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  apperrors.AppError
// @Failure      404  {object}  apperrors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/messages/{message_id}/feedback [put]
func (h *MessageFeedbackHandler) Set(c *gin.Context) {
	var req SetMessageFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	sessionID := secutils.SanitizeForLog(c.Param("id"))
	messageID := secutils.SanitizeForLog(c.Param("message_id"))
	feedback, err := h.service.UpsertFeedback(
		c.Request.Context(),
		sessionID,
		messageID,
		strings.ToLower(strings.TrimSpace(req.Rating)),
		req.Reasons,
		strings.TrimSpace(req.Comment),
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": feedback})
}

// ListChunkStats godoc
// @Summary      获取知识库片段维度的反馈统计
// @Description  KB owner / admin only。返回每条 chunk 的累计点赞、点踩、好评率与最近评价时间，支持按好评率升序/降序、低质筛选与按关键词搜索。
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id            path      string  true   "知识库 ID"
// @Param        page          query     int     false  "页码，从 1 开始"
// @Param        page_size     query     int     false  "每页大小"
// @Param        sort_by       query     string  false  "排序：positive_rate_asc/positive_rate_desc/feedback_count_desc/last_feedback_desc"
// @Param        low_quality   query     bool    false  "只看低质片段（好评率低于 needs-optimization 阈值）"
// @Param        keyword       query     string  false  "按内容预览模糊匹配"
// @Param        knowledge_id  query     string  false  "按文档 ID 过滤"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/feedback/chunk-stats [get]
func (h *MessageFeedbackHandler) ListChunkStats(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	query := parseChunkFeedbackStatsQuery(c)
	page, err := h.service.ListChunkStats(c.Request.Context(), kbID, query)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": page})
}

// ListWeightLogs godoc
// @Summary      获取知识库召回权重变更日志
// @Description  KB owner / admin only。可按 chunk_id 过滤，分页加载。
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id          path   string  true   "知识库 ID"
// @Param        chunk_id    query  string  false  "仅看该片段的权重变更日志"
// @Param        page        query  int     false  "页码"
// @Param        page_size   query  int     false  "每页大小"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/feedback/weight-logs [get]
func (h *MessageFeedbackHandler) ListWeightLogs(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if chunkID == "" {
		chunkID = strings.TrimSpace(c.Query("chunk_id"))
	}
	page := &types.Pagination{
		Page:     parseIntQuery(c, "page", 1),
		PageSize: parseIntQuery(c, "page_size", 20),
	}
	result, err := h.service.ListWeightLogs(c.Request.Context(), kbID, chunkID, page)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ResetFeedback godoc
// @Summary      重置知识库用户反馈
// @Description  推进反馈 epoch，使所有历史用户反馈不再计入统计；同步清空所有 chunk 的累计赞踩计数。KB owner / admin only。
// @Tags         知识库
// @Produce      json
// @Param        id   path  string  true  "知识库 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /knowledge-bases/{id}/feedback/reset [post]
func (h *MessageFeedbackHandler) ResetFeedback(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	resetCount, err := h.service.ResetKnowledgeBaseFeedback(c.Request.Context(), kbID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"reset_chunks": resetCount},
		"message": "feedback epoch advanced",
	})
}

// RecomputeTenantWeights godoc
// @Summary      触发租户级反馈权重重算
// @Description  根据当前 retrieval_config 中的 feedback policy 重新计算所有受影响 chunk 的 recall_weight。 Admin only。
// @Tags         系统
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/feedback/recompute [post]
func (h *MessageFeedbackHandler) RecomputeTenantWeights(c *gin.Context) {
	tenantID := types.MustTenantIDFromContext(c.Request.Context())
	updated, err := h.service.RecomputeTenantFeedbackWeights(c.Request.Context(), tenantID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"updated_chunks": updated},
	})
}

// parseChunkFeedbackStatsQuery reads the pagination / filter / sort parameters
// from the gin context. Empty values fall back to the service-side defaults,
// which keeps the route stable even when the UI evolves.
func parseChunkFeedbackStatsQuery(c *gin.Context) *interfaces.ChunkFeedbackStatsQuery {
	q := &interfaces.ChunkFeedbackStatsQuery{
		SortBy:         strings.TrimSpace(c.Query("sort_by")),
		LowQualityOnly: parseBoolQuery(c, "low_quality"),
		Keyword:        strings.TrimSpace(c.Query("keyword")),
		KnowledgeID:    strings.TrimSpace(c.Query("knowledge_id")),
		Pagination: &types.Pagination{
			Page:     parseIntQuery(c, "page", 1),
			PageSize: parseIntQuery(c, "page_size", 20),
		},
	}
	return q
}

func parseIntQuery(c *gin.Context, name string, def int) int {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func parseBoolQuery(c *gin.Context, name string) bool {
	switch strings.ToLower(strings.TrimSpace(c.Query(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// writeError translates service-layer errors into HTTP responses. AppError
// values are surfaced unchanged (their HTTPCode drives the status); everything
// else is logged in detail and returned as 500 to avoid leaking internals.
func (h *MessageFeedbackHandler) writeError(c *gin.Context, err error) {
	if appErr, ok := apperrors.IsAppError(err); ok {
		c.Error(appErr)
		return
	}
	logger.Errorf(c.Request.Context(), "message feedback operation failed: %v", err)
	c.Error(apperrors.NewInternalServerError("message feedback operation failed").WithDetails(err.Error()))
}
