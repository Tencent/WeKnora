package handler

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// KnowledgeFolderHandler handles knowledge-folder CRUD and navigation requests.
// Tenant and knowledge-base ownership checks are enforced by the route guards
// and revalidated by the service using the request context and KB ID.
type KnowledgeFolderHandler struct {
	service interfaces.KnowledgeFolderService
}

// NewKnowledgeFolderHandler creates a knowledge-folder HTTP handler.
func NewKnowledgeFolderHandler(service interfaces.KnowledgeFolderService) (*KnowledgeFolderHandler, error) {
	if service == nil {
		return nil, fmt.Errorf("knowledge folder service is required")
	}
	return &KnowledgeFolderHandler{service: service}, nil
}

func knowledgeFolderParams(c *gin.Context) (string, string, bool) {
	kbID := strings.TrimSpace(c.Param("id"))
	folderID := strings.TrimSpace(c.Param("folder_id"))
	if kbID == "" {
		_ = c.Error(apperrors.NewBadRequestError("knowledge base ID is required"))
		return "", "", false
	}
	return kbID, folderID, true
}

func writeKnowledgeFolderError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, apprepo.ErrKnowledgeFolderNotFound),
		stderrors.Is(err, apprepo.ErrKnowledgeBaseNotFound):
		_ = c.Error(apperrors.NewNotFoundError("knowledge folder not found"))
	case stderrors.Is(err, types.ErrInvalidArgument):
		_ = c.Error(apperrors.NewBadRequestError("invalid knowledge folder request"))
	case stderrors.Is(err, types.ErrFolderAlreadyExists):
		_ = c.Error(apperrors.NewConflictError("a folder with this name already exists"))
	case stderrors.Is(err, types.ErrFolderNotEmpty):
		_ = c.Error(apperrors.NewConflictError("knowledge folder is not empty"))
	default:
		_ = c.Error(apperrors.NewInternalServerError("knowledge folder operation failed"))
	}
}

// CreateFolder godoc
// @Summary      Create a knowledge folder
// @Description  Create a folder in a knowledge base. The parent folder must belong to the same tenant and knowledge base.
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id      path  string                    true  "Knowledge base ID"
// @Param        request body  types.CreateFolderRequest true  "Folder data"
// @Success      201  {object}  types.KnowledgeFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [post]
func (h *KnowledgeFolderHandler) CreateFolder(c *gin.Context) {
	kbID, _, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	var req types.CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	folder, err := h.service.CreateFolder(c.Request.Context(), kbID, &req)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, folder)
}

// ListFolders godoc
// @Summary      List knowledge folders
// @Description  List direct child folders under parent_id; an empty parent_id lists root folders.
// @Tags         Knowledge Folders
// @Produce      json
// @Param        id        path   string  true   "Knowledge base ID"
// @Param        parent_id query  string  false  "Parent folder ID (empty means root)"
// @Success      200  {array}   types.KnowledgeFolder
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [get]
func (h *KnowledgeFolderHandler) ListFolders(c *gin.Context) {
	kbID, _, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	folders, err := h.service.ListByParent(c.Request.Context(), kbID, strings.TrimSpace(c.Query("parent_id")))
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	if folders == nil {
		folders = []*types.KnowledgeFolder{}
	}
	c.JSON(http.StatusOK, folders)
}

// GetTree godoc
// @Summary      Get the knowledge-folder tree
// @Description  Return the complete folder tree with recursive knowledge counts.
// @Tags         Knowledge Folders
// @Produce      json
// @Param        id  path  string  true  "Knowledge base ID"
// @Success      200  {array}   types.KnowledgeFolder
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/tree [get]
func (h *KnowledgeFolderHandler) GetTree(c *gin.Context) {
	kbID, _, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	folders, err := h.service.GetTree(c.Request.Context(), kbID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	if folders == nil {
		folders = []*types.KnowledgeFolder{}
	}
	c.JSON(http.StatusOK, folders)
}

// GetFolder godoc
// @Summary      Get a knowledge folder
// @Description  Get a folder by ID within the specified knowledge base.
// @Tags         Knowledge Folders
// @Produce      json
// @Param        id        path  string  true  "Knowledge base ID"
// @Param        folder_id path  string  true  "Folder ID"
// @Success      200  {object}  types.KnowledgeFolder
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [get]
func (h *KnowledgeFolderHandler) GetFolder(c *gin.Context) {
	kbID, folderID, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	if folderID == "" {
		_ = c.Error(apperrors.NewBadRequestError("folder ID is required"))
		return
	}
	folder, err := h.service.GetFolder(c.Request.Context(), kbID, folderID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// UpdateFolder godoc
// @Summary      Rename a knowledge folder
// @Description  Rename a folder within the specified knowledge base.
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id        path  string                    true  "Knowledge base ID"
// @Param        folder_id path  string                    true  "Folder ID"
// @Param        request   body  types.UpdateFolderRequest true  "Folder update"
// @Success      200  {object}  types.KnowledgeFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeFolderHandler) UpdateFolder(c *gin.Context) {
	kbID, folderID, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	if folderID == "" {
		_ = c.Error(apperrors.NewBadRequestError("folder ID is required"))
		return
	}
	var req types.UpdateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	folder, err := h.service.UpdateFolder(c.Request.Context(), kbID, folderID, &req)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary      Delete a knowledge folder
// @Description  Delete a folder. A non-empty folder requires force=true.
// @Tags         Knowledge Folders
// @Param        id        path   string  true   "Knowledge base ID"
// @Param        folder_id path   string  true   "Folder ID"
// @Param        force     query  bool    false  "Move contained knowledge to root and delete the subtree"
// @Success      204
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeFolderHandler) DeleteFolder(c *gin.Context) {
	kbID, folderID, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	if folderID == "" {
		_ = c.Error(apperrors.NewBadRequestError("folder ID is required"))
		return
	}
	force := false
	if raw, exists := c.GetQuery("force"); exists {
		var err error
		force, err = strconv.ParseBool(raw)
		if err != nil {
			_ = c.Error(apperrors.NewBadRequestError("force must be a boolean"))
			return
		}
	}
	if err := h.service.DeleteFolder(c.Request.Context(), kbID, folderID, force); err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetBreadcrumb godoc
// @Summary      Get a knowledge-folder breadcrumb
// @Description  Return the root-to-folder path within the specified knowledge base.
// @Tags         Knowledge Folders
// @Produce      json
// @Param        id        path  string  true  "Knowledge base ID"
// @Param        folder_id path  string  true  "Folder ID"
// @Success      200  {array}   types.KnowledgeFolder
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id}/breadcrumb [get]
func (h *KnowledgeFolderHandler) GetBreadcrumb(c *gin.Context) {
	kbID, folderID, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	if folderID == "" {
		_ = c.Error(apperrors.NewBadRequestError("folder ID is required"))
		return
	}
	folders, err := h.service.GetBreadcrumb(c.Request.Context(), kbID, folderID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	if folders == nil {
		folders = []*types.KnowledgeFolder{}
	}
	c.JSON(http.StatusOK, folders)
}
