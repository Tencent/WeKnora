package handler

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

type documentFolderUpdatePayload struct {
	types.DocumentFolderUpdateRequest
	ParentID   json.RawMessage `json:"parent_id"`
	MoveParent json.RawMessage `json:"move_parent"`
}

// DocumentFolderHandler exposes the document-folder tree CRUD over HTTP. It depends on the
// DocumentFolderService (application rules) and KnowledgeBaseService (KB
// existence / type check) — the same shape as WikiPageHandler.
type DocumentFolderHandler struct {
	cfg           *config.Config
	folderService interfaces.DocumentFolderService
	kbService     interfaces.KnowledgeBaseService
}

// NewDocumentFolderHandler constructs the handler.
func NewDocumentFolderHandler(
	cfg *config.Config,
	folderService interfaces.DocumentFolderService,
	kbService interfaces.KnowledgeBaseService,
) *DocumentFolderHandler {
	return &DocumentFolderHandler{
		cfg:           cfg,
		folderService: folderService,
		kbService:     kbService,
	}
}

// validateDocumentKB resolves and validates the kb_id path param for folder
// operations. Folders are only supported on document-type KBs — FAQ KBs store
// Q&A pairs (no files) and Wiki KBs already have their own wiki_folders.
func (h *DocumentFolderHandler) validateDocumentKB(c *gin.Context) (string, uint64, error) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("kb_id"))
	tenantID, ok := types.TenantIDFromContext(ctx)

	if !config.DocumentFoldersEnabled(h.cfg) {
		return "", 0, errors.NewServiceUnavailableError(
			"document folders are disabled until the rolling upgrade is complete",
		)
	}
	if kbID == "" {
		return "", 0, errors.NewBadRequestError("Knowledge base ID is required")
	}
	if !ok || tenantID == 0 {
		return "", 0, errors.NewUnauthorizedError("Tenant context is required")
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return "", 0, errors.NewNotFoundError("Knowledge base not found")
	}

	if kb.Type != types.KnowledgeBaseTypeDocument || kb.IsWikiEnabled() {
		return "", 0, errors.NewBadRequestError(
			"Document folders are not supported for this knowledge base",
		)
	}

	return kbID, tenantID, nil
}

// ListFolders godoc
// @Summary      List document folders
// @Description  List one page of direct child folders. An empty parent_id selects the knowledge-base root.
// @Tags         知识库
// @Produce      json
// @Param        kb_id     path   string  true   "Knowledge base ID"
// @Param        parent_id query  string  false  "Parent folder ID (empty = root)"
// @Param        keyword   query  string  false  "Folder name or path keyword"
// @Param        cursor    query  string  false  "Opaque continuation cursor"
// @Param        page_size query  int     false  "Page size (1-200)" default(50)
// @Success      200  {object}  types.DocumentFolderListResponse
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/document-folders [get]
func (h *DocumentFolderHandler) ListFolders(c *gin.Context) {
	kbID, tenantID, err := h.validateDocumentKB(c)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	parentID := c.Query("parent_id") // "" when omitted → root listing
	pageSize := types.DefaultDocumentFolderPageSize
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > types.MaxDocumentFolderPageSize {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "page_size must be between 1 and " + strconv.Itoa(types.MaxDocumentFolderPageSize),
			})
			return
		}
	}
	resp, err := h.folderService.ListFolders(
		c.Request.Context(),
		kbID,
		tenantID,
		parentID,
		c.Query("keyword"),
		c.Query("cursor"),
		pageSize,
	)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetDeleteImpact godoc
// @Summary      Preview document-folder deletion
// @Description  Return the live folder, document, and active-processing counts for the selected folder subtree.
// @Tags         知识库
// @Produce      json
// @Param        kb_id     path  string  true  "Knowledge base ID"
// @Param        folder_id path  string  true  "Folder ID"
// @Success      200  {object}  types.DocumentFolderDeleteImpact
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/document-folders/{folder_id}/delete-impact [get]
func (h *DocumentFolderHandler) GetDeleteImpact(c *gin.Context) {
	kbID, tenantID, err := h.validateDocumentKB(c)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	folderID := strings.TrimSpace(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder_id is required"})
		return
	}
	impact, err := h.folderService.GetDeleteImpact(c.Request.Context(), kbID, tenantID, folderID)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, impact)
}

// CreateFolder godoc
// @Summary      Create a document folder
// @Description  Create a new empty folder under parent_id. An empty parent_id selects the knowledge-base root.
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        kb_id  path  string                             true  "Knowledge base ID"
// @Param        folder body  types.DocumentFolderCreateRequest true  "Folder data"
// @Success      201  {object}  types.DocumentFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/document-folders [post]
func (h *DocumentFolderHandler) CreateFolder(c *gin.Context) {
	kbID, tenantID, err := h.validateDocumentKB(c)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	var req types.DocumentFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	folder, err := h.folderService.CreateFolder(c.Request.Context(), kbID, tenantID, req.ParentID, req.Name)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, folder)
}

// UpdateFolder godoc
// @Summary      Rename a document folder
// @Description  Rename a folder and its materialized descendant paths. Folder reparenting is not supported.
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        kb_id     path  string                            true  "Knowledge base ID"
// @Param        folder_id path  string                            true  "Folder ID"
// @Param        folder    body  types.DocumentFolderUpdateRequest true  "Folder update"
// @Success      200  {object}  types.DocumentFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/document-folders/{folder_id} [put]
func (h *DocumentFolderHandler) UpdateFolder(c *gin.Context) {
	kbID, _, err := h.validateDocumentKB(c)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	folderID := c.Param("folder_id")
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder_id is required"})
		return
	}
	var req documentFolderUpdatePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.ParentID != nil || req.MoveParent != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "moving folders is not supported"})
		return
	}
	if req.Name == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	folder, err := h.folderService.RenameFolder(
		c.Request.Context(), kbID, folderID, *req.Name,
	)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary      Delete a document folder subtree
// @Description  Without mode, delete one empty folder synchronously. keep_documents recursively removes the folder structure and moves its documents to the knowledge-base root. delete_all permanently removes the subtree and all document-derived data. Explicit modes run asynchronously.
// @Tags         知识库
// @Produce      json
// @Param        kb_id     path   string  true   "Knowledge base ID"
// @Param        folder_id path   string  true   "Folder ID"
// @Param        mode      query  string  false  "Deletion mode; omit for legacy empty-folder deletion" Enums(keep_documents,delete_all)
// @Success      202  {object}  map[string]string
// @Success      204
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/document-folders/{folder_id} [delete]
func (h *DocumentFolderHandler) DeleteFolder(c *gin.Context) {
	kbID, tenantID, err := h.validateDocumentKB(c)
	if err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	folderID := c.Param("folder_id")
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder_id is required"})
		return
	}
	modeValue := strings.TrimSpace(c.Query("mode"))
	if modeValue != "" {
		mode := types.DocumentFolderDeleteMode(modeValue)
		if !mode.IsValid() {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "mode must be keep_documents or delete_all",
			})
			return
		}
		taskID, err := h.folderService.SubmitDeleteFolderTree(
			c.Request.Context(), kbID, tenantID, folderID, mode,
		)
		if err != nil {
			writeDocumentFolderError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"task_id": taskID})
		return
	}
	if err := h.folderService.DeleteFolder(c.Request.Context(), kbID, folderID); err != nil {
		writeDocumentFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeDocumentFolderError maps service-layer sentinel errors to HTTP status
// codes. Mirrors writeWikiFolderError.
func writeDocumentFolderError(c *gin.Context, err error) {
	if appErr, ok := errors.IsAppError(err); ok {
		c.JSON(appErr.HTTPCode, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	switch {
	case stderrors.Is(err, repository.ErrDocumentFolderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderNotEmpty):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderDepthExceeded):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderNameInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderLimitExceeded):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderCycleInData):
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderCursorInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderDocumentsProcessing):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case stderrors.Is(err, service.ErrFolderChangedDuringDelete):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
