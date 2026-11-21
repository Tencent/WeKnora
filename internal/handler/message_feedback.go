package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MessageFeedbackHandler serves like/dislike votes on assistant replies.
type MessageFeedbackHandler struct {
	feedback interfaces.MessageFeedbackService
}

// NewMessageFeedbackHandler creates the feedback handler.
func NewMessageFeedbackHandler(feedback interfaces.MessageFeedbackService) *MessageFeedbackHandler {
	return &MessageFeedbackHandler{feedback: feedback}
}

// Submit stores a like, dislike, or cancellation for one assistant message.
func (h *MessageFeedbackHandler) Submit(c *gin.Context) {
	if h == nil || h.feedback == nil {
		_ = c.Error(apperrors.NewInternalServerError("feedback service unavailable"))
		return
	}
	sessionID := firstParam(c, "session_id", "id")
	messageID := strings.TrimSpace(c.Param("message_id"))
	var req types.MessageFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	view, err := h.feedback.Submit(c.Request.Context(), sessionID, messageID, &req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func chunkFeedbackFilter(c *gin.Context) types.ChunkFeedbackFilter {
	var filter types.ChunkFeedbackFilter
	if raw := strings.TrimSpace(c.Query("max_positive_rate")); raw != "" {
		if rate, err := strconv.ParseFloat(raw, 64); err == nil && rate > 0 && rate <= 1 {
			filter.MaxPositiveRate = &rate
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.Query("needs_optimization"))) {
	case "1", "true", "yes":
		value := true
		filter.NeedsOptimization = &value
	case "0", "false", "no":
		value := false
		filter.NeedsOptimization = &value
	}
	return filter
}

func firstParam(c *gin.Context, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(c.Param(name)); value != "" {
			return secutils.SanitizeForLog(value)
		}
	}
	return ""
}

// SetFeedbackService lets message loading echo the caller's current vote.
func (h *MessageHandler) SetFeedbackService(feedback interfaces.MessageFeedbackService) {
	if h == nil {
		return
	}
	h.feedback = feedback
}

// SetFeedbackService lets chunk admin routes read and reset quality stats.
func (h *ChunkHandler) SetFeedbackService(feedback interfaces.MessageFeedbackService) {
	if h == nil {
		return
	}
	h.feedback = feedback
}

func (h *MessageHandler) attachFeedback(c *gin.Context, messages []*types.Message) {
	if h == nil || h.feedback == nil {
		return
	}
	h.feedback.Attach(c.Request.Context(), messages)
}

// GetChunkFeedback returns likes, dislikes, approval rate, citing sessions,
// and aggregated dislike reasons for one chunk.
func (h *ChunkHandler) GetChunkFeedback(c *gin.Context) {
	chunk, _, err := h.fetchChunkAndVerifyOwnership(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if h.feedback == nil {
		_ = c.Error(apperrors.NewInternalServerError("feedback service unavailable"))
		return
	}
	stats, err := h.feedback.ChunkStats(c.Request.Context(), chunk.ID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// ListChunkWeightLogs returns the recall-weight change log for one chunk.
func (h *ChunkHandler) ListChunkWeightLogs(c *gin.Context) {
	chunk, _, err := h.fetchChunkAndVerifyOwnership(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if h.feedback == nil {
		_ = c.Error(apperrors.NewInternalServerError("feedback service unavailable"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	logs, err := h.feedback.ListWeightLogs(c.Request.Context(), chunk.ID, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": logs})
}

// ResetChunkFeedback clears a chunk's vote tallies and restores a neutral weight.
func (h *ChunkHandler) ResetChunkFeedback(c *gin.Context) {
	chunk, _, err := h.fetchChunkAndVerifyOwnership(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if h.feedback == nil {
		_ = c.Error(apperrors.NewInternalServerError("feedback service unavailable"))
		return
	}
	updated, err := h.feedback.ResetChunk(c.Request.Context(), chunk.ID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": updated})
}
