package handler

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FolderHandler exposes knowledge-base folder operations over HTTP.
type FolderHandler struct {
	service interfaces.FolderService
}

// NewFolderHandler creates a FolderHandler backed only by FolderService.
func NewFolderHandler(service interfaces.FolderService) *FolderHandler {
	return &FolderHandler{service: service}
}

type createFolderRequest struct {
	Name     *string `json:"name"`
	ParentID *string `json:"parent_id"`
}

type renameFolderRequest struct {
	Name *string `json:"name"`
}

// nullableFolderID distinguishes an omitted JSON field from an explicit null.
type nullableFolderID struct {
	Present bool
	Value   *string
}

func (n *nullableFolderID) UnmarshalJSON(data []byte) error {
	n.Present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	n.Value = &value
	return nil
}

type moveFolderRequest struct {
	ParentID nullableFolderID `json:"parent_id"`
}

// CreateFolder godoc
// @Summary      Create a knowledge-base folder
// @Description  Create a top-level folder or a direct child of parent_id
// @Tags         Folder
// @Accept       json
// @Produce      json
// @Param        id       path  string  true  "Knowledge base ID"
// @Param        request  body  object{name=string,parent_id=string}  true  "Folder data"
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [post]
func (h *FolderHandler) CreateFolder(c *gin.Context) {
	ctx, tenantID, knowledgeBaseID, ok := folderRequestScope(c)
	if !ok {
		return
	}

	var req createFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	if req.Name == nil {
		_ = c.Error(apperrors.NewBadRequestError("name is required"))
		return
	}
	if err := validateOptionalFolderID(req.ParentID); err != nil {
		_ = c.Error(err)
		return
	}

	folder, err := h.service.CreateFolder(ctx, tenantID, knowledgeBaseID, req.ParentID, *req.Name)
	if err != nil {
		writeFolderServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    dto.NewFolderResponse(folder),
	})
}

// GetFolder godoc
// @Summary      Get a knowledge-base folder
// @Tags         Folder
// @Produce      json
// @Param        id         path  string  true  "Knowledge base ID"
// @Param        folder_id  path  string  true  "Folder ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [get]
func (h *FolderHandler) GetFolder(c *gin.Context) {
	ctx, tenantID, knowledgeBaseID, folderID, ok := folderResourceScope(c)
	if !ok {
		return
	}

	folder, err := h.service.GetFolder(ctx, tenantID, knowledgeBaseID, folderID)
	if err != nil {
		writeFolderServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewFolderResponse(folder),
	})
}

// ListFolders godoc
// @Summary      List knowledge-base folders
// @Description  List root folders by default, direct children with parent_id, or all folders with all=true
// @Tags         Folder
// @Produce      json
// @Param        id         path   string  true   "Knowledge base ID"
// @Param        parent_id  query  string  false  "Parent folder ID; empty means root"
// @Param        all        query  bool    false  "Return all folders in the knowledge base"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [get]
func (h *FolderHandler) ListFolders(c *gin.Context) {
	ctx, tenantID, knowledgeBaseID, ok := folderRequestScope(c)
	if !ok {
		return
	}

	all, queryErr := parseFolderAllQuery(c)
	if queryErr != nil {
		_ = c.Error(queryErr)
		return
	}
	rawParentID, parentPresent := c.GetQuery("parent_id")
	if all && parentPresent {
		_ = c.Error(apperrors.NewBadRequestError("all and parent_id cannot be used together"))
		return
	}

	var (
		folders []*types.Folder
		err     error
	)
	if all {
		folders, err = h.service.ListByKnowledgeBase(ctx, tenantID, knowledgeBaseID)
	} else {
		var parentID *string
		if parentPresent && strings.TrimSpace(rawParentID) != "" {
			trimmed := strings.TrimSpace(rawParentID)
			parentID = &trimmed
			if err := validateOptionalFolderID(parentID); err != nil {
				_ = c.Error(err)
				return
			}
		}
		folders, err = h.service.ListChildren(ctx, tenantID, knowledgeBaseID, parentID)
	}
	if err != nil {
		writeFolderServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewFolderResponses(folders),
	})
}

// RenameFolder godoc
// @Summary      Rename a knowledge-base folder
// @Tags         Folder
// @Accept       json
// @Produce      json
// @Param        id         path  string  true  "Knowledge base ID"
// @Param        folder_id  path  string  true  "Folder ID"
// @Param        request    body  object{name=string}  true  "New folder name"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id}/name [patch]
func (h *FolderHandler) RenameFolder(c *gin.Context) {
	ctx, tenantID, knowledgeBaseID, folderID, ok := folderResourceScope(c)
	if !ok {
		return
	}

	var req renameFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	if req.Name == nil {
		_ = c.Error(apperrors.NewBadRequestError("name is required"))
		return
	}

	folder, err := h.service.RenameFolder(ctx, tenantID, knowledgeBaseID, folderID, *req.Name)
	if err != nil {
		writeFolderServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewFolderResponse(folder),
	})
}

// MoveFolder godoc
// @Summary      Move a knowledge-base folder
// @Description  Set parent_id to a folder UUID, or null to move to the knowledge-base root
// @Tags         Folder
// @Accept       json
// @Produce      json
// @Param        id         path  string  true  "Knowledge base ID"
// @Param        folder_id  path  string  true  "Folder ID"
// @Param        request    body  object{parent_id=string}  true  "New parent"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id}/parent [patch]
func (h *FolderHandler) MoveFolder(c *gin.Context) {
	ctx, tenantID, knowledgeBaseID, folderID, ok := folderResourceScope(c)
	if !ok {
		return
	}

	var req moveFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	if !req.ParentID.Present {
		_ = c.Error(apperrors.NewBadRequestError("parent_id is required"))
		return
	}
	if err := validateOptionalFolderID(req.ParentID.Value); err != nil {
		_ = c.Error(err)
		return
	}

	folder, err := h.service.MoveFolder(
		ctx,
		tenantID,
		knowledgeBaseID,
		folderID,
		req.ParentID.Value,
	)
	if err != nil {
		writeFolderServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewFolderResponse(folder),
	})
}

// DeleteFolder godoc
// @Summary      Delete an empty knowledge-base folder
// @Tags         Folder
// @Param        id         path  string  true  "Knowledge base ID"
// @Param        folder_id  path  string  true  "Folder ID"
// @Success      204
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *FolderHandler) DeleteFolder(c *gin.Context) {
	ctx, tenantID, knowledgeBaseID, folderID, ok := folderResourceScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteFolder(ctx, tenantID, knowledgeBaseID, folderID); err != nil {
		writeFolderServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func folderRequestScope(c *gin.Context) (context.Context, uint64, string, bool) {
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		_ = c.Error(apperrors.NewInternalServerError("internal server error"))
		return nil, 0, "", false
	}
	knowledgeBaseID := strings.TrimSpace(c.Param("id"))
	if !isValidFolderUUID(knowledgeBaseID) {
		_ = c.Error(apperrors.NewBadRequestError("invalid knowledge base ID"))
		return nil, 0, "", false
	}
	return ctx, tenantID, knowledgeBaseID, true
}

func folderResourceScope(c *gin.Context) (context.Context, uint64, string, string, bool) {
	ctx, tenantID, knowledgeBaseID, ok := folderRequestScope(c)
	if !ok {
		return nil, 0, "", "", false
	}
	folderID := strings.TrimSpace(c.Param("folder_id"))
	if !isValidFolderUUID(folderID) {
		_ = c.Error(apperrors.NewBadRequestError("invalid folder ID"))
		return nil, 0, "", "", false
	}
	return ctx, tenantID, knowledgeBaseID, folderID, true
}

func validateOptionalFolderID(folderID *string) *apperrors.AppError {
	if folderID == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*folderID)
	if !isValidFolderUUID(trimmed) {
		return apperrors.NewBadRequestError("invalid parent folder ID")
	}
	*folderID = trimmed
	return nil
}

func isValidFolderUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func parseFolderAllQuery(c *gin.Context) (bool, *apperrors.AppError) {
	raw, present := c.GetQuery("all")
	if !present {
		return false, nil
	}
	all, err := strconv.ParseBool(raw)
	if err != nil {
		return false, apperrors.NewBadRequestError("invalid all query parameter")
	}
	return all, nil
}

func writeFolderServiceError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, service.ErrInvalidFolderScope):
		_ = c.Error(apperrors.NewBadRequestError("invalid folder scope"))
	case stderrors.Is(err, service.ErrInvalidFolderName):
		_ = c.Error(apperrors.NewBadRequestError("invalid folder name"))
	case stderrors.Is(err, service.ErrFolderNotFound):
		_ = c.Error(apperrors.NewNotFoundError("folder not found"))
	case stderrors.Is(err, service.ErrParentFolderNotFound):
		_ = c.Error(apperrors.NewNotFoundError("parent folder not found"))
	case stderrors.Is(err, service.ErrFolderNameConflict):
		_ = c.Error(apperrors.NewConflictError("folder already exists"))
	case stderrors.Is(err, service.ErrFolderMoveCycle):
		_ = c.Error(apperrors.NewConflictError("invalid folder move"))
	case stderrors.Is(err, service.ErrFolderNotEmpty):
		_ = c.Error(apperrors.NewConflictError("folder is not empty"))
	case stderrors.Is(err, service.ErrFolderHierarchyCorrupted):
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"knowledge_base_id": c.Param("id"),
			"folder_id":         c.Param("folder_id"),
		})
		_ = c.Error(apperrors.NewInternalServerError("internal server error"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"knowledge_base_id": c.Param("id"),
			"folder_id":         c.Param("folder_id"),
		})
		_ = c.Error(apperrors.NewInternalServerError("internal server error"))
	}
}
