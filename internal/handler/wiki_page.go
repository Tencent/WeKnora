package handler

import (
	"context"
	"encoding/json"
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
	"github.com/hibiken/asynq"
)

// WikiPageHandler handles HTTP requests for wiki page operations
type WikiPageHandler struct {
	wikiService    interfaces.WikiPageService
	kbService      interfaces.KnowledgeBaseService
	lintService    *service.WikiLintService
	auditService   interfaces.AuditLogService
	taskEnqueuer   interfaces.TaskEnqueuer
	sessionService interfaces.SessionService
}

// NewWikiPageHandler creates a new wiki page handler
func NewWikiPageHandler(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	lintService *service.WikiLintService,
	auditService interfaces.AuditLogService,
	taskEnqueuer interfaces.TaskEnqueuer,
	sessionService interfaces.SessionService,
) *WikiPageHandler {
	return &WikiPageHandler{
		wikiService:    wikiService,
		kbService:      kbService,
		lintService:    lintService,
		auditService:   auditService,
		taskEnqueuer:   taskEnqueuer,
		sessionService: sessionService,
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
// @Tags         Wiki
// @Produce      json
// @Param        kb_id    path   string  true   "Knowledge base ID"
// @Param        slug     path   string  true   "Page slug"
// @Param        version  query  int     false  "Return this single revision with content"
// @Param        limit    query  int     false  "Page size (default 50, max 200)"
// @Param        offset   query  int     false  "Offset into the newest-first list"
// @Success      200  {object}  types.WikiPageRevisionListResponse
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
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
// @Param        kb_id   path  string                       true  "Knowledge base ID"
// @Param        revert  body  types.WikiPageRevertRequest  true  "Revert target"
// @Success      200  {object}  types.WikiPage
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
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
// @Param        status    query  string  false  "Filter by status (open, actionable, unresolved, ignored, resolved)"
// @Param        page      query  int     false  "Page number (omit with page_size for legacy bare-array response)"
// @Param        page_size query  int     false  "Page size (max 100)"
// @Success      200  {object}  types.WikiIssueListResponse
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
	// Callers that omit both pagination keys get the pre-pagination bare array,
	// so scripts and older clients keep working. The problem-centre UI always
	// sends page/page_size and receives the envelope.
	if !c.Request.URL.Query().Has("page") && !c.Request.URL.Query().Has("page_size") {
		issues, err := h.wikiService.ListIssues(c.Request.Context(), kbID, slug, status)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, issues)
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	issues, err := h.wikiService.ListIssuesPage(c.Request.Context(), kbID, slug, status, page, pageSize)
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
		Status  string `json:"status" binding:"required"`
		Summary string `json:"summary"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	// Only the transitions a client may request. repairing and verifying are
	// absent because they are owned by the repair lifecycle, not by callers.
	validStatuses := map[string]bool{
		types.WikiIssueStatusOpen:          true,
		types.WikiIssueStatusLegacyPending: true,
		types.WikiIssueStatusIgnored:       true,
		types.WikiIssueStatusResolved:      true,
	}
	if !validStatuses[req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status. Must be open, pending, ignored, or resolved"})
		return
	}

	if err := h.wikiService.UpdateIssueStatus(c.Request.Context(), kbID, issueID, req.Status, req.Summary); err != nil {
		if stderrors.Is(err, repository.ErrWikiIssueNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if stderrors.Is(err, repository.ErrWikiIssueConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Issue status updated successfully"})
}

// StartLintRun queues a durable full-KB lint scan.
func (h *WikiPageHandler) StartLintRun(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	run, err := h.lintService.StartRun(c.Request.Context(), tenantID, kbID)
	if err != nil {
		if stderrors.Is(err, repository.ErrWikiIssueConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "a wiki lint run is already active"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	payload, _ := json.Marshal(service.WikiLintTaskPayload{TenantID: tenantID, KnowledgeBaseID: kbID, RunID: run.ID})
	task := asynq.NewTask(types.TypeWikiLint, payload)
	// A lint run is itself a durable retry unit. Mark the run failed once and
	// let the user explicitly retry, instead of replaying the same run while a
	// newer run may already have started.
	if _, err := h.taskEnqueuer.Enqueue(task, asynq.Queue(types.QueueWiki), asynq.MaxRetry(0)); err != nil {
		_ = h.lintService.FailRun(c.Request.Context(), kbID, run.ID, "failed to enqueue lint task: "+err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// GetLintRun returns one lint run or the latest run for a KB.
func (h *WikiPageHandler) GetLintRun(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	var run *types.WikiLintRun
	if runID == "latest" {
		run, err = h.lintService.GetLatestRun(c.Request.Context(), kbID)
	} else {
		run, err = h.lintService.GetRun(c.Request.Context(), kbID, runID)
	}
	if err != nil {
		writeWikiIssueError(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

// writeWikiIssueError maps the wiki issue/repair sentinels onto status codes.
// Without it every failure on these routes collapsed into one code — a lost
// race and a broken database looked identical to the client, and to us.
func writeWikiIssueError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, repository.ErrWikiIssueNotFound),
		stderrors.Is(err, repository.ErrWikiPageNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case stderrors.Is(err, repository.ErrWikiIssueConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// StartIssueRepair atomically claims an issue and starts its repair path.
func (h *WikiPageHandler) StartIssueRepair(c *gin.Context) {
	kbID, tenantID, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	issueID := strings.TrimSpace(c.Param("issue_id"))
	issue, err := h.wikiService.GetIssue(c.Request.Context(), kbID, issueID)
	if err != nil {
		writeWikiIssueError(c, err)
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	_ = c.ShouldBindJSON(&req)
	mode := req.Mode
	if mode == "" || mode == "auto" {
		mode = issue.RepairMode
	}
	if mode == "" {
		mode = types.WikiIssueRepairAgent
	}
	// A deterministic rule only helps when it has a confident rewrite to make.
	// A broken link whose target page is simply gone has none, and running the
	// deterministic path anyway would hand the user a failed attempt with no
	// agent session to continue in — and a retry that fails the same way every
	// time. Escalate to the agent before the issue is claimed, so the caller
	// gets a working repair session on the first click.
	if mode == types.WikiIssueRepairDeterministic &&
		!h.lintService.DeterministicRepairAvailable(c.Request.Context(), issue) {
		logger.Infof(c.Request.Context(),
			"wiki repair: issue %s has no applicable deterministic fix, escalating to agent", issue.ID)
		mode = types.WikiIssueRepairAgent
	}

	sessionID := ""
	if mode != types.WikiIssueRepairDeterministic {
		session := &types.Session{
			TenantID: tenantID, Title: "Wiki Repair: " + issue.Slug,
			Description: "Wiki repair session for issue " + issue.ID,
			UserID:      types.SessionOwnerIDFromContext(c.Request.Context()),
		}
		created, createErr := h.sessionService.CreateSession(c.Request.Context(), session)
		if createErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": createErr.Error()})
			return
		}
		sessionID = created.ID
	}
	attempt, claimedIssue, err := h.wikiService.BeginIssueRepair(c.Request.Context(), kbID, issueID, sessionID, mode)
	if err != nil {
		if sessionID != "" {
			_ = h.sessionService.DeleteSession(c.Request.Context(), sessionID)
		}
		writeWikiIssueError(c, err)
		return
	}
	if mode == types.WikiIssueRepairDeterministic {
		h.runDeterministicRepair(c.Request.Context(), kbID, claimedIssue, attempt)
	}
	// Re-read so the client sees the attempt's settled state rather than the
	// pre-repair snapshot. A read failure here is not worth failing the request
	// over — the client polls this same record.
	if refreshed, refreshErr := h.wikiService.GetRepairAttempt(
		c.Request.Context(), kbID, attempt.ID,
	); refreshErr == nil {
		attempt = refreshed
	}
	c.JSON(http.StatusAccepted, gin.H{
		"attempt":        attempt,
		"session_id":     sessionID,
		"initial_prompt": wikiRepairInitialPrompt(c, issue),
	})
}

// runDeterministicRepair executes a typed repair and records the outcome.
//
// RepairPersistedIssue closes the attempt itself when verification passes, and
// already fails it when verification rejects the result. So we only mark a
// failure that the service could not have recorded — otherwise the second write
// would race an attempt that is no longer active and lose to its own guard.
func (h *WikiPageHandler) runDeterministicRepair(
	ctx context.Context, kbID string,
	issue *types.WikiPageIssue, attempt *types.WikiRepairAttempt,
) {
	err := h.lintService.RepairPersistedIssue(ctx, issue, attempt)
	if err == nil {
		return
	}
	logger.Warnf(ctx, "wiki deterministic repair failed: issue=%s attempt=%s err=%v",
		issue.ID, attempt.ID, err)
	current, getErr := h.wikiService.GetRepairAttempt(ctx, kbID, attempt.ID)
	if getErr == nil && current.Status == types.WikiIssueStatusFailed {
		return
	}
	_ = h.wikiService.FailIssueRepair(ctx, kbID, issue.ID, attempt.ID, err.Error())
}

// wikiRepairInitialPrompt is the first turn handed to the Wiki Fixer session.
// It follows the caller's locale (resolved by middleware.Language) so a
// non-Chinese operator is not handed a Chinese instruction, with English as the
// fallback for every other locale.
func wikiRepairInitialPrompt(c *gin.Context, issue *types.WikiPageIssue) string {
	lang, _ := types.LanguageFromContext(c.Request.Context())
	if strings.HasPrefix(strings.ToLower(lang), "zh") {
		return fmt.Sprintf(
			"请修复 Wiki 页面 [[%s]] 的问题，问题 ID：%s。请先读取问题和页面，核对来源后执行修复。",
			issue.Slug, issue.ID,
		)
	}
	return fmt.Sprintf(
		"Repair the issue on Wiki page [[%s]] (issue ID: %s). "+
			"Read the issue and the page first, verify against the sources, then apply the fix.",
		issue.Slug, issue.ID,
	)
}

// GetRepairAttempt returns a KB-scoped repair attempt.
func (h *WikiPageHandler) GetRepairAttempt(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	attempt, err := h.wikiService.GetRepairAttempt(c.Request.Context(), kbID, strings.TrimSpace(c.Param("attempt_id")))
	if err != nil {
		writeWikiIssueError(c, err)
		return
	}
	c.JSON(http.StatusOK, attempt)
}

// ListActiveRepairAttempts returns reconnectable repair attempts for a KB.
func (h *WikiPageHandler) ListActiveRepairAttempts(c *gin.Context) {
	kbID, _, err := h.validateWikiKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	attempts, err := h.wikiService.ListActiveRepairAttempts(c.Request.Context(), kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, attempts)
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
