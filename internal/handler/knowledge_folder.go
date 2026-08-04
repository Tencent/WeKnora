package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type KnowledgeFolderHandler struct {
	service interfaces.KnowledgeFolderService
}

func NewKnowledgeFolderHandler(service interfaces.KnowledgeFolderService) *KnowledgeFolderHandler {
	return &KnowledgeFolderHandler{service: service}
}

// List godoc
// @Summary      List knowledge folders
// @Tags         Knowledge Folders
// @Produce      json
// @Param        id path string true "Knowledge base ID"
// @Success      200 {object} map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [get]
func (h *KnowledgeFolderHandler) List(c *gin.Context) {
	ctx, tenantID, kbID, ok := knowledgeFolderRequestScope(c)
	if !ok {
		return
	}
	folders, err := h.service.List(ctx, tenantID, kbID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	if folders == nil {
		folders = []*types.KnowledgeFolder{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folders})
}

// Create godoc
// @Summary      Create a knowledge folder
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id path string true "Knowledge base ID"
// @Param        request body types.CreateKnowledgeFolderRequest true "Folder"
// @Success      201 {object} map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [post]
func (h *KnowledgeFolderHandler) Create(c *gin.Context) {
	ctx, tenantID, kbID, ok := knowledgeFolderRequestScope(c)
	if !ok {
		return
	}
	var req types.CreateKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	folder, err := h.service.Create(ctx, tenantID, kbID, req.ParentID, req.Name)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": folder})
}

// Update godoc
// @Summary      Rename or move a knowledge folder
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id path string true "Knowledge base ID"
// @Param        folder_id path string true "Folder ID"
// @Param        request body types.UpdateKnowledgeFolderRequest true "Folder update"
// @Success      200 {object} map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeFolderHandler) Update(c *gin.Context) {
	ctx, tenantID, kbID, ok := knowledgeFolderRequestScope(c)
	if !ok {
		return
	}
	folderID := strings.TrimSpace(c.Param("folder_id"))
	if !validKnowledgeFolderUUID(folderID) {
		c.Error(apperrors.NewBadRequestError("invalid folder ID"))
		return
	}
	var req types.UpdateKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	if req.Name == nil && !req.MoveParent {
		c.Error(apperrors.NewBadRequestError("name or parent_id is required"))
		return
	}
	folder, err := h.service.Update(
		ctx,
		tenantID,
		kbID,
		folderID,
		req.Name,
		req.ParentID,
		req.MoveParent,
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// Delete godoc
// @Summary      Delete an empty knowledge folder
// @Tags         Knowledge Folders
// @Param        id path string true "Knowledge base ID"
// @Param        folder_id path string true "Folder ID"
// @Success      204
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeFolderHandler) Delete(c *gin.Context) {
	ctx, tenantID, kbID, ok := knowledgeFolderRequestScope(c)
	if !ok {
		return
	}
	folderID := strings.TrimSpace(c.Param("folder_id"))
	if !validKnowledgeFolderUUID(folderID) {
		c.Error(apperrors.NewBadRequestError("invalid folder ID"))
		return
	}
	if err := h.service.Delete(ctx, tenantID, kbID, folderID); err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// MoveKnowledge godoc
// @Summary      Move one document to a folder
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id path string true "Knowledge base ID"
// @Param        knowledge_id path string true "Knowledge ID"
// @Param        request body types.MoveKnowledgeFolderRequest true "Target folder"
// @Success      200 {object} map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/{knowledge_id}/folder [put]
func (h *KnowledgeFolderHandler) MoveKnowledge(c *gin.Context) {
	ctx, tenantID, kbID, ok := knowledgeFolderRequestScope(c)
	if !ok {
		return
	}
	knowledgeID := strings.TrimSpace(c.Param("knowledge_id"))
	if !validKnowledgeFolderUUID(knowledgeID) {
		c.Error(apperrors.NewBadRequestError("invalid knowledge ID"))
		return
	}
	var req types.MoveKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	count, err := h.service.MoveKnowledge(ctx, tenantID, kbID, []string{knowledgeID}, req.FolderID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"moved_count": count}})
}

// BatchMoveKnowledge godoc
// @Summary      Move documents to a folder
// @Tags         Knowledge Folders
// @Accept       json
// @Produce      json
// @Param        id path string true "Knowledge base ID"
// @Param        request body types.BatchMoveKnowledgeFolderRequest true "Documents and target folder"
// @Success      200 {object} map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/batch-folder [post]
func (h *KnowledgeFolderHandler) BatchMoveKnowledge(c *gin.Context) {
	ctx, tenantID, kbID, ok := knowledgeFolderRequestScope(c)
	if !ok {
		return
	}
	var req types.BatchMoveKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	count, err := h.service.MoveKnowledge(ctx, tenantID, kbID, req.KnowledgeIDs, req.FolderID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"moved_count": count}})
}

func knowledgeFolderRequestScope(c *gin.Context) (
	ctx context.Context,
	tenantID uint64,
	kbID string,
	ok bool,
) {
	ctx = c.Request.Context()
	tenantID, ok = types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		c.Error(apperrors.NewUnauthorizedError("workspace context unavailable"))
		return nil, 0, "", false
	}
	kbID = strings.TrimSpace(c.Param("id"))
	if !validKnowledgeFolderUUID(kbID) {
		c.Error(apperrors.NewBadRequestError("invalid knowledge base ID"))
		return nil, 0, "", false
	}
	return ctx, tenantID, kbID, true
}

func validKnowledgeFolderUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func writeKnowledgeFolderError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, service.ErrKnowledgeFolderInvalid),
		stderrors.Is(err, service.ErrKnowledgeMoveInvalid):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	case stderrors.Is(err, repository.ErrKnowledgeFolderNotFound),
		stderrors.Is(err, repository.ErrKnowledgeNotFound):
		c.Error(apperrors.NewNotFoundError(err.Error()))
	case stderrors.Is(err, service.ErrKnowledgeFolderConflict),
		stderrors.Is(err, service.ErrKnowledgeFolderCycle),
		stderrors.Is(err, repository.ErrKnowledgeFolderNotEmpty):
		c.Error(apperrors.NewConflictError(err.Error()))
	default:
		c.Error(apperrors.NewInternalServerError("knowledge folder operation failed"))
	}
}
