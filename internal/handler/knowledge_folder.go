package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type KnowledgeFolderHandler struct {
	service interfaces.KnowledgeFolderService
}

func NewKnowledgeFolderHandler(service interfaces.KnowledgeFolderService) *KnowledgeFolderHandler {
	return &KnowledgeFolderHandler{service: service}
}

func (h *KnowledgeFolderHandler) List(c *gin.Context) {
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		c.Error(errors.NewBadRequestError("invalid pagination"))
		return
	}
	result, err := h.service.List(c.Request.Context(), c.Param("id"), c.Query("parent_id"), c.Query("keyword"), &page)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
func (h *KnowledgeFolderHandler) Get(c *gin.Context) {
	result, err := h.service.Get(c.Request.Context(), c.Param("id"), c.Param("folder_id"))
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

type createFolderRequest struct {
	Name     string `json:"name" binding:"required"`
	ParentID string `json:"parent_id"`
}

func (h *KnowledgeFolderHandler) Create(c *gin.Context) {
	var req createFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	result, err := h.service.Create(c.Request.Context(), c.Param("id"), req.ParentID, req.Name)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": result})
}

type updateFolderRequest struct {
	Name     *string `json:"name"`
	ParentID *string `json:"parent_id"`
}

func (h *KnowledgeFolderHandler) Update(c *gin.Context) {
	var req updateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if req.Name == nil && req.ParentID == nil {
		c.Error(errors.NewBadRequestError("name or parent_id is required"))
		return
	}
	result, err := h.service.Update(c.Request.Context(), c.Param("id"), c.Param("folder_id"), req.Name, req.ParentID)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
func (h *KnowledgeFolderHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), c.Param("id"), c.Param("folder_id")); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type ensurePathsRequest struct {
	ParentID string                   `json:"parent_id"`
	Paths    []types.EnsureFolderPath `json:"paths" binding:"required"`
}

func (h *KnowledgeFolderHandler) EnsurePaths(c *gin.Context) {
	var req ensurePathsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	result, err := h.service.EnsurePaths(c.Request.Context(), c.Param("id"), req.ParentID, req.Paths)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

type moveKnowledgeFolderRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required"`
	FolderID     string   `json:"folder_id"`
}

func (h *KnowledgeFolderHandler) MoveKnowledge(c *gin.Context) {
	var req moveKnowledgeFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := h.service.MoveKnowledge(c.Request.Context(), c.Param("id"), req.KnowledgeIDs, req.FolderID); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
