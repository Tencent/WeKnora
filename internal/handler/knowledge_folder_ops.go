package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// MoveFolder godoc
// @Summary      Move a knowledge folder
// @Description  Move a folder and its subtree under another folder in the same knowledge base.
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id        path  string                  true  "Knowledge base ID"
// @Param        folder_id path  string                  true  "Folder ID"
// @Param        request   body  types.MoveFolderRequest true  "Move target"
// @Success      200  {object}  types.KnowledgeFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id}/move [post]
func (h *KnowledgeFolderHandler) MoveFolder(c *gin.Context) {
	kbID, folderID, ok := knowledgeFolderParams(c)
	if !ok {
		return
	}
	if folderID == "" {
		_ = c.Error(apperrors.NewBadRequestError("folder ID is required"))
		return
	}
	var req types.MoveFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	folder, err := h.service.MoveFolder(c.Request.Context(), kbID, folderID, &req)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// MoveKnowledgeToFolder godoc
// @Summary      Move a knowledge item to a folder
// @Description  Assign a knowledge item to a folder in its existing knowledge base; an empty folder_id means root.
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id      path  string                   true  "Knowledge ID"
// @Param        request body  object{folder_id=string} true  "Target folder"
// @Success      200
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledges/{id}/folder [put]
func (h *KnowledgeFolderHandler) MoveKnowledgeToFolder(c *gin.Context) {
	knowledgeID := strings.TrimSpace(c.Param("id"))
	if knowledgeID == "" {
		_ = c.Error(apperrors.NewBadRequestError("knowledge ID is required"))
		return
	}
	var req struct {
		FolderID string `json:"folder_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	folderID := strings.TrimSpace(req.FolderID)
	if folderID == types.FolderRootFilter {
		folderID = types.FolderRootID
	}
	if err := h.service.MoveKnowledgeToFolder(c.Request.Context(), knowledgeID, folderID); err != nil {
		if stderrors.Is(err, apprepo.ErrKnowledgeNotFound) {
			_ = c.Error(apperrors.NewNotFoundError("knowledge not found"))
		} else {
			writeKnowledgeFolderError(c, err)
		}
		return
	}
	c.Status(http.StatusOK)
}
