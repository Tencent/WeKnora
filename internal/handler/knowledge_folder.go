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
	"github.com/gin-gonic/gin"
)

func (h *KnowledgeHandler) ListKnowledgeFolders(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, false)
	if err != nil {
		c.Error(err)
		return
	}
	nodes, err := h.folderService.List(ctx, kbID)
	if err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": nodes})
}

func (h *KnowledgeHandler) CreateKnowledgeFolder(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, true)
	if err != nil {
		c.Error(err)
		return
	}
	var req types.KnowledgeFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	folder, err := h.folderService.Create(ctx, kbID, &req)
	if err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": folder})
}

func (h *KnowledgeHandler) UpdateKnowledgeFolder(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, true)
	if err != nil {
		c.Error(err)
		return
	}
	var req types.KnowledgeFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	folder, err := h.folderService.Update(ctx, kbID, c.Param("folder_id"), &req)
	if err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

func (h *KnowledgeHandler) DeleteKnowledgeFolder(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, true)
	if err != nil {
		c.Error(err)
		return
	}
	if err := h.folderService.Delete(ctx, kbID, c.Param("folder_id")); err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *KnowledgeHandler) ReparseKnowledgeFolder(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, true)
	if err != nil {
		c.Error(err)
		return
	}
	folderID := strings.TrimSpace(c.Param("folder_id"))
	tenantID := types.MustTenantIDFromContext(ctx)
	ids, err := h.folderService.ResolveKnowledgeIDs(ctx, tenantID, types.FolderScope{
		KnowledgeBaseID:    kbID,
		FolderID:           folderID,
		IncludeDescendants: true,
	})
	if err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"reparse_count": 0}})
		return
	}
	taskID, err := h.enqueueKnowledgeListReparse(ctx, tenantID, ids, nil)
	if err != nil {
		c.Error(apperrors.NewInternalServerError("failed to enqueue folder reparse task"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"task_id": taskID, "reparse_count": len(ids)},
	})
}

func (h *KnowledgeHandler) DeleteKnowledgeFolderRecursive(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, true)
	if err != nil {
		c.Error(err)
		return
	}
	folderID := strings.TrimSpace(c.Param("folder_id"))
	tenantID := types.MustTenantIDFromContext(ctx)
	ids, err := h.folderService.ResolveKnowledgeIDs(ctx, tenantID, types.FolderScope{
		KnowledgeBaseID:    kbID,
		FolderID:           folderID,
		IncludeDescendants: true,
	})
	if err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}

	taskID, err := h.enqueueKnowledgeListDelete(ctx, tenantID, ids, &types.FolderDeleteTarget{
		KnowledgeBaseID: kbID,
		FolderID:        folderID,
	})
	if err != nil {
		c.Error(apperrors.NewInternalServerError("failed to enqueue folder delete task"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"task_id": taskID, "deleted_count": len(ids)},
	})
}

func (h *KnowledgeHandler) MoveKnowledgeToFolder(c *gin.Context) {
	ctx, kbID, err := h.knowledgeFolderContext(c, true)
	if err != nil {
		c.Error(err)
		return
	}
	var req types.MoveKnowledgeToFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	if err := h.folderService.MoveKnowledge(ctx, kbID, req.KnowledgeIDs, req.FolderID); err != nil {
		c.Error(mapKnowledgeFolderError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *KnowledgeHandler) knowledgeFolderContext(c *gin.Context, write bool) (context.Context, string, error) {
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		return c.Request.Context(), kbID, err
	}
	if write && permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		return c.Request.Context(), kbID, apperrors.NewForbiddenError("No permission to modify knowledge folders")
	}
	return context.WithValue(c.Request.Context(), types.TenantIDContextKey, effectiveTenantID), kbID, nil
}

func mapKnowledgeFolderError(err error) error {
	switch {
	case stderrors.Is(err, repository.ErrKnowledgeFolderNotFound):
		return apperrors.NewNotFoundError("knowledge folder not found")
	case stderrors.Is(err, repository.ErrKnowledgeFolderConflict):
		return apperrors.NewConflictError("a folder with this name already exists")
	case stderrors.Is(err, repository.ErrKnowledgeFolderNotEmpty):
		return apperrors.NewConflictError("folder must be empty before it can be deleted")
	case stderrors.Is(err, repository.ErrKnowledgeFolderMove):
		return apperrors.NewBadRequestError("one or more knowledge items do not belong to this knowledge base")
	case stderrors.Is(err, service.ErrKnowledgeFolderInvalidName):
		return apperrors.NewBadRequestError("folder name is required, must be at most 255 characters, and cannot contain path separators")
	case stderrors.Is(err, service.ErrKnowledgeFolderDepthLimit):
		return apperrors.NewBadRequestError("folder nesting cannot exceed 10 levels")
	case stderrors.Is(err, service.ErrKnowledgeFolderCycle):
		return apperrors.NewBadRequestError("a folder cannot be moved into itself or its descendant")
	default:
		return apperrors.NewInternalServerError(err.Error())
	}
}

func normalizeRequestFolderID(raw string) *string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return &raw
}

func normalizeRequestFolderIDPtr(raw *string) *string {
	if raw == nil {
		return nil
	}
	return normalizeRequestFolderID(*raw)
}
