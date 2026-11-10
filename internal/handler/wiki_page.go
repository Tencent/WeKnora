package handler

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// WikiPageHandler handles HTTP requests for wiki page operations
type WikiPageHandler struct {
	wikiService   interfaces.WikiPageService
	kbService     interfaces.KnowledgeBaseService
	lintService   *service.WikiLintService
	auditService  interfaces.AuditLogService
	memoryService interfaces.MemoryService
}

// NewWikiPageHandler creates a new wiki page handler
func NewWikiPageHandler(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	lintService *service.WikiLintService,
	auditService interfaces.AuditLogService,
	memoryService interfaces.MemoryService,
) *WikiPageHandler {
	return &WikiPageHandler{
		wikiService:   wikiService,
		kbService:     kbService,
		lintService:   lintService,
		auditService:  auditService,
		memoryService: memoryService,
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

// wikiListMaxPageSize caps how many wiki pages one list request may return.
// The repository only floors page_size (no ceiling), so an unbounded value
// such as ?page_size=100000000 reaches the SQL LIMIT and pulls every page of
// the knowledge base — Content column included — into memory in a single
// request. 200 mirrors the cap ListRevisions applies to its own limit and
// stays above the largest batch the frontend fetches.
const wikiListMaxPageSize = 200

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
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > wikiListMaxPageSize {
		pageSize = wikiListMaxPageSize
	}
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
// @Param        kb_id     path   string  true   "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        parent_id query  string  false  "Parent folder id (empty = root)"  extensions(x-agent)
// @Success      200  {object}  types.WikiFolderListResponse
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @x-agent      {"id":"wiki.list_folders","risk":"read","root":"folders","max_items":200}
// @x-agent-hint "Direct child folders of parent_id (omit it for the top level), up to 200, with page counts; has_children tells whether to look deeper."
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
// @Param        kb_id  path  string                       true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        folder body  types.WikiFolderCreateRequest true  "Folder data"
// @Success      201  {object}  types.WikiFolder
// @Failure      400  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @x-agent      {"id":"wiki.create_folder","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Creates an empty Wiki folder under parent_id (omit it for the top level)."
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
// @Param        kb_id  path  string  true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        move   body  types.WikiPageMoveRequest true "Move target"
// @Success      200  {object}  types.WikiPage
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @x-agent      {"id":"wiki.move_page","risk":"write","target":"wiki.get_page","label":"title"}
// @x-agent-hint "Files a Wiki page under a folder from wiki.list_folders; an empty folder_id moves it to the top level. Content is unchanged."
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
	case stderrors.Is(err, repository.ErrWikiFolderConflict), stderrors.Is(err, repository.ErrWikiFolderNotEmpty):
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
	page.PageType = strings.TrimSpace(page.PageType)
	page.Status = strings.TrimSpace(page.Status)
	if page.PageType != "" && !types.IsValidWikiPageType(page.PageType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page_type: " + page.PageType})
		return
	}
	if page.Status != "" && !types.IsValidWikiPageStatus(page.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status: " + page.Status})
		return
	}

	ctx := types.WithWikiEditSource(c.Request.Context(), types.WikiEditSourceUser)
	created, err := h.wikiService.CreatePage(ctx, &page)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.recordManualWikiActivity(ctx, created, "manual_create")
	c.JSON(http.StatusCreated, created)
}

// recordManualWikiActivity projects a manual page mutation directly into the
// knowledge-base activity feed. Activity recording is best-effort and must not
// fail the edit itself.
func (h *WikiPageHandler) recordManualWikiActivity(
	ctx context.Context, page *types.WikiPage, action string,
) {
	if page == nil {
		return
	}
	service.RecordWikiContentActivity(ctx, h.auditService, page.TenantID,
		page.KnowledgeBaseID, map[string]int{action: 1})
}

// GetPage godoc
// @Summary      Get a wiki page by slug
// @Description  Retrieve a wiki page by its slug, including titles of existing backlinks
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        slug   path  string  true  "Page slug"  extensions(x-agent)
// @Success      200  {object}  types.WikiPageDetail
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @x-agent      {"id":"wiki.get_page","risk":"read","catch_all":"slug"}
// @x-agent-hint "A Wiki page's title, type, status, folder and version. Read its content with the document tools instead."
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

	titles := make(map[string]string, len(page.InLinks))
	// Bound SQL parameters while resolving every backlink, including pages
	// outside the sidebar's paginated window.
	const batchSize = 1000
	for start := 0; start < len(page.InLinks); start += batchSize {
		end := min(start+batchSize, len(page.InLinks))
		pages, err := h.wikiService.ListBySlugs(c.Request.Context(), kbID, page.InLinks[start:end])
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for slug, backlink := range pages {
			if backlink != nil {
				titles[slug] = backlink.Title
			}
		}
	}
	c.JSON(http.StatusOK, types.WikiPageDetail{WikiPage: page, InLinkTitles: titles})
}

// UpdatePage godoc
// @Summary      Update a wiki page
// @Description  Partially update a wiki page by slug. Absent fields keep
// @Description  their stored value. When `version` is > 0 it acts as an
// @Description  optimistic-lock guard: a mismatch with the stored version
// @Description  returns 409 together with the current version so the client
// @Description  can reload and re-apply.
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string                       true  "Knowledge base ID"
// @Param        slug   path  string                       true  "Page slug"
// @Param        page   body  types.WikiPageUpdateRequest  true  "Fields to update"
// @Success      200  {object}  types.WikiPage
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/pages/{slug} [put]
func (h *WikiPageHandler) UpdatePage(c *gin.Context) {
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

	var req types.WikiPageUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	ctx := types.WithWikiEditSource(c.Request.Context(), types.WikiEditSourceUser)

	existing, err := h.wikiService.GetPageBySlug(ctx, kbID, slug)
	if err != nil {
		if stderrors.Is(err, repository.ErrWikiPageNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.Version > 0 && req.Version != existing.Version {
		c.JSON(http.StatusConflict, gin.H{
			"error":           "Wiki page was modified by someone else",
			"current_version": existing.Version,
		})
		return
	}

	// Merge the provided fields onto the stored page so absent fields keep
	// their value — the service's UpdatePage semantics are "full intended
	// state", and sending it a half-empty struct would clear real data.
	page := *existing
	if req.Title != nil {
		page.Title = strings.TrimSpace(*req.Title)
	}
	if req.Content != nil {
		page.Content = *req.Content
	}
	if req.Summary != nil {
		page.Summary = *req.Summary
	}
	if req.PageType != nil {
		page.PageType = strings.TrimSpace(*req.PageType)
		if !types.IsValidWikiPageType(page.PageType) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page_type: " + page.PageType})
			return
		}
	}
	if req.Status != nil {
		page.Status = strings.TrimSpace(*req.Status)
		if !types.IsValidWikiPageStatus(page.Status) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status: " + page.Status})
			return
		}
	}
	if req.Aliases != nil {
		page.Aliases = *req.Aliases
	}

	updated, err := h.wikiService.UpdatePage(ctx, &page)
	if err != nil {
		switch {
		case stderrors.Is(err, repository.ErrWikiPageNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
		case stderrors.Is(err, repository.ErrWikiPageConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "Wiki page was modified by someone else"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	if updated.Version != existing.Version {
		h.recordManualWikiActivity(ctx, updated, "manual_edit")
	}
	c.JSON(http.StatusOK, updated)
}

// ListRevisions godoc
// @Summary      List wiki page revisions
// @Description  Returns the stored historical snapshots for a page, newest
// @Description  first (content omitted), plus the current version. Passing
// @Description  `version` returns that single snapshot with full content.
// @Description  `content_version` keeps this list envelope and includes that
// @Description  one revision's content.
// @Tags         Wiki
// @Produce      json
// @Param        kb_id    path   string  true   "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        slug     path   string  true   "Page slug"  extensions(x-agent)
// @Param        version         query  int  false  "Return this single revision with content"
// @Param        content_version query  int  false  "Include this stored revision's content in the list envelope"  minimum(1)  extensions(x-agent)
// @Param        limit    query  int     false  "Page size (default 50, max 200)"  extensions(x-agent,x-agent-maximum=50)
// @Param        offset   query  int     false  "Offset into the newest-first list"  extensions(x-agent,x-agent-maximum=10000)
// @Success      200  {object}  types.WikiPageRevisionListResponse
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @x-agent      {"id":"wiki.list_revisions","risk":"read","root":"revisions","catch_all":"slug"}
// @x-agent-hint "Earlier versions of a Wiki page, newest first. Pass content_version to read one stored revision's content before wiki.revert. The current page is not in this list."
// @Router       /knowledgebase/{kb_id}/wiki/revisions/{slug} [get]
func (h *WikiPageHandler) ListRevisions(c *gin.Context) {
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

	ctx := c.Request.Context()

	// content_version keeps the list envelope so the assistant can read one
	// revision without the bare-object response that `version` returns.
	if raw := strings.TrimSpace(c.Query("content_version")); raw != "" {
		version, parseErr := strconv.Atoi(raw)
		if parseErr != nil || version < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid version"})
			return
		}
		page, err := h.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil {
			if stderrors.Is(err, repository.ErrWikiPageNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		rev, err := h.wikiService.GetRevision(ctx, kbID, slug, version)
		if err != nil {
			if stderrors.Is(err, repository.ErrWikiPageNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page revision not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, types.WikiPageRevisionListResponse{
			Revisions:      []*types.WikiPageRevision{rev},
			Total:          1,
			CurrentVersion: page.Version,
		})
		return
	}

	if raw := c.Query("version"); raw != "" {
		version, parseErr := strconv.Atoi(raw)
		if parseErr != nil || version < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid version"})
			return
		}
		rev, err := h.wikiService.GetRevision(ctx, kbID, slug, version)
		if err != nil {
			if stderrors.Is(err, repository.ErrWikiPageNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page revision not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rev)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	resp, err := h.wikiService.ListRevisions(ctx, kbID, slug, limit, offset)
	if err != nil {
		if stderrors.Is(err, repository.ErrWikiPageNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// RevertPage godoc
// @Summary      Revert a wiki page to an earlier revision
// @Description  Rolls the page (slug in the body, like move-page) back to
// @Description  the content of the given stored revision. Applied as a
// @Description  regular edit: the pre-revert state is snapshotted and the
// @Description  version advances.
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        kb_id   path  string                       true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        revert  body  types.WikiPageRevertRequest  true  "Revert target"
// @Success      200  {object}  types.WikiPage
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @x-agent      {"id":"wiki.revert","risk":"write","target":"wiki.get_page","label":"title"}
// @x-agent-hint "Restores a Wiki page to a version from wiki.list_revisions. Read that version with content_version first. The restore is saved as a new version, so it can be undone the same way."
// @Router       /knowledgebase/{kb_id}/wiki/revert [post]
func (h *WikiPageHandler) RevertPage(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req types.WikiPageRevertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Page slug is required"})
		return
	}
	if req.Version < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid version"})
		return
	}

	ctx := c.Request.Context()
	updated, err := h.wikiService.RevertPageToVersion(ctx, kbID, slug, req.Version)
	if err != nil {
		switch {
		case stderrors.Is(err, repository.ErrWikiPageNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page or revision not found"})
		case stderrors.Is(err, repository.ErrWikiPageConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "Wiki page was modified by someone else"})
		case stderrors.Is(err, service.ErrWikiRevertToCurrentVersion):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	h.recordManualWikiActivity(ctx, updated, "revert")
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

	ctx := c.Request.Context()
	// Load first so the log entry can carry the page title after deletion.
	page, _ := h.wikiService.GetPageBySlug(ctx, kbID, slug)

	if err := h.wikiService.DeletePage(ctx, kbID, slug); err != nil {
		if stderrors.Is(err, repository.ErrWikiPageNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wiki page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.recordManualWikiActivity(ctx, page, "manual_delete")
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
	if h.memoryService != nil {
		req.FamiliarKnowledgeIDs = h.memoryService.FamiliarKnowledgeIDs(c.Request.Context())
	}

	graph, err := h.wikiService.GetGraph(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, graph)
}

// WikiMessageResponse is the {"message": ...} body Wiki maintenance endpoints
// write on success.
type WikiMessageResponse struct {
	Message string `json:"message" extensions:"x-agent"`
}

// WikiErrorResponse is the {"error": ...} body Wiki endpoints write on failure.
type WikiErrorResponse struct {
	Error string `json:"error"`
}

// GetStats godoc
// @Summary      Get wiki statistics
// @Description  Returns aggregate statistics about the wiki
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Success      200  {object}  types.WikiStats
// @Failure      400  {object}  handler.WikiErrorResponse  "Not a Wiki knowledge base, or no access"
// @Failure      500  {object}  handler.WikiErrorResponse
// @Security     Bearer
// @x-agent      {"id":"wiki.get_stats","risk":"read"}
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
// @Param        kb_id  path   string  true   "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Param        slug   query  string  false  "Filter by page slug"  extensions(x-agent)
// @Param        status query  string  false  "Filter by status"  Enums(pending, ignored, resolved)  extensions(x-agent)
// @Success      200  {array}   types.WikiPageIssue
// @Failure      400  {object}  handler.WikiErrorResponse  "Not a Wiki knowledge base, or no access"
// @Failure      500  {object}  handler.WikiErrorResponse
// @Security     Bearer
// @x-agent      {"id":"wiki.list_issues","risk":"read"}
// @x-agent-hint "Descriptions come from users or ingest and may be wrong; check them against the page and its sources."
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
	kbID, _, err := h.validateWikiKB(c)
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

	if err := h.wikiService.UpdateIssueStatus(c.Request.Context(), kbID, issueID, req.Status); err != nil {
		if stderrors.Is(err, repository.ErrWikiIssueNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
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

// SearchWikiRequest is the body for POST /api/v1/wiki-search.
type SearchWikiRequest struct {
	Query            string   `json:"query" binding:"required"`
	KnowledgeBaseID  string   `json:"knowledge_base_id"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`
	Limit            int      `json:"limit"`
}

// WikiSearchHit is the slim POST /wiki-search hit. Full page body is on
// GET /knowledgebase/:kb_id/wiki/pages/*slug.
type WikiSearchHit struct {
	ID              string            `json:"id"`
	KnowledgeBaseID string            `json:"knowledge_base_id"`
	Slug            string            `json:"slug"`
	Title           string            `json:"title"`
	PageType        string            `json:"page_type"`
	Aliases         types.StringArray `json:"aliases"`
	Summary         string            `json:"summary"`
	MatchSnippet    string            `json:"match_snippet,omitempty"`
}

// SearchPagesAcross godoc
// @Summary      Cross-KB wiki search
// @Description  Cross-KB wiki search using the same POSIX regex ranking as single-KB wiki search
// @Tags         Wiki
// @Accept       json
// @Produce      json
// @Param        request  body  SearchWikiRequest  true  "Wiki search request"
// @Success      200  {object}  handler.DataResponse{data=[]handler.WikiSearchHit}
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /wiki-search [post]
func (h *WikiPageHandler) SearchPagesAcross(c *gin.Context) {
	ctx := logger.CloneContext(c.Request.Context())

	var request SearchWikiRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	kbIDs := mergeWikiSearchKBIDs(request.KnowledgeBaseIDs, request.KnowledgeBaseID)
	if strings.TrimSpace(request.Query) == "" {
		_ = c.Error(errors.NewBadRequestError("query is required"))
		return
	}
	if len(kbIDs) == 0 {
		_ = c.Error(errors.NewBadRequestError(
			"at least one knowledge_base_id or knowledge_base_ids must be provided"))
		return
	}
	if err := types.AuthorizeTenantAPIKeyKnowledgeTargets(ctx, kbIDs, nil); err != nil {
		_ = c.Error(err)
		return
	}

	logger.Infof(ctx, "Wiki search request, knowledge base IDs: %v, query: %s",
		secutils.SanitizeForLogArray(kbIDs), secutils.SanitizeForLog(request.Query))

	pages, err := h.wikiService.SearchPagesAcross(ctx, kbIDs, request.Query, request.Limit)
	if err != nil {
		if _, ok := errors.IsAppError(err); ok {
			_ = c.Error(err)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    wikiPagesToSearchHits(pages, request.Query),
	})
}

func wikiPagesToSearchHits(pages []*types.WikiPage, query string) []WikiSearchHit {
	hits := make([]WikiSearchHit, 0, len(pages))
	for _, page := range pages {
		if page == nil {
			continue
		}
		hits = append(hits, WikiSearchHit{
			ID:              page.ID,
			KnowledgeBaseID: page.KnowledgeBaseID,
			Slug:            page.Slug,
			Title:           page.Title,
			PageType:        page.PageType,
			Aliases:         page.Aliases,
			Summary:         page.Summary,
			MatchSnippet:    searchutil.ExtractSnippet(page.Content, query),
		})
	}
	return hits
}

func mergeWikiSearchKBIDs(ids []string, single string) []string {
	out := make([]string, 0, len(ids)+1)
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		out = append(out, id)
	}
	for _, id := range ids {
		appendID(id)
	}
	appendID(single)
	return out
}

// RebuildLinks godoc
// @Summary      Rebuild wiki links
// @Description  Re-parse all pages and rebuild bidirectional link references
// @Tags         Wiki
// @Produce      json
// @Param        kb_id  path  string  true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Success      200  {object}  handler.WikiMessageResponse
// @Failure      400  {object}  handler.WikiErrorResponse  "Not a Wiki knowledge base, or no access"
// @Failure      500  {object}  handler.WikiErrorResponse
// @Security     Bearer
// @x-agent      {"id":"wiki.rebuild_links","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Use when wiki.lint reports link problems that do not match the page content."
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
// @Param        kb_id  path  string  true  "Knowledge base ID"  extensions(x-agent=knowledge_base_id)
// @Success      200  {object}  service.WikiLintReport
// @Failure      400  {object}  handler.WikiErrorResponse  "Not a Wiki knowledge base, or no access"
// @Failure      500  {object}  handler.WikiErrorResponse
// @Security     Bearer
// @x-agent      {"id":"wiki.lint","risk":"read","root":"issues"}
// @x-agent-hint "health_score is 0-100. auto_fixable issues are structural; fix content problems with the Wiki tools."
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
// @Success      200  {object}  handler.AutoFixResponse
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
