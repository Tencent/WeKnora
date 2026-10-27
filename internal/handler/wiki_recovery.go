package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ListDocuments godoc
// @Summary List source document Wiki results
// @Description Source ingestion and Wiki results are reported separately.
// @Description Filter failed to locate recoverable Wiki dead letters.
// @Tags Wiki
// @Produce json
// @Param kb_id path string true "Knowledge base ID"
// @Param status query string false "Wiki status" Enums(failed,pending,running,completed,skipped,not_started)
// @Param page query int false "Page number"
// @Param page_size query int false "Page size (1-100)"
// @Success 200 {object} types.WikiDocumentList
// @Security Bearer
// @Security ApiKeyAuth
// @Router /knowledgebase/{kb_id}/wiki/documents [get]
func (h *WikiPageHandler) ListDocuments(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiRecoveryKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	status := c.Query("status")
	switch status {
	case "", "failed", "pending", "running", "completed", "skipped", "not_started":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Wiki status"})
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 || page > 1000000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page"})
		return
	}
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || size < 1 || size > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Page size must be between 1 and 100"})
		return
	}
	var result *types.WikiDocumentList
	result, err = h.recoveryService.ListDocuments(c.Request.Context(), tenantID, kbID, status, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// RetryDocuments godoc
// @Summary Retry failed Wiki operations without reparsing source documents
// @Description Restores up to 100 failed documents and schedules the existing Wiki worker.
// @Description Duplicate requests reuse pending work. Chunks, indexes, and existing Wiki pages are retained.
// @Tags Wiki
// @Accept json
// @Produce json
// @Param kb_id path string true "Knowledge base ID"
// @Param request body WikiRetryRequest true "Source document IDs"
// @Success 200 {object} WikiRetryResponse
// @Security Bearer
// @Security ApiKeyAuth
// @Router /knowledgebase/{kb_id}/wiki/retry [post]
func (h *WikiPageHandler) RetryDocuments(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiRecoveryKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var request WikiRetryRequest
	if err := c.ShouldBindJSON(&request); err != nil ||
		len(request.KnowledgeIDs) == 0 || len(request.KnowledgeIDs) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provide between 1 and 100 source document IDs"})
		return
	}
	for _, id := range request.KnowledgeIDs {
		if strings.TrimSpace(id) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Source document ID is required"})
			return
		}
	}
	n, err := h.recoveryService.RetryDocuments(c.Request.Context(), tenantID, kbID, request.KnowledgeIDs)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, repository.ErrWikiRecoveryNotReady) {
			status = http.StatusConflict
		}
		if errors.Is(err, repository.ErrKnowledgeNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, WikiRetryResponse{Requeued: n})
}

// WikiRetryRequest selects the source documents whose Wiki failed.
type WikiRetryRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids"`
}

// WikiRetryResponse reports newly queued document operations.
type WikiRetryResponse struct {
	Requeued int `json:"requeued"`
}

func (h *WikiPageHandler) validateWikiRecoveryKB(c *gin.Context) (string, uint64, error) {
	kb, err := h.kbService.GetKnowledgeBaseByID(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		return "", 0, err
	}
	if !kb.IsWikiEnabled() {
		return "", 0, fmt.Errorf("wiki feature is not enabled for this knowledge base")
	}
	return kb.ID, kb.TenantID, nil
}
