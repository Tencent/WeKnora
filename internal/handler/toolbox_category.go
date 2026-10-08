package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ToolboxCategoryHandler serves workspace tag and resource assignment endpoints.
type ToolboxCategoryHandler struct {
	service interfaces.ToolboxCategoryService
}

// NewToolboxCategoryHandler creates a handler backed by the tag service.
func NewToolboxCategoryHandler(service interfaces.ToolboxCategoryService) *ToolboxCategoryHandler {
	return &ToolboxCategoryHandler{service: service}
}

type toolboxCategoryNameRequest struct {
	Name string `json:"name"`
}

type toolboxCategoryAssignmentRequest struct {
	CategoryIDs *[]string `json:"category_ids"`
}

func toolboxTenantID(c *gin.Context) (uint64, error) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		return 0, apperrors.NewBadRequestError("workspace ID cannot be empty")
	}
	return tenantID, nil
}

// List godoc
// @Summary      List Toolbox tags
// @Description  Lists the workspace tags shared by Skills and MCP services.
// @Tags         Toolbox tags
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /toolbox-categories [get]
func (h *ToolboxCategoryHandler) List(c *gin.Context) {
	tenantID, err := toolboxTenantID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	categories, err := h.service.List(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": categories})
}

// Create godoc
// @Summary      Create a Toolbox tag
// @Tags         Toolbox tags
// @Accept       json
// @Produce      json
// @Param        request  body      toolboxCategoryNameRequest  true  "Tag name"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Failure      409      {object}  apperrors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /toolbox-categories [post]
func (h *ToolboxCategoryHandler) Create(c *gin.Context) {
	tenantID, err := toolboxTenantID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	var req toolboxCategoryNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid tag request"))
		return
	}
	category, err := h.service.Create(c.Request.Context(), tenantID, req.Name)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": category})
}

// Update godoc
// @Summary      Rename a Toolbox tag
// @Tags         Toolbox tags
// @Accept       json
// @Produce      json
// @Param        id       path      string                      true  "Tag ID"
// @Param        request  body      toolboxCategoryNameRequest  true  "Tag name"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Failure      404      {object}  apperrors.AppError
// @Failure      409      {object}  apperrors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /toolbox-categories/{id} [put]
func (h *ToolboxCategoryHandler) Update(c *gin.Context) {
	tenantID, err := toolboxTenantID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	var req toolboxCategoryNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid tag request"))
		return
	}
	category, err := h.service.Update(c.Request.Context(), tenantID, c.Param("id"), req.Name)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": category})
}

// Delete godoc
// @Summary      Delete a Toolbox tag
// @Description  Deletes the tag and its resource assignments, not the resources.
// @Tags         Toolbox tags
// @Produce      json
// @Param        id  path      string  true  "Tag ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  apperrors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /toolbox-categories/{id} [delete]
func (h *ToolboxCategoryHandler) Delete(c *gin.Context) {
	tenantID, err := toolboxTenantID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if err := h.service.Delete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ReplaceSkillCategories godoc
// @Summary      Replace a Skill's tags
// @Tags         Toolbox tags
// @Accept       json
// @Produce      json
// @Param        id       path      string                            true  "Skill catalog ID"
// @Param        request  body      toolboxCategoryAssignmentRequest  true  "Tag IDs"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Failure      404      {object}  apperrors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /skills/catalog/{id}/categories [put]
func (h *ToolboxCategoryHandler) ReplaceSkillCategories(c *gin.Context) {
	h.replaceCategories(c, h.service.ReplaceSkillCategories)
}

// ReplaceMCPServiceCategories godoc
// @Summary      Replace an MCP service's tags
// @Tags         Toolbox tags
// @Accept       json
// @Produce      json
// @Param        id       path      string                            true  "MCP service ID"
// @Param        request  body      toolboxCategoryAssignmentRequest  true  "Tag IDs"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Failure      404      {object}  apperrors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/categories [put]
func (h *ToolboxCategoryHandler) ReplaceMCPServiceCategories(c *gin.Context) {
	h.replaceCategories(c, h.service.ReplaceMCPServiceCategories)
}

func (h *ToolboxCategoryHandler) replaceCategories(
	c *gin.Context,
	replace func(context.Context, uint64, string, []string) ([]types.ToolboxCategory, error),
) {
	tenantID, err := toolboxTenantID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	var req toolboxCategoryAssignmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid tag assignment request"))
		return
	}
	if req.CategoryIDs == nil {
		_ = c.Error(apperrors.NewBadRequestError("category_ids is required"))
		return
	}
	categories, err := replace(c.Request.Context(), tenantID, c.Param("id"), *req.CategoryIDs)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": categories})
}
