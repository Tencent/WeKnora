package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// KnowledgeFolderHandler handles HTTP requests for knowledge folder operations.
type KnowledgeFolderHandler struct {
	folderService interfaces.KnowledgeFolderService
	kbService     interfaces.KnowledgeBaseService
}

// NewKnowledgeFolderHandler creates a new knowledge folder handler instance.
func NewKnowledgeFolderHandler(
	folderService interfaces.KnowledgeFolderService,
	kbService interfaces.KnowledgeBaseService,
) *KnowledgeFolderHandler {
	return &KnowledgeFolderHandler{
		folderService: folderService,
		kbService:     kbService,
	}
}

// CreateFolder godoc
// @Summary      Create a new folder
// @Description  Create a new folder in the specified knowledge base
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "Knowledge Base ID"
// @Param        body body      types.CreateFolderRequest  true  "Folder creation request"
// @Success      201  {object}  types.KnowledgeFolder
// @Failure      400  {object}  errors.AppError
// @Failure      403  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [post]
func (h *KnowledgeFolderHandler) CreateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Failed to get tenant ID")
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		logger.Warnf(ctx, "Permission denied or KB not found: %s", kbID)
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Parse request body
	var req types.CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warnf(ctx, "Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError("Invalid request body"))
		return
	}

	// Create folder
	folder, err := h.folderService.CreateFolder(ctx, kbID, &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id":            kbID,
			"folder_name":      req.Name,
			"parent_folder_id": req.ParentFolderID,
		})
		c.JSON(http.StatusInternalServerError, errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Folder created: %s (id=%s) in KB %s", folder.Name, folder.ID, kbID)
	c.JSON(http.StatusCreated, folder)
}

// GetFolder godoc
// @Summary      Get folder by ID
// @Description  Retrieve detailed information about a specific folder
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id         path      string  true  "Knowledge Base ID"
// @Param        folder_id  path      string  true  "Folder ID"
// @Success      200        {object}  types.KnowledgeFolder
// @Failure      403        {object}  errors.AppError
// @Failure      404        {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [get]
func (h *KnowledgeFolderHandler) GetFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Get folder
	folder, err := h.folderService.GetFolder(ctx, folderID)
	if err != nil {
		logger.Warnf(ctx, "Folder not found: %s", folderID)
		c.JSON(http.StatusNotFound, errors.NewNotFoundError("Folder not found"))
		return
	}

	// Verify folder belongs to this KB
	if folder.KnowledgeBaseID != kbID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Folder does not belong to this knowledge base"))
		return
	}

	c.JSON(http.StatusOK, folder)
}

// ListFolders godoc
// @Summary      List folders
// @Description  List all folders under a specific parent (or root if parent_id not provided)
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "Knowledge Base ID"
// @Param        parent_id  query     string  false  "Parent Folder ID (omit for root level)"
// @Success      200        {array}   types.KnowledgeFolder
// @Failure      403        {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [get]
func (h *KnowledgeFolderHandler) ListFolders(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Get parent_id from query
	parentIDStr := c.Query("parent_id")
	var parentID *string
	if parentIDStr != "" {
		parentID = &parentIDStr
	}

	// List folders
	folders, err := h.folderService.ListByParent(ctx, kbID, parentID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id":     kbID,
			"parent_id": parentID,
		})
		c.JSON(http.StatusInternalServerError, errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, folders)
}

// GetFolderTree godoc
// @Summary      Get folder tree
// @Description  Retrieve the complete folder hierarchy for a knowledge base
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "Knowledge Base ID"
// @Success      200  {array}   types.KnowledgeFolder
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/tree [get]
func (h *KnowledgeFolderHandler) GetFolderTree(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Get folder tree
	tree, err := h.folderService.GetTree(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbID})
		c.JSON(http.StatusInternalServerError, errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, tree)
}

// UpdateFolder godoc
// @Summary      Update folder
// @Description  Update folder properties (name, color, description, sort order)
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id         path      string  true  "Knowledge Base ID"
// @Param        folder_id  path      string  true  "Folder ID"
// @Param        body       body      types.UpdateFolderRequest  true  "Update request"
// @Success      200        {object}  types.KnowledgeFolder
// @Failure      400        {object}  errors.AppError
// @Failure      403        {object}  errors.AppError
// @Failure      404        {object}  errors.AppError
// @Failure      409        {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeFolderHandler) UpdateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Parse request body
	var req types.UpdateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError("Invalid request body"))
		return
	}

	// Update folder
	folder, err := h.folderService.UpdateFolder(ctx, folderID, &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"folder_id": folderID,
			"kb_id":     kbID,
		})
		c.JSON(http.StatusInternalServerError, errors.NewInternalServerError(err.Error()))
		return
	}

	// Verify folder belongs to this KB
	if folder.KnowledgeBaseID != kbID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Folder does not belong to this knowledge base"))
		return
	}

	logger.Infof(ctx, "Folder updated: %s (id=%s)", folder.Name, folder.ID)
	c.JSON(http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary      Delete folder
// @Description  Delete a folder (soft delete by default, use force=true for cascade delete)
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "Knowledge Base ID"
// @Param        folder_id  path      string  true   "Folder ID"
// @Param        force      query     bool    false  "Force cascade delete (delete children and files)"
// @Success      200        {object}  map[string]interface{}
// @Failure      400        {object}  errors.AppError
// @Failure      403        {object}  errors.AppError
// @Failure      404        {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeFolderHandler) DeleteFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	force := c.Query("force") == "true"

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Verify folder belongs to this KB
	folder, err := h.folderService.GetFolder(ctx, folderID)
	if err != nil {
		c.JSON(http.StatusNotFound, errors.NewNotFoundError("Folder not found"))
		return
	}
	if folder.KnowledgeBaseID != kbID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Folder does not belong to this knowledge base"))
		return
	}

	// Delete folder
	if err := h.folderService.DeleteFolder(ctx, folderID, force); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"folder_id": folderID,
			"force":     force,
		})
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Folder deleted: %s (force=%v)", folderID, force)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Folder deleted successfully",
	})
}

// MoveFolder godoc
// @Summary      Move folder
// @Description  Move a folder to a new parent location
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id         path      string  true  "Knowledge Base ID"
// @Param        folder_id  path      string  true  "Folder ID"
// @Param        body       body      types.MoveFolderRequest  true  "Move request"
// @Success      200        {object}  types.KnowledgeFolder
// @Failure      400        {object}  errors.AppError
// @Failure      403        {object}  errors.AppError
// @Failure      404        {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id}/move [post]
func (h *KnowledgeFolderHandler) MoveFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Parse request body
	var req types.MoveFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError("Invalid request body"))
		return
	}

	// Verify folder belongs to this KB
	folder, err := h.folderService.GetFolder(ctx, folderID)
	if err != nil {
		c.JSON(http.StatusNotFound, errors.NewNotFoundError("Folder not found"))
		return
	}
	if folder.KnowledgeBaseID != kbID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Folder does not belong to this knowledge base"))
		return
	}

	// Move folder
	updatedFolder, err := h.folderService.MoveFolder(ctx, folderID, &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"folder_id":       folderID,
			"target_parent":   req.TargetParentFolderID,
		})
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Folder moved: %s to parent %v", folderID, req.TargetParentFolderID)
	c.JSON(http.StatusOK, updatedFolder)
}

// GetBreadcrumb godoc
// @Summary      Get folder breadcrumb
// @Description  Get the breadcrumb path from root to the specified folder
// @Tags         Folders
// @Accept       json
// @Produce      json
// @Param        id         path      string  true  "Knowledge Base ID"
// @Param        folder_id  path      string  true  "Folder ID"
// @Success      200        {array}   types.KnowledgeFolder
// @Failure      403        {object}  errors.AppError
// @Failure      404        {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id}/breadcrumb [get]
func (h *KnowledgeFolderHandler) GetBreadcrumb(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))

	// Validate knowledge base access
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb.TenantID != tenantID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Get breadcrumb
	breadcrumb, err := h.folderService.GetBreadcrumb(ctx, folderID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get breadcrumb for folder %s: %v", folderID, err)
		c.JSON(http.StatusNotFound, errors.NewNotFoundError("Folder not found"))
		return
	}

	// Verify folder belongs to this KB
	if len(breadcrumb) > 0 && breadcrumb[len(breadcrumb)-1].KnowledgeBaseID != kbID {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Folder does not belong to this knowledge base"))
		return
	}

	c.JSON(http.StatusOK, breadcrumb)
}
