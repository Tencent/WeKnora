package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// AnswerFeedbackHandler serves like/dislike ratings and chunk feedback stats.
type AnswerFeedbackHandler struct {
	feedback interfaces.AnswerFeedbackService
}

// NewAnswerFeedbackHandler creates the feedback handler.
func NewAnswerFeedbackHandler(feedback interfaces.AnswerFeedbackService) *AnswerFeedbackHandler {
	return &AnswerFeedbackHandler{feedback: feedback}
}

type submitAnswerFeedbackRequest struct {
	Rating string `json:"rating"`
	Reason string `json:"reason"`
}

// SubmitAnswerFeedback stores the caller's like, dislike, or cancellation.
func (h *AnswerFeedbackHandler) SubmitAnswerFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	var req submitAnswerFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewValidationError("invalid feedback payload").WithDetails(err.Error()))
		return
	}
	state, err := h.feedback.Submit(
		ctx,
		strings.TrimSpace(c.Param("session_id")),
		strings.TrimSpace(c.Param("id")),
		req.Rating,
		req.Reason,
	)
	if err != nil {
		writeFeedbackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// GetAnswerFeedback returns the caller's rating for one assistant message.
func (h *AnswerFeedbackHandler) GetAnswerFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	state, err := h.feedback.Get(
		ctx,
		strings.TrimSpace(c.Param("session_id")),
		strings.TrimSpace(c.Param("id")),
	)
	if err != nil {
		writeFeedbackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// ListChunkFeedback lists chunk like/dislike stats for a knowledge base.
func (h *AnswerFeedbackHandler) ListChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	filter, err := chunkFeedbackFilter(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	page, err := h.feedback.ListChunkFeedback(ctx, strings.TrimSpace(c.Param("id")), filter)
	if err != nil {
		writeFeedbackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": page})
}

// GetChunkFeedback returns one chunk's stats, reasons, sessions, and weight logs.
func (h *AnswerFeedbackHandler) GetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	detail, err := h.feedback.GetChunkFeedback(
		ctx,
		strings.TrimSpace(c.Param("id")),
		strings.TrimSpace(c.Param("chunk_id")),
	)
	if err != nil {
		writeFeedbackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}

// ResetChunkFeedback clears one chunk's ratings and restores a neutral weight.
func (h *AnswerFeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	detail, err := h.feedback.ResetChunkFeedback(
		ctx,
		strings.TrimSpace(c.Param("id")),
		strings.TrimSpace(c.Param("chunk_id")),
	)
	if err != nil {
		writeFeedbackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}

func chunkFeedbackFilter(c *gin.Context) (types.ChunkFeedbackListFilter, error) {
	page, err := optionalPositiveInt(c.Query("page"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("page must be a positive integer")
	}
	pageSize, err := optionalPositiveInt(c.Query("page_size"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("page_size must be a positive integer")
	}
	minApproval, err := optionalRate(c.Query("min_approval_rate"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("min_approval_rate must be between 0 and 1")
	}
	maxApproval, err := optionalRate(c.Query("max_approval_rate"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("max_approval_rate must be between 0 and 1")
	}
	needs, err := optionalBool(c.Query("needs_optimization"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("needs_optimization must be true or false")
	}
	lowQuality, err := optionalBool(c.Query("low_quality"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("low_quality must be true or false")
	}
	includeUnrated, err := optionalBool(c.Query("include_unrated"))
	if err != nil {
		return types.ChunkFeedbackListFilter{}, errors.NewBadRequestError("include_unrated must be true or false")
	}
	filter := types.ChunkFeedbackListFilter{
		Page:              page,
		PageSize:          pageSize,
		MinApproval:       minApproval,
		MaxApproval:       maxApproval,
		NeedsOptimization: needs,
		LowQuality:        lowQuality != nil && *lowQuality,
		IncludeUnrated:    includeUnrated != nil && *includeUnrated,
	}
	return filter, nil
}

func optionalPositiveInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, err
	}
	return value, nil
}

func optionalRate(raw string) (*float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 1 {
		if err == nil {
			err = strconv.ErrRange
		}
		return nil, err
	}
	return &value, nil
}

func optionalBool(raw string) (*bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func writeFeedbackError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	if appErr, ok := errors.IsAppError(err); ok {
		_ = c.Error(appErr)
		return
	}
	if err == errors.ErrSessionNotFound {
		_ = c.Error(errors.NewNotFoundError("session not found"))
		return
	}
	logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
		"path": secutils.SanitizeForLog(c.FullPath()),
	})
	_ = c.Error(errors.NewInternalServerError("feedback request failed"))
}
