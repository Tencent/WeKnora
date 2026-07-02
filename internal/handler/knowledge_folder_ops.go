package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MoveKnowledgeToFolder godoc
// @Summary      Move knowledge to folder
// @Description  Move a knowledge entry to a specified folder or to root
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "Knowledge ID"
// @Param        body body      object  true  "Move request with folder_id (null for root)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      403  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledges/{id}/folder [put]
func (h *KnowledgeHandler) MoveKnowledgeToFolder(c *gin.Context) {
	ctx := c.Request.Context()
	knowledgeID := secutils.SanitizeForLog(c.Param("id"))

	// Parse request body
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warnf(ctx, "Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError("Invalid request body"))
		return
	}

	// Validate access
	_, effectiveCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, knowledgeID, types.OrgRoleEditor)
	if err != nil {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Move to folder
	if err := h.kgService.MoveToFolder(effectiveCtx, knowledgeID, req.FolderID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": knowledgeID,
			"folder_id":    req.FolderID,
		})
		c.JSON(http.StatusInternalServerError, errors.NewInternalServerError(err.Error()))
		return
	}

	folderIDStr := "root"
	if req.FolderID != nil {
		folderIDStr = *req.FolderID
	}
	logger.Infof(ctx, "Knowledge %s moved to folder %s", knowledgeID, folderIDStr)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge moved successfully",
		"data": gin.H{
			"knowledge_id": knowledgeID,
			"folder_id":    req.FolderID,
		},
	})
}

// BatchMoveKnowledgeToFolder godoc
// @Summary      Batch move knowledge to folder
// @Description  Move multiple knowledge entries to a specified folder or to root
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        body body      object  true  "Batch move request with knowledge_ids and folder_id"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledges/batch-move-folder [post]
func (h *KnowledgeHandler) BatchMoveKnowledgeToFolder(c *gin.Context) {
	ctx := c.Request.Context()

	// Parse request body
	var req struct {
		KnowledgeIDs []string `json:"knowledge_ids" binding:"required,min=1"`
		FolderID     *string  `json:"folder_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warnf(ctx, "Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError("Invalid request body"))
		return
	}

	// Validate access for all knowledge entries (check first one for KB access)
	if len(req.KnowledgeIDs) == 0 {
		c.JSON(http.StatusBadRequest, errors.NewBadRequestError("No knowledge IDs provided"))
		return
	}

	firstKnowledge, effectiveCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, req.KnowledgeIDs[0], types.OrgRoleEditor)
	if err != nil {
		c.JSON(http.StatusForbidden, errors.NewForbiddenError("Permission denied"))
		return
	}

	// Batch move
	if err := h.kgService.BatchMoveToFolder(effectiveCtx, req.KnowledgeIDs, req.FolderID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_count": len(req.KnowledgeIDs),
			"folder_id":       req.FolderID,
		})
		c.JSON(http.StatusInternalServerError, errors.NewInternalServerError(err.Error()))
		return
	}

	folderIDStr := "root"
	if req.FolderID != nil {
		folderIDStr = *req.FolderID
	}
	logger.Infof(ctx, "Batch moved %d knowledge entries to folder %s (KB: %s)",
		len(req.KnowledgeIDs), folderIDStr, firstKnowledge.KnowledgeBaseID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge entries moved successfully",
		"data": gin.H{
			"moved_count": len(req.KnowledgeIDs),
			"folder_id":   req.FolderID,
		},
	})
}
