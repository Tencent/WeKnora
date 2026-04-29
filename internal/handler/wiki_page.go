package handler

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// WikiPageHandler handles HTTP requests for wiki page operations
type WikiPageHandler struct {
	wikiService interfaces.WikiPageService
	kbService   interfaces.KnowledgeBaseService
	lintService *service.WikiLintService
}

// NewWikiPageHandler creates a new wiki page handler
func NewWikiPageHandler(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	lintService *service.WikiLintService,
) *WikiPageHandler {
	return &WikiPageHandler{
		wikiService: wikiService,
		kbService:   kbService,
		lintService: lintService,
	}
}

// validateWikiKB validates that the KB exists and is a wiki type
func (h *WikiPageHandler) validateWikiKB(c *gin.Context) (string, uint64, error) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("kb_id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	if kbID == "" {
		return "", 0, errors.NewBadRequestError("Knowledge base ID is required")
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return "", 0, errors.NewNotFoundError("Knowledge base not found")
	}

	if !kb.IsWikiEnabled() {
		return "", 0, errors.NewBadRequestError("Wiki feature is not enabled for this knowledge base")
	}

	return kbID, tenantID, nil
}

// getSlugParam extracts and cleans the slug from gin's wildcard path param
func getSlugParam(c *gin.Context) string {
	slug := c.Param("slug")
	// gin wildcard params include a leading "/"
	slug = strings.TrimPrefix(slug, "/")
	return strings.TrimSpace(slug)
}

// ListPages godoc
// @Summary      List wiki pages
// @Description  List wiki pages with optional filtering and pagination
// @Tags         Wiki
// @Produce      json
// @Param        kb_id      path      string  true   "Knowledge base ID"
// @Param        page_type  query     string  false  "Filter by page type"
// @Param        status     query     string  false  "Filter by status"
// @Param        query      query     string  false  "Full-text search"
// @Param        page       query     int     false  "Page number"
// @Param        page_size  query     int     false  "Page size"
// @Param        sort_by    query     string  false  "Sort field"
// @Param        sort_order query     string  false  "Sort order (asc/desc)"
// @Success      200  {object}  types.WikiPageListResponse
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/pages [get]
func (h *WikiPageHandler) ListPages(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	req := &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		PageType:        c.Query("page_type"),
		Status:          c.Query("status"),
		Query:           c.Query("query"),
		Page:            page,
		PageSize:        pageSize,
		SortBy:          c.DefaultQuery("sort_by", "updated_at"),
		SortOrder:       c.DefaultQuery("sort_order", "desc"),
	}

	resp, err := h.wikiService.ListPages(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreatePage godoc
// @Summary      Create a wiki page
// @Description  Create a new wiki page in the knowledge base
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string          true  "Knowledge base ID"
// @Param        page   body  types.WikiPage  true  "Wiki page data"
// @Success      201  {object}  types.WikiPage
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/pages [post]
func (h *WikiPageHandler) CreatePage(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var page types.WikiPage
	if err := c.ShouldBindJSON(&page); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	page.KnowledgeBaseID = kbID
	page.TenantID = tenantID

	created, err := h.wikiService.CreatePage(c.Request.Context(), &page)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, created)
}

// GetPage godoc
// @Summary      Get a wiki page by slug
// @Description  Retrieve a wiki page by its slug
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Param        slug   path  string  true  "Page slug"
// @Success      200  {object}  types.WikiPage
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/pages/{slug} [get]
func (h *WikiPageHandler) GetPage(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug := getSlugParam(c)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Page slug is required"})
		return
	}

	page, err := h.wikiService.GetPageBySlug(c.Request.Context(), kbID, slug)
	if err != nil {
		if stderrors.Is(err, repository.ErrWikiPageNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, page)
}

// UpdatePage godoc
// @Summary      Update a wiki page
// @Description  Update an existing wiki page by slug
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string          true  "Knowledge base ID"
// @Param        slug   path  string          true  "Page slug"
// @Param        page   body  types.WikiPage  true  "Updated wiki page data"
// @Success      200  {object}  types.WikiPage
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/pages/{slug} [put]
func (h *WikiPageHandler) UpdatePage(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug := getSlugParam(c)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Page slug is required"})
		return
	}

	var page types.WikiPage
	if err := c.ShouldBindJSON(&page); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	page.KnowledgeBaseID = kbID
	page.TenantID = tenantID
	page.Slug = slug

	updated, err := h.wikiService.UpdatePage(c.Request.Context(), &page)
	if err != nil {
		if stderrors.Is(err, repository.ErrWikiPageNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updated)
}

// DeletePage godoc
// @Summary      Delete a wiki page
// @Description  Soft-delete a wiki page by slug
// @Tags         Wiki
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Param        slug   path  string  true  "Page slug"
// @Success      204
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/pages/{slug} [delete]
func (h *WikiPageHandler) DeletePage(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug := getSlugParam(c)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Page slug is required"})
		return
	}

	if err := h.wikiService.DeletePage(c.Request.Context(), kbID, slug); err != nil {
		if stderrors.Is(err, repository.ErrWikiPageNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// GetIndex godoc
// @Summary      Get wiki index page
// @Description  Returns the wiki index page (creates default if not exists)
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  types.WikiPage
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/index [get]
func (h *WikiPageHandler) GetIndex(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	page, err := h.wikiService.GetIndex(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, page)
}

// GetLog godoc
// @Summary      Get wiki operation log
// @Description  Returns the wiki operation log page (creates default if not exists)
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  types.WikiPage
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/log [get]
func (h *WikiPageHandler) GetLog(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	page, err := h.wikiService.GetLog(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, page)
}

// GetGraph godoc
// @Summary      Get wiki link graph
// @Description  Returns the wiki link graph data for visualization
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  types.WikiGraphData
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/graph [get]
func (h *WikiPageHandler) GetGraph(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	graph, err := h.wikiService.GetGraph(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, graph)
}

// GetStats godoc
// @Summary      Get wiki statistics
// @Description  Returns aggregate statistics about the wiki
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  types.WikiStats
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/stats [get]
func (h *WikiPageHandler) GetStats(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stats, err := h.wikiService.GetStats(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// ListIssues godoc
// @Summary      List wiki page issues
// @Description  List issues flagged on wiki pages with optional filtering
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path   string  true   "Knowledge base ID"
// @Param        slug   query  string  false  "Filter by page slug"
// @Param        status query  string  false  "Filter by status (pending, ignored, resolved)"
// @Success      200  {array}  types.WikiPageIssue
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/issues [get]
func (h *WikiPageHandler) ListIssues(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug := c.Query("slug")
	status := c.Query("status")

	issues, err := h.wikiService.ListIssues(c.Request.Context(), kbID, slug, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, issues)
}

// UpdateIssueStatus godoc
// @Summary      Update wiki page issue status
// @Description  Update the status of a flagged wiki page issue
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id    path  string  true  "Knowledge base ID"
// @Param        issue_id path  string  true  "Issue ID"
// @Param        status   body  object  true  "New status {'status': 'ignored'}"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/issues/{issue_id}/status [put]
func (h *WikiPageHandler) UpdateIssueStatus(c *gin.Context) {
	_, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	issueID := secutils.SanitizeForLog(c.Param("issue_id"))
	if issueID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Issue ID is required"})
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	validStatuses := map[string]bool{"pending": true, "ignored": true, "resolved": true}
	if !validStatuses[req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status. Must be pending, ignored, or resolved"})
		return
	}

	if err := h.wikiService.UpdateIssueStatus(c.Request.Context(), issueID, req.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Issue status updated successfully"})
}

// SearchPages godoc
// @Summary      Search wiki pages
// @Description  Full-text search over wiki pages
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path   string  true   "Knowledge base ID"
// @Param        q      query  string  true   "Search query"
// @Param        limit  query  int     false  "Max results (default 10)"
// @Success      200  {array}  types.WikiPage
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/search [get]
func (h *WikiPageHandler) SearchPages(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Search query 'q' is required"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	pages, err := h.wikiService.SearchPages(c.Request.Context(), kbID, query, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"pages": pages})
}

// RebuildLinks godoc
// @Summary      Rebuild wiki links
// @Description  Re-parse all pages and rebuild bidirectional link references
// @Tags         Wiki
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  map[string]string
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/rebuild-links [post]
func (h *WikiPageHandler) RebuildLinks(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.wikiService.RebuildLinks(c.Request.Context(), kbID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Links rebuilt successfully"})
}

// Lint godoc
// @Summary      Run wiki lint
// @Description  Perform a comprehensive health check on the wiki
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  service.WikiLintReport
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/lint [get]
func (h *WikiPageHandler) Lint(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	report, err := h.lintService.RunLint(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, report)
}

// AutoFix godoc
// @Summary      Auto-fix wiki issues
// @Description  Automatically fix fixable wiki issues (broken links, etc.)
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /api/v1/knowledgebase/{kb_id}/wiki/auto-fix [post]
func (h *WikiPageHandler) AutoFix(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fixed, err := h.lintService.AutoFix(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"fixed": fixed, "message": fmt.Sprintf("Auto-fixed %d issues", fixed)})
}
