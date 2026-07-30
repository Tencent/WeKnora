package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type ChunkFeedbackHandler struct {
	service interfaces.ChunkFeedbackService
}

func NewChunkFeedbackHandler(svc interfaces.ChunkFeedbackService) *ChunkFeedbackHandler {
	return &ChunkFeedbackHandler{service: svc}
}

type ChunkFeedbackStatsQuery struct {
	types.Pagination
	MaxPositiveRate   *float64 `form:"max_positive_rate"`
	NeedsOptimization *bool    `form:"needs_optimization"`
}

func (h *ChunkFeedbackHandler) ListKnowledgeBaseChunkFeedbackStats(c *gin.Context) {
	ctx := c.Request.Context()
	knowledgeBaseID := secutils.SanitizeForLog(c.Param("id"))
	if knowledgeBaseID == "" {
		c.Error(errors.NewBadRequestError("knowledge base ID cannot be empty"))
		return
	}

	var query ChunkFeedbackStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 10
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewUnauthorizedError("tenant ID not found"))
		return
	}

	result, err := h.service.ListKnowledgeBaseChunkFeedbackStats(ctx, tenantID, knowledgeBaseID, &query.Pagination, query.MaxPositiveRate, query.NeedsOptimization)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	for _, item := range result.Data.([]*types.ChunkFeedbackStats) {
		if item != nil && item.ContentPreview != "" {
			item.ContentPreview = secutils.SanitizeForDisplay(item.ContentPreview)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

func (h *ChunkFeedbackHandler) ListChunkRecallWeightLogs(c *gin.Context) {
	ctx := c.Request.Context()
	knowledgeBaseID := secutils.SanitizeForLog(c.Param("id"))
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if knowledgeBaseID == "" || chunkID == "" {
		c.Error(errors.NewBadRequestError("knowledge base ID and chunk ID cannot be empty"))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewUnauthorizedError("tenant ID not found"))
		return
	}

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	logs, err := h.service.ListChunkRecallWeightLogs(ctx, tenantID, knowledgeBaseID, chunkID, limit)
	if err != nil {
		if err == service.ErrChunkFeedbackChunkNotFound {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    logs,
	})
}

type UpdateChunkWeightRequest struct {
	Weight float64 `json:"weight" binding:"required"`
}

func (h *ChunkFeedbackHandler) UpdateChunkWeight(c *gin.Context) {
	ctx := c.Request.Context()
	knowledgeBaseID := secutils.SanitizeForLog(c.Param("id"))
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if knowledgeBaseID == "" || chunkID == "" {
		c.Error(errors.NewBadRequestError("knowledge base ID and chunk ID cannot be empty"))
		return
	}

	var req UpdateChunkWeightRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("invalid request body"))
		return
	}

	uidVal, ok := c.Get(types.UserIDContextKey.String())
	if !ok {
		c.Error(errors.NewUnauthorizedError("user ID not found"))
		return
	}
	userID, _ := uidVal.(string)
	if userID == "" {
		c.Error(errors.NewUnauthorizedError("user ID not found"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewUnauthorizedError("tenant ID not found"))
		return
	}

	if err := h.service.UpdateChunkWeight(ctx, tenantID, knowledgeBaseID, chunkID, userID, req.Weight); err != nil {
		if err == service.ErrChunkFeedbackChunkNotFound {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		if err.Error() == "weight must be between 0.1 and 10.0" {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *ChunkFeedbackHandler) ResetChunkFeedback(c *gin.Context) {
	ctx := c.Request.Context()
	knowledgeBaseID := secutils.SanitizeForLog(c.Param("id"))
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if knowledgeBaseID == "" || chunkID == "" {
		c.Error(errors.NewBadRequestError("knowledge base ID and chunk ID cannot be empty"))
		return
	}

	uidVal, ok := c.Get(types.UserIDContextKey.String())
	if !ok {
		c.Error(errors.NewUnauthorizedError("user ID not found"))
		return
	}
	userID, _ := uidVal.(string)
	if userID == "" {
		c.Error(errors.NewUnauthorizedError("user ID not found"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewUnauthorizedError("tenant ID not found"))
		return
	}

	if err := h.service.ResetChunkFeedback(ctx, tenantID, knowledgeBaseID, chunkID, userID); err != nil {
		if err == service.ErrChunkFeedbackChunkNotFound {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
