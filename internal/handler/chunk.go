package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// ChunkHandler defines HTTP handlers for chunk operations
type ChunkHandler struct {
	service interfaces.ChunkService
}

// NewChunkHandler creates a new chunk handler
func NewChunkHandler(service interfaces.ChunkService) *ChunkHandler {
	return &ChunkHandler{service: service}
}

// ListKnowledgeChunks lists all chunks for a given knowledge ID
func (h *ChunkHandler) ListKnowledgeChunks(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start retrieving knowledge chunks list")

	knowledgeID := c.Param("knowledge_id")
	if knowledgeID == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Parse pagination parameters
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge chunks list, knowledge ID: %s, page: %d, page size: %d",
		knowledgeID, pagination.Page, pagination.PageSize)

	// Use pagination for query
	result, err := h.service.ListPagedChunksByKnowledgeID(ctx, knowledgeID, &pagination)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 对 chunk 内容进行安全清理
	for _, chunk := range result.Data.([]*types.Chunk) {
		if chunk.Content != "" {
			chunk.Content = secutils.SanitizeForDisplay(chunk.Content)
		}
	}

	logger.Infof(
		ctx, "Successfully retrieved knowledge chunks list, knowledge ID: %s, total: %d",
		knowledgeID, result.Total,
	)
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// UpdateChunkRequest defines the request structure for updating a chunk
type UpdateChunkRequest struct {
	Content    string    `json:"content"`
	Embedding  []float32 `json:"embedding"`
	ChunkIndex int       `json:"chunk_index"`
	IsEnabled  bool      `json:"is_enabled"`
	StartAt    int       `json:"start_at"`
	EndAt      int       `json:"end_at"`
	ImageInfo  string    `json:"image_info"`
}

// validateAndGetChunk validates request parameters and retrieves the chunk
// Returns chunk information, knowledge ID, and error
func (h *ChunkHandler) validateAndGetChunk(c *gin.Context) (*types.Chunk, string, error) {
	ctx := c.Request.Context()

	// Validate knowledge ID
	knowledgeID := c.Param("knowledge_id")
	if knowledgeID == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge ID cannot be empty")
	}

	// Validate chunk ID
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Chunk ID is empty")
		return nil, knowledgeID, errors.NewBadRequestError("Chunk ID cannot be empty")
	}

	// Get tenant ID from context
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		return nil, knowledgeID, errors.NewUnauthorizedError("Unauthorized")
	}

	logger.Infof(ctx, "Retrieving knowledge chunk information, knowledge ID: %s, chunk ID: %s", knowledgeID, id)

	// Get existing chunk
	chunk, err := h.service.GetChunkByID(ctx, knowledgeID, id)
	if err != nil {
		if err == service.ErrChunkNotFound {
			logger.Warnf(ctx, "Chunk not found, knowledge ID: %s, chunk ID: %s", knowledgeID, id)
			return nil, knowledgeID, errors.NewNotFoundError("Chunk not found")
		}
		logger.ErrorWithFields(ctx, err, nil)
		return nil, knowledgeID, errors.NewInternalServerError(err.Error())
	}

	// Validate tenant ID
	if chunk.TenantID != tenantID.(uint) || chunk.KnowledgeID != knowledgeID {
		logger.Warnf(
			ctx,
			"Tenant has no permission to access chunk, knowledge ID: %s, chunk ID: %s, req tenant: %d, chunk tenant: %d",
			knowledgeID, id, tenantID.(uint), chunk.TenantID,
		)
		return nil, knowledgeID, errors.NewForbiddenError("No permission to access this chunk")
	}

	return chunk, knowledgeID, nil
}

// UpdateChunk updates a chunk's properties
func (h *ChunkHandler) UpdateChunk(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating knowledge chunk")

	// Validate parameters and get chunk
	chunk, knowledgeID, err := h.validateAndGetChunk(c)
	if err != nil {
		c.Error(err)
		return
	}
	var req UpdateChunkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Update chunk properties
	if req.Content != "" {
		chunk.Content = req.Content
	}

	chunk.IsEnabled = req.IsEnabled

	logger.Infof(ctx, "Updating knowledge chunk, knowledge ID: %s, chunk ID: %s", knowledgeID, chunk.ID)

	if err := h.service.UpdateChunk(ctx, chunk); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge chunk updated successfully, knowledge ID: %s, chunk ID: %s", knowledgeID, chunk.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    chunk,
	})
}

// DeleteChunk deletes a specific chunk
func (h *ChunkHandler) DeleteChunk(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting knowledge chunk")

	// Validate parameters and get chunk
	chunk, knowledgeID, err := h.validateAndGetChunk(c)
	if err != nil {
		c.Error(err)
		return
	}

	logger.Infof(ctx, "Deleting knowledge chunk, knowledge ID: %s, chunk ID: %s", knowledgeID, chunk.ID)

	if err := h.service.DeleteChunk(ctx, chunk.ID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge chunk deleted successfully, knowledge ID: %s, chunk ID: %s", knowledgeID, chunk.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Chunk deleted",
	})
}

// DeleteChunksByKnowledgeID deletes all chunks for a given knowledge ID
func (h *ChunkHandler) DeleteChunksByKnowledgeID(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting all chunks under knowledge")

	knowledgeID := c.Param("knowledge_id")
	if knowledgeID == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Get tenant ID from context
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	logger.Infof(ctx, "Deleting all chunks under knowledge, knowledge ID: %s, tenant ID: %d", knowledgeID, tenantID.(uint))

	// Delete all chunks under the knowledge
	err := h.service.DeleteChunksByKnowledgeID(ctx, knowledgeID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "All chunks under knowledge deleted successfully, knowledge ID: %s", knowledgeID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "All chunks under knowledge deleted",
	})
}
