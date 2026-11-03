package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListDocumentProgress godoc
// @Summary List document ingestion and durable Wiki progress
// @Description SystemAdmin metadata view with separate current attempts and historical failures. No raw payloads.
// @Tags System Admin
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param page_size query int false "Page size, at most 100" default(25)
// @Param status query string false "Document filter" Enums(all,unfinished,failed,completed) default(unfinished)
// @Param kb_id query string false "Knowledge-base ID"
// @Param search query string false "Title, filename or knowledge-base search, at most 200 characters"
// @Success 200 {object} handler.DataResponse{data=service.DocumentProgressResponse}
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Security Bearer
// @Router /system/admin/runtime/documents [get]
func (h *SystemHandler) ListDocumentProgress(c *gin.Context) {
	page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, sizeErr := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	filter := service.DocumentProgressFilter{
		Page: page, PageSize: size, Status: c.DefaultQuery("status", "unfinished"),
		KnowledgeBaseID: c.Query("kb_id"), Search: c.Query("search"),
	}
	if pageErr != nil || sizeErr != nil || !filter.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid document progress filter"})
		return
	}
	if h.documentProgress == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Document progress is unavailable"})
		return
	}
	data, err := h.documentProgress.List(c.Request.Context(), filter)
	if err != nil {
		logger.Errorf(c.Request.Context(), "list document progress: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to read document progress"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetDocumentProgress godoc
// @Summary Read a document's current stages and historical failures
// @Description Returns up to 50 original failures with bounded error previews. Excludes bodies and task payloads.
// @Tags System Admin
// @Produce json
// @Param knowledge_id path string true "Knowledge ID"
// @Success 200 {object} handler.DataResponse{data=service.DocumentProgressDetail}
// @Failure 404 {object} map[string]string
// @Security Bearer
// @Router /system/admin/runtime/documents/{knowledge_id} [get]
func (h *SystemHandler) GetDocumentProgress(c *gin.Context) {
	id := c.Param("knowledge_id")
	if id == "" || len(id) > 64 || strings.TrimSpace(id) != id {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid knowledge ID"})
		return
	}
	if h.documentProgress == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Document progress is unavailable"})
		return
	}
	data, err := h.documentProgress.Detail(c.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
		return
	}
	if err != nil {
		logger.Errorf(c.Request.Context(), "read document progress: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to read document progress"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// StartDocumentWiki godoc
// @Summary Request immediate Wiki work for existing durable document operations
// @Description Schedules up to 100 eligible documents, oldest first, under existing worker claims and limits.
// @Description Ingestion and failure history are retained. Partial handoff errors include accepted counts.
// @Tags System Admin
// @Accept json
// @Produce json
// @Param request body service.DocumentWikiStartRequest true "One knowledge ID or a bounded count and optional filters"
// @Success 200 {object} handler.DataResponse{data=service.DocumentWikiStartResult}
// @Failure 400 {object} map[string]string
// @Failure 503 {object} handler.DataResponse{data=service.DocumentWikiStartResult}
// @Security Bearer
// @Router /system/admin/runtime/documents/wiki-start [post]
func (h *SystemHandler) StartDocumentWiki(c *gin.Context) {
	var request service.DocumentWikiStartRequest
	if err := c.ShouldBindJSON(&request); err != nil || !request.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Use a count between 1 and 100; one document requires count 1"})
		return
	}
	if h.documentProgress == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Wiki task queue is unavailable"})
		return
	}
	result, err := h.documentProgress.StartWiki(c.Request.Context(), request)
	if result != nil && result.Started > 0 {
		h.emitQueueTaskAudit(c.Request.Context(), types.AuditActionSystemQueueTaskRunNow,
			types.QueueWiki, request.KnowledgeID, map[string]string{
				"scope": "durable_document", "requested": strconv.Itoa(result.Requested),
				"accepted": strconv.Itoa(result.Started),
			})
	}
	if err != nil {
		logger.Errorf(c.Request.Context(), "schedule document Wiki work: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false, "error": "Wiki queue handoff incomplete; accepted work and durable progress are retained",
			"data": result,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
