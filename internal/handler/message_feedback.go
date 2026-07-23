package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type MessageFeedbackHandler struct {
	service interfaces.MessageFeedbackService
}

func NewMessageFeedbackHandler(service interfaces.MessageFeedbackService) *MessageFeedbackHandler {
	return &MessageFeedbackHandler{service: service}
}

type submitMessageFeedbackRequest struct {
	Rating       int    `json:"rating" binding:"required,oneof=1 -1"`
	ReasonCode   string `json:"reason_code"`
	ReasonDetail string `json:"reason_detail"`
}

func (h *MessageFeedbackHandler) Submit(c *gin.Context) {
	var request submitMessageFeedbackRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	feedback, err := h.service.Submit(
		c.Request.Context(),
		c.Param("session_id"),
		c.Param("id"),
		request.Rating,
		request.ReasonCode,
		request.ReasonDetail,
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": feedback})
}

func (h *MessageFeedbackHandler) Cancel(c *gin.Context) {
	if err := h.service.Cancel(c.Request.Context(), c.Param("session_id"), c.Param("id")); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *MessageFeedbackHandler) ListChunkStats(c *gin.Context) {
	filter := types.ChunkFeedbackStatsFilter{
		Page:      parsePositiveInt(c.DefaultQuery("page", "1"), 1),
		PageSize:  parsePositiveInt(c.DefaultQuery("page_size", "20"), 20),
		Status:    strings.TrimSpace(c.Query("status")),
		Keyword:   strings.TrimSpace(c.Query("keyword")),
		SortBy:    strings.TrimSpace(c.Query("sort_by")),
		SortOrder: strings.TrimSpace(c.Query("sort_order")),
	}
	var err error
	if raw := strings.TrimSpace(c.Query("min_positive_rate")); raw != "" {
		filter.MinPositiveRate, err = parseRate(raw)
		if err != nil {
			c.Error(apperrors.NewBadRequestError("min_positive_rate must be between 0 and 1"))
			return
		}
	}
	if raw := strings.TrimSpace(c.Query("max_positive_rate")); raw != "" {
		filter.MaxPositiveRate, err = parseRate(raw)
		if err != nil {
			c.Error(apperrors.NewBadRequestError("max_positive_rate must be between 0 and 1"))
			return
		}
	}
	if filter.MinPositiveRate != nil && filter.MaxPositiveRate != nil &&
		*filter.MinPositiveRate > *filter.MaxPositiveRate {
		c.Error(apperrors.NewBadRequestError("min_positive_rate cannot exceed max_positive_rate"))
		return
	}
	if filter.Status != "" &&
		filter.Status != types.ChunkFeedbackStatusNormal &&
		filter.Status != types.ChunkFeedbackStatusPendingOptimization {
		c.Error(apperrors.NewBadRequestError("invalid feedback status"))
		return
	}

	result, err := h.service.ListChunkStats(c.Request.Context(), c.Param("id"), filter)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

func (h *MessageFeedbackHandler) ListWeightLogs(c *gin.Context) {
	logs, err := h.service.ListWeightLogs(
		c.Request.Context(),
		c.Param("id"),
		c.Param("chunk_id"),
		parsePositiveInt(c.DefaultQuery("limit", "50"), 50),
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": logs})
}

func (h *MessageFeedbackHandler) ResetChunk(c *gin.Context) {
	if err := h.service.ResetChunk(
		c.Request.Context(),
		c.Param("id"),
		c.Param("chunk_id"),
	); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *MessageFeedbackHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidMessageFeedback),
		errors.Is(err, service.ErrFeedbackNotAvailable):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.Error(apperrors.NewNotFoundError("resource not found"))
	default:
		c.Error(apperrors.NewInternalServerError(err.Error()))
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseRate(raw string) (*float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 1 {
		return nil, errors.New("invalid rate")
	}
	return &value, nil
}
