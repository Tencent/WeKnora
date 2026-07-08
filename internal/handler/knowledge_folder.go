package handler

import (
	"context"
	goerrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ListKnowledgeFolders godoc
// @Summary      List document folders
// @Description  List direct child folders under parent_id. Use parent_id=__root__ or empty for the root level.
// @Tags         知识管理
// @Produce      json
// @Param        id         path   string  true   "知识库ID"
// @Param        parent_id  query  string  false  "父目录ID，__root__ 表示根目录"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [get]
func (h *KnowledgeHandler) ListKnowledgeFolders(c *gin.Context) {
	ctx := c.Request.Context()
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = withEffectiveTenantID(ctx, effectiveTenantID)

	folders, err := h.folderService.ListFolders(ctx, kbID, c.Query("parent_id"))
	if err != nil {
		h.handleKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"folders": folders}})
}

// CreateKnowledgeFolder godoc
// @Summary      Create document folder
// @Description  Create an empty document-management folder under parent_id.
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path  string                              true  "知识库ID"
// @Param        request  body  types.KnowledgeFolderCreateRequest  true  "目录信息"
// @Success      201  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [post]
func (h *KnowledgeHandler) CreateKnowledgeFolder(c *gin.Context) {
	ctx := c.Request.Context()
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = withEffectiveTenantID(ctx, effectiveTenantID)

	var req types.KnowledgeFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid folder request").WithDetails(err.Error()))
		return
	}
	folder, err := h.folderService.CreateFolder(ctx, kbID, req.ParentID, req.Name)
	if err != nil {
		h.handleKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": folder})
}

// UpdateKnowledgeFolder godoc
// @Summary      Rename document folder
// @Description  Rename a document-management folder. Moving folders is intentionally out of scope for the first PR.
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id         path  string                              true  "知识库ID"
// @Param        folder_id  path  string                              true  "目录ID"
// @Param        request    body  types.KnowledgeFolderUpdateRequest  true  "目录信息"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeHandler) UpdateKnowledgeFolder(c *gin.Context) {
	ctx := c.Request.Context()
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = withEffectiveTenantID(ctx, effectiveTenantID)

	var req types.KnowledgeFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid folder request").WithDetails(err.Error()))
		return
	}
	folderID := c.Param("folder_id")
	folder, err := h.folderService.RenameFolder(ctx, kbID, folderID, req.Name)
	if err != nil {
		h.handleKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// DeleteKnowledgeFolder godoc
// @Summary      Delete empty document folder
// @Description  Delete a folder only when it has no child folders and no direct documents.
// @Tags         知识管理
// @Produce      json
// @Param        id         path  string  true  "知识库ID"
// @Param        folder_id  path  string  true  "目录ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeHandler) DeleteKnowledgeFolder(c *gin.Context) {
	ctx := c.Request.Context()
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = withEffectiveTenantID(ctx, effectiveTenantID)

	folderID := c.Param("folder_id")
	if err := h.folderService.DeleteEmptyFolder(ctx, kbID, folderID); err != nil {
		h.handleKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MoveKnowledgeToFolder godoc
// @Summary      Move knowledge to folder
// @Description  Move a single knowledge item to a folder. Empty folder_id or __root__ moves it to root.
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path  string                            true  "知识ID"
// @Param        request  body  types.KnowledgeFolderMoveRequest  true  "目标目录"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/folder [put]
func (h *KnowledgeHandler) MoveKnowledgeToFolder(c *gin.Context) {
	knowledgeID := c.Param("id")
	if strings.TrimSpace(knowledgeID) == "" {
		c.Error(apperrors.NewValidationError("knowledge id is required"))
		return
	}

	var req types.KnowledgeFolderMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid folder move request").WithDetails(err.Error()))
		return
	}
	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, knowledgeID, types.OrgRoleEditor)
	if err != nil {
		c.Error(err)
		return
	}
	knowledge, err := h.folderService.MoveKnowledgeToFolder(effCtx, knowledgeID, req.FolderID)
	if err != nil {
		h.handleKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": knowledge})
}

func (h *KnowledgeHandler) handleKnowledgeFolderError(c *gin.Context, err error) {
	switch {
	case goerrors.Is(err, service.ErrKnowledgeFolderNameRequired):
		c.Error(apperrors.NewValidationError("folder name is required"))
	case goerrors.Is(err, service.ErrKnowledgeFolderInvalidName):
		c.Error(apperrors.NewValidationError("folder name cannot contain path separators"))
	case goerrors.Is(err, service.ErrKnowledgeFolderTooDeep):
		c.Error(apperrors.NewValidationError("folder depth exceeds limit"))
	case goerrors.Is(err, service.ErrKnowledgeFolderExists):
		c.Error(apperrors.NewConflictError("folder already exists"))
	case goerrors.Is(err, service.ErrKnowledgeFolderNotEmpty):
		c.Error(apperrors.NewConflictError("folder is not empty"))
	case goerrors.Is(err, service.ErrKnowledgeFolderNotFound):
		c.Error(apperrors.NewNotFoundError("folder not found"))
	case goerrors.Is(err, repository.ErrKnowledgeNotFound):
		c.Error(apperrors.NewNotFoundError("knowledge not found"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(apperrors.NewInternalServerError("knowledge folder operation failed").WithDetails(err.Error()))
	}
}

func normalizeKnowledgeFolderQuery(raw string) *string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	folderID := strings.TrimSpace(raw)
	if folderID == types.KnowledgeFolderRootID {
		folderID = ""
	}
	return &folderID
}

func withEffectiveTenantID(ctx context.Context, tenantID uint64) context.Context {
	return context.WithValue(ctx, types.TenantIDContextKey, tenantID)
}
