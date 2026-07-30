package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type FeedbackHandler struct {
	messageService  interfaces.MessageService
	feedbackService FeedbackServiceInterface
}

type FeedbackServiceInterface interface {
	SubmitFeedback(ctx context.Context, tenantID uint64, sessionID string, messageID string, feedbackType types.FeedbackType, reason string) (*types.Message, error)
	ListChunkFeedbackStats(ctx context.Context, tenantID uint64, kbID string, page, pageSize int) ([]*types.ChunkFeedbackStats, int64, error)
	ListWeightLogs(ctx context.Context, tenantID uint64, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)
	ListWeightLogsByChunk(ctx context.Context, tenantID uint64, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)
	ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkID string, operatorID string) error
}

func NewFeedbackHandler(
	messageService interfaces.MessageService,
	feedbackService FeedbackServiceInterface,
) *FeedbackHandler {
	return &FeedbackHandler{
		messageService:  messageService,
		feedbackService: feedbackService,
	}
}

type submitFeedbackRequest struct {
	Type   types.FeedbackType `json:"type" binding:"required"`
	Reason string             `json:"reason,omitempty"`
}

func (h *FeedbackHandler) SubmitFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("message_id"))

	var req submitFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse feedback request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if req.Type != types.FeedbackTypeLike && req.Type != types.FeedbackTypeDislike && req.Type != types.FeedbackTypeNone {
		c.Error(errors.NewBadRequestError("invalid feedback type"))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	msg, err := h.feedbackService.SubmitFeedback(ctx, tenantID, sessionID, messageID, req.Type, req.Reason)
	if err != nil {
		logger.Errorf(ctx, "Failed to submit feedback: %v", err)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    msg.Feedback,
	})
}

func (h *FeedbackHandler) GetMessageFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("message_id"))

	msg, err := h.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get message: %v", err)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"feedback": msg.Feedback,
	})
}

func (h *FeedbackHandler) ListChunkFeedbackStats(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	kbID := secutils.SanitizeForLog(c.Query("kb_id"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	stats, total, err := h.feedbackService.ListChunkFeedbackStats(ctx, tenantID, kbID, page, pageSize)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      stats,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *FeedbackHandler) ListWeightLogs(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	chunkID := secutils.SanitizeForLog(c.Query("chunk_id"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var logs []*types.ChunkWeightLog
	var total int64
	var err error
	if chunkID != "" {
		logs, total, err = h.feedbackService.ListWeightLogsByChunk(ctx, tenantID, chunkID, page, pageSize)
	} else {
		logs, total, err = h.feedbackService.ListWeightLogs(ctx, tenantID, page, pageSize)
	}
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      logs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *FeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))

	userID, _ := c.Get(types.UserIDContextKey.String())
	operatorID := "admin"
	if uid, ok := userID.(string); ok {
		operatorID = uid
	}

	if err := h.feedbackService.ResetChunkFeedback(ctx, tenantID, chunkID, operatorID); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Feedback data reset successfully",
	})
}
