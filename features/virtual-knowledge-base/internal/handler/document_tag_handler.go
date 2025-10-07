package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	service "github.com/tencent/weknora/features/virtualkb/internal/service/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// DocumentTagHandler exposes endpoints for document-tag relationships.
type DocumentTagHandler struct {
	service service.DocumentTagService
}

// NewDocumentTagHandler creates a new handler instance.
func NewDocumentTagHandler(service service.DocumentTagService) *DocumentTagHandler {
	return &DocumentTagHandler{service: service}
}

// RegisterRoutes wires document-tag routes under the provided router group.
func (h *DocumentTagHandler) RegisterRoutes(rg *gin.RouterGroup) {
	docs := rg.Group("/documents")
	{
		docs.GET(":id/tags", h.listTagsForDocument)
		docs.POST(":id/tags", h.assignTag)
		docs.PUT(":id/tags/:tag_id", h.updateTag)
		docs.DELETE(":id/tags/:tag_id", h.removeTag)
	}

	tags := rg.Group("/tags")
	{
		tags.GET(":tag_id/documents", h.listDocumentsForTag)
	}
}

func (h *DocumentTagHandler) assignTag(c *gin.Context) {
	documentID := c.Param("id")
	var req assignTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid tag assignment payload", err)
		return
	}

	assignment := &types.DocumentTag{
		DocumentID: documentID,
		TagID:      req.TagID,
		Weight:     req.Weight,
	}
	if err := h.service.AssignTag(c.Request.Context(), assignment); err != nil {
		respondBadRequest(c, "failed to assign tag", err)
		return
	}
	respondSuccess(c, http.StatusCreated, assignment)
}

func (h *DocumentTagHandler) updateTag(c *gin.Context) {
	documentID := c.Param("id")
	tagID, err := parseIDParam(c.Param("tag_id"))
	if err != nil {
		respondBadRequest(c, "invalid tag id", err)
		return
	}

	var req assignTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid tag assignment payload", err)
		return
	}

	assignment := &types.DocumentTag{
		DocumentID: documentID,
		TagID:      tagID,
		Weight:     req.Weight,
	}
	if err := h.service.UpdateTag(c.Request.Context(), assignment); err != nil {
		respondBadRequest(c, "failed to update tag assignment", err)
		return
	}
	respondSuccess(c, http.StatusOK, assignment)
}

func (h *DocumentTagHandler) removeTag(c *gin.Context) {
	documentID := c.Param("id")
	tagID, err := parseIDParam(c.Param("tag_id"))
	if err != nil {
		respondBadRequest(c, "invalid tag id", err)
		return
	}

	if err := h.service.RemoveTag(c.Request.Context(), documentID, tagID); err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusNoContent, nil)
}

func (h *DocumentTagHandler) listTagsForDocument(c *gin.Context) {
	documentID := c.Param("id")
	tags, err := h.service.ListTags(c.Request.Context(), documentID)
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, tags)
}

func (h *DocumentTagHandler) listDocumentsForTag(c *gin.Context) {
	tagID, err := parseIDParam(c.Param("tag_id"))
	if err != nil {
		respondBadRequest(c, "invalid tag id", err)
		return
	}
	documents, err := h.service.ListDocuments(c.Request.Context(), tagID)
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, documents)
}

type assignTagRequest struct {
	TagID  int64    `json:"tag_id" binding:"required"`
	Weight *float64 `json:"weight"`
}
