package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// FeedbackHandler exposes authenticated feedback endpoints.
type FeedbackHandler struct {
	service interfaces.FeedbackService
}

// NewFeedbackHandler creates a feedback HTTP handler.
func NewFeedbackHandler(service interfaces.FeedbackService) *FeedbackHandler {
	return &FeedbackHandler{service: service}
}

type putMessageFeedbackRequest struct {
	Type       types.FeedbackType        `json:"type" binding:"required"`
	ReasonCode *types.FeedbackReasonCode `json:"reason_code"`
}

// PutMessageFeedback creates, changes, or cancels feedback on an assistant message.
func (h *FeedbackHandler) PutMessageFeedback(c *gin.Context) {
	var request putMessageFeedbackRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	state, err := h.service.ApplyMessageFeedback(
		c.Request.Context(), c.Param("session_id"), c.Param("message_id"),
		request.Type, request.ReasonCode,
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// ResetChunkFeedback resets a chunk's feedback-derived projection.
func (h *FeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	if err := h.service.ResetChunkFeedback(c.Request.Context(), c.Param("id"), c.Param("chunk_id")); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetChunkFeedbackDetails returns feedback reasons and audit events for a chunk.
func (h *FeedbackHandler) GetChunkFeedbackDetails(c *gin.Context) {
	details, err := h.service.GetChunkFeedbackDetails(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": details})
}

func (h *FeedbackHandler) writeError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, service.ErrInvalidFeedback):
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
	case stderrors.Is(err, service.ErrFeedbackForbidden):
		_ = c.Error(apperrors.NewForbiddenError(err.Error()))
	case stderrors.Is(err, service.ErrFeedbackNotEligible):
		_ = c.Error(apperrors.NewConflictError(err.Error()))
	case stderrors.Is(err, service.ErrFeedbackNotFound),
		stderrors.Is(err, repository.ErrFeedbackChunkNotFound):
		_ = c.Error(apperrors.NewNotFoundError(err.Error()))
	default:
		_ = c.Error(apperrors.NewInternalServerError(err.Error()))
	}
}
