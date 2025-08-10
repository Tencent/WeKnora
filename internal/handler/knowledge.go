package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// KnowledgeHandler processes HTTP requests related to knowledge resources
type KnowledgeHandler struct {
	kgService interfaces.KnowledgeService
	kbService interfaces.KnowledgeBaseService
}

// NewKnowledgeHandler creates a new knowledge handler instance
func NewKnowledgeHandler(
	kgService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
) *KnowledgeHandler {
	return &KnowledgeHandler{kgService: kgService, kbService: kbService}
}

// validateKnowledgeBaseAccess validates access permissions to a knowledge base
// Returns the knowledge base, the knowledge base ID, and any errors encountered
func (h *KnowledgeHandler) validateKnowledgeBaseAccess(c *gin.Context) (*types.KnowledgeBase, string, error) {
	ctx := c.Request.Context()

	// Get knowledge base ID from URL path parameter
	kbID := c.Param("id")
	if kbID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}

	// Get knowledge base details
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, kbID, errors.NewInternalServerError(err.Error())
	}

	// Verify tenant permissions
	if kb.TenantID != c.GetUint(types.TenantIDContextKey.String()) {
		logger.Warnf(
			ctx,
			"Permission denied to access this knowledge base, tenant ID mismatch, "+
				"requested tenant ID: %d, knowledge base tenant ID: %d",
			c.GetUint(types.TenantIDContextKey.String()),
			kb.TenantID,
		)
		return nil, kbID, errors.NewForbiddenError("Permission denied to access this knowledge base")
	}

	return kb, kbID, nil
}

// handleDuplicateKnowledgeError handles cases where duplicate knowledge is detected
// Returns true if the error was a duplicate error and was handled, false otherwise
func (h *KnowledgeHandler) handleDuplicateKnowledgeError(c *gin.Context,
	err error, knowledge *types.Knowledge, duplicateType string,
) bool {
	if dupErr, ok := err.(*types.DuplicateKnowledgeError); ok {
		ctx := c.Request.Context()
		logger.Warnf(ctx, "Detected duplicate %s: %s", duplicateType, dupErr.Error())
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": dupErr.Error(),
			"data":    knowledge, // knowledge contains the existing document
			"code":    fmt.Sprintf("duplicate_%s", duplicateType),
		})
		return true
	}
	return false
}

// CreateKnowledgeFromFile handles requests to create knowledge from an uploaded file
func (h *KnowledgeHandler) CreateKnowledgeFromFile(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from file")

	// Validate access to the knowledge base
	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		logger.Error(ctx, "File upload failed", err)
		c.Error(errors.NewBadRequestError("File upload failed").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "File upload successful, filename: %s, size: %.2f KB", file.Filename, float64(file.Size)/1024)
	logger.Infof(ctx, "Creating knowledge, knowledge base ID: %s, filename: %s", kbID, file.Filename)

	// Parse metadata if provided
	var metadata map[string]string
	metadataStr := c.PostForm("metadata")
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
			logger.Error(ctx, "Failed to parse metadata", err)
			c.Error(errors.NewBadRequestError("Invalid metadata format").WithDetails(err.Error()))
			return
		}
		logger.Infof(ctx, "Received file metadata: %v", metadata)
	}

	enableMultimodelForm := c.PostForm("enable_multimodel")
	var enableMultimodel *bool
	if enableMultimodelForm != "" {
		parseBool, err := strconv.ParseBool(enableMultimodelForm)
		if err != nil {
			logger.Error(ctx, "Failed to parse enable_multimodel", err)
			c.Error(errors.NewBadRequestError("Invalid enable_multimodel format").WithDetails(err.Error()))
			return
		}
		enableMultimodel = &parseBool
	}

	// Create knowledge entry from the file
	knowledge, err := h.kgService.CreateKnowledgeFromFile(ctx, kbID, file, metadata, enableMultimodel)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "file") {
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge created successfully, ID: %s, title: %s", knowledge.ID, knowledge.Title)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateKnowledgeFromURL handles requests to create knowledge from a URL
func (h *KnowledgeHandler) CreateKnowledgeFromURL(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from URL")

	// Validate access to the knowledge base
	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// Parse URL from request body
	var req struct {
		URL              string `json:"url" binding:"required"`
		EnableMultimodel *bool  `json:"enable_multimodel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse URL request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Received URL request: %s", req.URL)
	logger.Infof(ctx, "Creating knowledge from URL, knowledge base ID: %s, URL: %s", kbID, req.URL)

	// Create knowledge entry from the URL
	knowledge, err := h.kgService.CreateKnowledgeFromURL(ctx, kbID, req.URL, req.EnableMultimodel)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "url") {
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge created successfully from URL, ID: %s, title: %s", knowledge.ID, knowledge.Title)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// GetKnowledge retrieves a knowledge entry by its ID
func (h *KnowledgeHandler) GetKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge")

	// Get knowledge ID from URL path parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge, ID: %s", id)
	knowledge, err := h.kgService.GetKnowledgeByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge retrieved successfully, ID: %s, title: %s", knowledge.ID, knowledge.Title)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// ListKnowledge retrieves a paginated list of knowledge entries from a knowledge base
func (h *KnowledgeHandler) ListKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge list")

	// Get knowledge base ID from URL path parameter
	kbID := c.Param("id")
	if kbID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base ID cannot be empty"))
		return
	}

	// Parse pagination parameters from query string
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge list under knowledge base, knowledge base ID: %s, page: %d, page size: %d",
		kbID, pagination.Page, pagination.PageSize)

	// Retrieve paginated knowledge entries
	result, err := h.kgService.ListPagedKnowledgeByKnowledgeBaseID(ctx, kbID, &pagination)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge list retrieved successfully, knowledge base ID: %s, total: %d", kbID, result.Total)
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// DeleteKnowledge handles requests to delete a knowledge entry by its ID
func (h *KnowledgeHandler) DeleteKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting knowledge")

	// Get knowledge ID from URL path parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting knowledge, ID: %s", id)
	err := h.kgService.DeleteKnowledge(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge deleted successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Deleted successfully",
	})
}

// DownloadKnowledgeFile handles requests to download a file associated with a knowledge entry
func (h *KnowledgeHandler) DownloadKnowledgeFile(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start downloading knowledge file")

	// Get knowledge ID from URL path parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge file, ID: %s", id)

	// Get file content and filename
	file, filename, err := h.kgService.GetKnowledgeFile(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve file").WithDetails(err.Error()))
		return
	}
	defer file.Close()

	logger.Infof(ctx, "Knowledge file retrieved successfully, ID: %s, filename: %s", id, filename)

	// Set response headers for file download
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Expires", "0")
	c.Header("Cache-Control", "must-revalidate")
	c.Header("Pragma", "public")

	// Stream file content to response
	c.Stream(func(w io.Writer) bool {
		if _, err := io.Copy(w, file); err != nil {
			logger.Errorf(ctx, "Failed to send file: %v", err)
			return false
		}
		logger.Debug(ctx, "File sending completed")
		return false
	})
}

// GetKnowledgeBatchRequest defines parameters for batch knowledge retrieval
type GetKnowledgeBatchRequest struct {
	IDs []string `form:"ids" binding:"required"` // List of knowledge IDs
}

// GetKnowledgeBatch handles requests to retrieve multiple knowledge entries in a batch
func (h *KnowledgeHandler) GetKnowledgeBatch(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start batch retrieving knowledge")

	// Get tenant ID from context
	tenantID, ok := c.Get(types.TenantIDContextKey.String())
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// Parse request parameters from query string
	var req GetKnowledgeBatchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Batch retrieving knowledge, tenant ID: %d, number of knowledge IDs: %d",
		tenantID.(uint), len(req.IDs),
	)

	// Retrieve knowledge entries in batch
	knowledges, err := h.kgService.GetKnowledgeBatch(ctx, tenantID.(uint), req.IDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve knowledge list").WithDetails(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Batch knowledge retrieval successful, requested count: %d, returned count: %d",
		len(req.IDs), len(knowledges),
	)

	// Return results
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledges,
	})
}

func (h *KnowledgeHandler) UpdateKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start Update knowledge")

	// Get knowledge ID from URL path parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var knowledge types.Knowledge
	if err := c.ShouldBindJSON(&knowledge); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if err := h.kgService.UpdateKnowledge(ctx, &knowledge); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge updated successfully, knowledge ID: %s", knowledge.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk updated successfully",
	})
}

// UpdateImageInfo updates a chunk's properties
func (h *KnowledgeHandler) UpdateImageInfo(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating image info")

	// Get knowledge ID from URL path parameter
	id := c.Param("id")
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}
	chunkID := c.Param("chunk_id")
	if id == "" {
		logger.Error(ctx, "Chunk ID is empty")
		c.Error(errors.NewBadRequestError("Chunk ID cannot be empty"))
		return
	}

	var request struct {
		ImageInfo string `json:"image_info"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Update chunk properties
	logger.Infof(ctx, "Updating knowledge chunk, knowledge ID: %s, chunk ID: %s", id, chunkID)
	err := h.kgService.UpdateImageInfo(ctx, id, chunkID, request.ImageInfo)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge chunk updated successfully, knowledge ID: %s, chunk ID: %s", id, chunkID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk image updated successfully",
	})
}
