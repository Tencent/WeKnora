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
	wikiService     interfaces.WikiPageService
	kbService       interfaces.KnowledgeBaseService
	lintService     *service.WikiLintService
	logEntryService interfaces.WikiLogEntryService
}

// NewWikiPageHandler creates a new wiki page handler
func NewWikiPageHandler(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	lintService *service.WikiLintService,
	logEntryService interfaces.WikiLogEntryService,
) *WikiPageHandler {
	return &WikiPageHandler{
		wikiService:     wikiService,
		kbService:       kbService,
		lintService:     lintService,
		logEntryService: logEntryService,
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
// @Param        page_type  query     string  false  "Filter by page type; comma-separated for multiple (e.g. entity,concept)"
// @Param        status     query     string  false  "Filter by status"
// @Param        query      query     string  false  "Full-text search"
// @Param        page       query     int     false  "Page number"
// @Param        page_size  query     int     false  "Page size"
// @Param        sort_by    query     string  false  "Sort field"
// @Param        sort_order query     string  false  "Sort order (asc/desc)"
// @Success      200  {object}  types.WikiPageListResponse
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/pages [get]
func (h *WikiPageHandler) ListPages(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	categoryPath := parseWikiCategoryPath(c.Query("category_path"))
	// folder_id is an exact placement filter. An explicitly-present but empty
	// value means "root" (folder_id = ''); an absent param means "no filter".
	var folderID *string
	if raw, ok := c.GetQuery("folder_id"); ok {
		raw = strings.TrimSpace(raw)
		folderID = &raw
	}
	var categoryDepth *int
	if raw := c.Query("category_depth"); raw != "" {
		if depth, parseErr := strconv.Atoi(raw); parseErr == nil && depth >= 0 {
			categoryDepth = &depth
		}
	}

	req := &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		PageType:        c.Query("page_type"),
		Status:          c.Query("status"),
		Query:           c.Query("query"),
		FolderID:        folderID,
		CategoryPath:    types.StringArray(categoryPath),
		CategoryDepth:   categoryDepth,
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

// ListFolders godoc
// @Summary      List wiki folders
// @Description  Retrieve the direct child folders of a parent folder (parent_id empty = root level), each with its page count and a has-children flag for the directory tree.
// @Tags         Wiki
// @Produce      json
// @Param        kb_id     path   string  true   "Knowledge base ID"
// @Param        parent_id query  string  false  "Parent folder id (empty = root)"
// @Success      200  {object}  types.WikiFolderListResponse
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/folders [get]
func (h *WikiPageHandler) ListFolders(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	parentID := strings.TrimSpace(c.Query("parent_id"))
	var pageTypes []string
	if raw := strings.TrimSpace(c.Query("page_types")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				pageTypes = append(pageTypes, p)
			}
		}
	}
	folders, err := h.wikiService.ListChildFolders(c.Request.Context(), kbID, parentID, pageTypes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if folders == nil {
		folders = []types.WikiFolderNode{}
	}
	c.JSON(http.StatusOK, types.WikiFolderListResponse{ParentID: parentID, Folders: folders})
}

// CreateFolder godoc
// @Summary      Create a wiki folder
// @Description  Create a new (initially empty) directory node under parent_id
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string                       true  "Knowledge base ID"
// @Param        folder body  types.WikiFolderCreateRequest true  "Folder data"
// @Success      201  {object}  types.WikiFolder
// @Failure      400  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/folders [post]
func (h *WikiPageHandler) CreateFolder(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req types.WikiFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	folder, err := h.wikiService.CreateFolder(c.Request.Context(), kbID, tenantID, strings.TrimSpace(req.ParentID), req.Name)
	if err != nil {
		writeWikiFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, folder)
}

// UpdateFolder godoc
// @Summary      Rename or move a wiki folder
// @Description  Rename and/or reparent a folder; the whole subtree's paths and the affected pages' cached paths are recomputed
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id     path  string                        true  "Knowledge base ID"
// @Param        folder_id path  string                        true  "Folder ID"
// @Param        folder    body  types.WikiFolderUpdateRequest true  "Folder update"
// @Success      200  {object}  types.WikiFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/folders/{folder_id} [put]
func (h *WikiPageHandler) UpdateFolder(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder ID is required"})
		return
	}
	var req types.WikiFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	folder, err := h.wikiService.RenameOrMoveFolder(
		c.Request.Context(), kbID, folderID, req.Name, strings.TrimSpace(req.ParentID), req.MoveParent)
	if err != nil {
		writeWikiFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary      Delete an empty wiki folder
// @Description  Delete a folder that has no pages and no child folders
// @Tags         Wiki
// @Param        kb_id     path  string  true  "Knowledge base ID"
// @Param        folder_id path  string  true  "Folder ID"
// @Success      204
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/folders/{folder_id} [delete]
func (h *WikiPageHandler) DeleteFolder(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder ID is required"})
		return
	}
	if err := h.wikiService.DeleteFolder(c.Request.Context(), kbID, folderID); err != nil {
		writeWikiFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// MovePage godoc
// @Summary      Move a wiki page into a folder
// @Description  Relocate a page (identified by slug in the body) into a folder (folder_id empty = root); the page's cached category path is recomputed
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"
// @Param        move   body  types.WikiPageMoveRequest true "Move target"
// @Success      200  {object}  types.WikiPage
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/move-page [put]
func (h *WikiPageHandler) MovePage(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req types.WikiPageMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Page slug is required"})
		return
	}
	page, err := h.wikiService.MovePage(c.Request.Context(), kbID, slug, strings.TrimSpace(req.FolderID))
	if err != nil {
		writeWikiFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// writeWikiFolderError maps folder/page service errors to HTTP status codes.
func writeWikiFolderError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, repository.ErrWikiFolderNotFound), stderrors.Is(err, repository.ErrWikiPageNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case stderrors.Is(err, repository.ErrWikiFolderConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func parseWikiCategoryPath(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
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
// @Router       /knowledgebase/{kb_id}/wiki/pages [post]
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
// @Router       /knowledgebase/{kb_id}/wiki/pages/{slug} [get]
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
// @Router       /knowledgebase/{kb_id}/wiki/pages/{slug} [put]
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
// @Router       /knowledgebase/{kb_id}/wiki/pages/{slug} [delete]
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
// @Summary      Get wiki index view
// @Description  Returns the wiki index as intro text plus per-type paginated
// @Description  directory groups. The heavy directory markdown that used to
// @Description  live in wiki_pages.content was replaced with this structured
// @Description  response so a KB with tens of thousands of pages no longer
// @Description  materializes megabytes of TEXT on every index open.
// @Tags         Wiki
// @Produce      json
// @Param        kb_id   path   string  true   "Knowledge base ID"
// @Param        types   query  string  false  "Comma-separated page types (default: all content types)"
// @Param        limit   query  int     false  "Per-group window size, 1-200 (default 50)"
// @Param        cursor  query  string  false  "Opaque offset cursor from previous response"
// @Success      200  {object}  types.WikiIndexResponse
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/index [get]
func (h *WikiPageHandler) GetIndex(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var pageTypes []string
	if raw := c.Query("types"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				pageTypes = append(pageTypes, t)
			}
		}
	}

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			limit = v
		}
	}

	resp, err := h.wikiService.GetIndexView(c.Request.Context(), kbID, pageTypes, limit, c.Query("cursor"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetLog godoc
// @Summary      Get wiki operation log
// @Description  Returns a paginated feed of wiki operation events (ingest, retract, ...)
// @Description  newest-first. Pagination is cursor-based: pass `next_cursor` from the
// @Description  previous response back as `cursor` to fetch the next page.
// @Tags         Wiki
// @Produce      json
// @Param        kb_id   path   string  true   "Knowledge base ID"
// @Param        cursor  query  string  false  "Opaque cursor from the previous page (empty = newest)"
// @Param        limit   query  int     false  "Page size, 1-200 (default 50)"
// @Success      200  {object}  types.WikiLogEntryListResponse
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/log [get]
func (h *WikiPageHandler) GetLog(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cursor := c.Query("cursor")
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			limit = v
		}
	}

	resp, err := h.logEntryService.List(c.Request.Context(), kbID, cursor, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Graph query parameter bounds. The defaults cap an `overview` request at
// 500 nodes — comfortably renderable in the frontend's hand-rolled SVG
// force simulation — while the hard max of 2000 is the upper bound a
// power user can opt into before rendering gets choppy. Ego depth is
// capped at 3 hops because the node population grows super-linearly with
// depth and wider searches are better served by repeated ego jumps.
const (
	wikiGraphDefaultLimit = 500
	wikiGraphMaxLimit     = 2000
	wikiGraphMaxDepth     = 3
	wikiGraphDefaultDepth = 1
)

// GetGraph godoc
// @Summary      Get wiki link graph
// @Description  Returns a slice of the wiki link graph for visualization. Supports
// @Description  `mode=overview` (top-N most-connected pages, default) and
// @Description  `mode=ego` (BFS neighborhood of a center slug) to keep response
// @Description  size tractable for knowledge bases with tens of thousands of pages.
// @Tags         Wiki
// @Produce      json
// @Param        kb_id   path  string  true   "Knowledge base ID"
// @Param        mode    query string  false  "overview (default) | ego"
// @Param        center  query string  false  "Center slug for ego mode"
// @Param        depth   query int     false  "Ego BFS depth (1-3, default 1)"
// @Param        types   query string  false  "Comma-separated page_type allow-list"
// @Param        limit   query int     false  "Max nodes to return (default 500, max 2000)"
// @Success      200  {object}  types.WikiGraphData
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/graph [get]
func (h *WikiPageHandler) GetGraph(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	mode := strings.TrimSpace(c.Query("mode"))
	if mode == "" {
		mode = types.WikiGraphModeOverview
	}
	if mode != types.WikiGraphModeOverview && mode != types.WikiGraphModeEgo {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be 'overview' or 'ego'"})
		return
	}

	center := strings.TrimSpace(c.Query("center"))
	if mode == types.WikiGraphModeEgo && center == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "center is required when mode=ego"})
		return
	}

	depth := wikiGraphDefaultDepth
	if v := c.Query("depth"); v != "" {
		parsed, parseErr := strconv.Atoi(v)
		if parseErr != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "depth must be a positive integer"})
			return
		}
		if parsed > wikiGraphMaxDepth {
			parsed = wikiGraphMaxDepth
		}
		depth = parsed
	}

	limit := wikiGraphDefaultLimit
	if v := c.Query("limit"); v != "" {
		parsed, parseErr := strconv.Atoi(v)
		if parseErr != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		if parsed > wikiGraphMaxLimit {
			parsed = wikiGraphMaxLimit
		}
		limit = parsed
	}

	var typesFilter []string
	if v := strings.TrimSpace(c.Query("types")); v != "" {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				typesFilter = append(typesFilter, t)
			}
		}
	}

	req := &types.WikiGraphRequest{
		KnowledgeBaseID: kbID,
		Mode:            mode,
		Center:          center,
		Depth:           depth,
		Types:           typesFilter,
		Limit:           limit,
	}

	graph, err := h.wikiService.GetGraph(c.Request.Context(), req)
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
// @Router       /knowledgebase/{kb_id}/wiki/stats [get]
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
// @Router       /knowledgebase/{kb_id}/wiki/issues [get]
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
// @Router       /knowledgebase/{kb_id}/wiki/issues/{issue_id}/status [put]
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
// @Router       /knowledgebase/{kb_id}/wiki/search [get]
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
// @Router       /knowledgebase/{kb_id}/wiki/rebuild-links [post]
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
// @Router       /knowledgebase/{kb_id}/wiki/lint [get]
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
// @Router       /knowledgebase/{kb_id}/wiki/auto-fix [post]
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
