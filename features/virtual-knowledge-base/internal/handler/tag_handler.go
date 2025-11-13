package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	service "github.com/tencent/weknora/features/virtualkb/internal/service/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// TagHandler exposes HTTP endpoints for tag categories and tags.
type TagHandler struct {
	service service.TagService
}

// NewTagHandler creates a new TagHandler instance.
func NewTagHandler(service service.TagService) *TagHandler {
	return &TagHandler{service: service}
}

// RegisterRoutes wires tag routes under the provided router group.
func (h *TagHandler) RegisterRoutes(rg *gin.RouterGroup) {
	categories := rg.Group("/categories")
	{
		categories.GET("", h.listCategories)
		categories.POST("", h.createCategory)
		categories.GET(":id", h.getCategory)
		categories.PUT(":id", h.updateCategory)
		categories.DELETE(":id", h.deleteCategory)
	}

	tags := rg.Group("/tags")
	{
		tags.POST("", h.createTag)
		tags.GET(":id", h.getTag)
		tags.PUT(":id", h.updateTag)
		tags.DELETE(":id", h.deleteTag)
		tags.GET("", h.listTagsByCategory)
	}
}

func (h *TagHandler) createCategory(c *gin.Context) {
	var req types.TagCategory
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid category payload", err)
		return
	}

	if err := h.service.CreateCategory(c.Request.Context(), &req); err != nil {
		respondBadRequest(c, "failed to create category", err)
		return
	}
	respondSuccess(c, http.StatusCreated, req)
}

func (h *TagHandler) listCategories(c *gin.Context) {
	categories, err := h.service.ListCategories(c.Request.Context())
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, categories)
}

func (h *TagHandler) getCategory(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid category id", err)
		return
	}

	category, err := h.service.GetCategoryByID(c.Request.Context(), id)
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, category)
}

func (h *TagHandler) updateCategory(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid category id", err)
		return
	}

	var req types.TagCategory
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid category payload", err)
		return
	}
	req.ID = id

	if err := h.service.UpdateCategory(c.Request.Context(), &req); err != nil {
		respondBadRequest(c, "failed to update category", err)
		return
	}
	respondSuccess(c, http.StatusOK, req)
}

func (h *TagHandler) deleteCategory(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid category id", err)
		return
	}

	if err := h.service.DeleteCategory(c.Request.Context(), id); err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusNoContent, nil)
}

func (h *TagHandler) createTag(c *gin.Context) {
	var req types.Tag
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid tag payload", err)
		return
	}

	if err := h.service.CreateTag(c.Request.Context(), &req); err != nil {
		respondBadRequest(c, "failed to create tag", err)
		return
	}
	respondSuccess(c, http.StatusCreated, req)
}

func (h *TagHandler) getTag(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid tag id", err)
		return
	}

	tag, err := h.service.GetTagByID(c.Request.Context(), id)
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, tag)
}

func (h *TagHandler) updateTag(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid tag id", err)
		return
	}

	var req types.Tag
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid tag payload", err)
		return
	}
	req.ID = id

	if err := h.service.UpdateTag(c.Request.Context(), &req); err != nil {
		respondBadRequest(c, "failed to update tag", err)
		return
	}
	respondSuccess(c, http.StatusOK, req)
}

func (h *TagHandler) deleteTag(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid tag id", err)
		return
	}

	if err := h.service.DeleteTag(c.Request.Context(), id); err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusNoContent, nil)
}

func (h *TagHandler) listTagsByCategory(c *gin.Context) {
	categoryParam := c.Query("category_id")
	if categoryParam == "" {
		respondBadRequest(c, "category_id query parameter is required", nil)
		return
	}
	categoryID, err := strconv.ParseInt(categoryParam, 10, 64)
	if err != nil {
		respondBadRequest(c, "invalid category_id parameter", err)
		return
	}

	tags, err := h.service.ListTagsByCategory(c.Request.Context(), categoryID)
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, tags)
}

func parseIDParam(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}
