package handler

import (
	goerrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// ListFolders godoc
// @Summary      List knowledge folders
// @Description  List the direct child folders of parent_id (empty = root level) for a knowledge base
// @Tags         知识管理
// @Param        id        path  string  true  "Knowledge base ID"
// @Param        parent_id query string  false "Parent folder ID (empty = root)"
// @Success      200  {object}  types.KnowledgeFolderListResponse
// @Security     Bearer
// @Router       /knowledge-bases/{id}/folders [get]
func (h *KnowledgeHandler) ListFolders(c *gin.Context) {
	_, kbID, _, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	parentID := strings.TrimSpace(c.Query("parent_id"))

	// ?all=true returns the whole folder tree in one request (with recursive
	// knowledge counts) so flat pickers — e.g. the chat @mention folder list —
	// don't need one round-trip per directory.
	if c.Query("all") == "true" {
		allFolders, ferr := h.kgService.ListAllFoldersFlat(c.Request.Context(), kbID)
		if ferr != nil {
			c.Error(errors.NewInternalServerError(ferr.Error()))
			return
		}
		if allFolders == nil {
			allFolders = []types.KnowledgeFolderNode{}
		}
		c.JSON(http.StatusOK, types.KnowledgeFolderListResponse{ParentID: "", Folders: allFolders})
		return
	}

	folders, err := h.kgService.ListChildFolders(c.Request.Context(), kbID, parentID)
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if folders == nil {
		folders = []types.KnowledgeFolderNode{}
	}
	c.JSON(http.StatusOK, types.KnowledgeFolderListResponse{ParentID: parentID, Folders: folders})
}

// CreateFolder godoc
// @Summary      Create a knowledge folder
// @Description  Create a new (initially empty) directory node under parent_id
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id      path  string                            true  "Knowledge base ID"
// @Param        folder  body  types.KnowledgeFolderCreateRequest true  "Folder data"
// @Success      201  {object}  types.KnowledgeFolder
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/folders [post]
func (h *KnowledgeHandler) CreateFolder(c *gin.Context) {
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	var req types.KnowledgeFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	folder, err := h.kgService.CreateFolder(c.Request.Context(), kbID, effectiveTenantID, strings.TrimSpace(req.ParentID), req.Name)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, folder)
}

// UpdateFolder godoc
// @Summary      Rename or move a knowledge folder
// @Description  Rename and/or reparent a folder; the whole subtree's paths and depths are recomputed
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id         path  string                            true  "Knowledge base ID"
// @Param        folder_id  path  string                            true  "Folder ID"
// @Param        folder     body  types.KnowledgeFolderUpdateRequest true  "Folder update"
// @Success      200  {object}  types.KnowledgeFolder
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeHandler) UpdateFolder(c *gin.Context) {
	_, kbID, _, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder ID is required"})
		return
	}
	var req types.KnowledgeFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	folder, err := h.kgService.RenameOrMoveFolder(
		c.Request.Context(), kbID, folderID, req.Name, strings.TrimSpace(req.ParentID), req.MoveParent)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary      Delete an empty knowledge folder
// @Description  Delete a folder that has no knowledges and no child folders
// @Tags         知识管理
// @Param        id         path  string  true  "Knowledge base ID"
// @Param        folder_id  path  string  true  "Folder ID"
// @Success      204
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeHandler) DeleteFolder(c *gin.Context) {
	_, kbID, _, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder ID is required"})
		return
	}
	if err := h.kgService.DeleteFolder(c.Request.Context(), kbID, folderID); err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SetKnowledgeFolder godoc
// @Summary      Move a knowledge into a folder
// @Description  Relocate a knowledge (identified by :knowledge_id in the path) into a folder (folder_id empty = root)
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id            path  string                      true  "Knowledge base ID"
// @Param        knowledge_id  path  string                      true  "Knowledge ID"
// @Param        body          body  types.SetKnowledgeFolderRequest true  "Target folder"
// @Success      200  {object}  types.Knowledge
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/knowledge/{knowledge_id}/folder [put]
func (h *KnowledgeHandler) SetKnowledgeFolder(c *gin.Context) {
	_, kbID, _, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	knowledgeID := secutils.SanitizeForLog(c.Param("knowledge_id"))
	if knowledgeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Knowledge ID is required"})
		return
	}
	var req types.SetKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	knowledge, err := h.kgService.SetKnowledgeFolder(c.Request.Context(), kbID, knowledgeID, strings.TrimSpace(req.FolderID))
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, knowledge)
}

// writeKnowledgeFolderError maps folder service errors to HTTP status codes.
func writeKnowledgeFolderError(c *gin.Context, err error) {
	switch {
	case goerrors.Is(err, repository.ErrKnowledgeFolderNotFound),
		goerrors.Is(err, repository.ErrKnowledgeNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case goerrors.Is(err, repository.ErrKnowledgeFolderConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
