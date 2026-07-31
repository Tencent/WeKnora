package handler

import (
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"net/http"
)

type FolderHandler struct {
	folderService interfaces.KnowledgeFolderService
}

func NewFolderHandler(folderService interfaces.KnowledgeFolderService) *FolderHandler {
	return &FolderHandler{folderService: folderService}
}

type createFolderReq struct {
	Name     string `json:"name" binding:"required"`
	ParentID string `json:"parent_id"`
}

func (h *FolderHandler) CreateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	var req createFolderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request").WithDetails(err.Error()))
		return
	}
	folder, err := h.folderService.CreateFolder(ctx, kbID, req.ParentID, secutils.SanitizeForLog(req.Name))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbID})
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

type updateFolderReq struct {
	Name string `json:"name" binding:"required"`
}

func (h *FolderHandler) UpdateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	var req updateFolderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request").WithDetails(err.Error()))
		return
	}
	folder, err := h.folderService.UpdateFolder(ctx, folderID, secutils.SanitizeForLog(req.Name))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"folder_id": folderID})
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

func (h *FolderHandler) DeleteFolder(c *gin.Context) {
	ctx := c.Request.Context()
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if err := h.folderService.DeleteFolder(ctx, folderID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"folder_id": folderID})
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *FolderHandler) ListFolders(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folders, err := h.folderService.ListFolders(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbID})
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folders})
}

func (h *FolderHandler) GetFolderTree(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	tree, err := h.folderService.GetFolderTree(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kb_id": kbID})
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tree})
}

type moveKnowledgeToFolderReq struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required"`
	FolderID     string   `json:"folder_id"`
}

func (h *FolderHandler) MoveKnowledgeToFolder(c *gin.Context) {
	ctx := c.Request.Context()
	var req moveKnowledgeToFolderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request").WithDetails(err.Error()))
		return
	}
	for _, kid := range req.KnowledgeIDs {
		if err := h.folderService.MoveKnowledgeToFolder(ctx, kid, req.FolderID); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{"knowledge_id": kid, "folder_id": req.FolderID})
			c.Error(err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
