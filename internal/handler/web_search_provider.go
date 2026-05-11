package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// WebSearchProviderHandler handles HTTP requests for web search provider CRUD
type WebSearchProviderHandler struct {
	repo     interfaces.WebSearchProviderRepository
	service  interfaces.WebSearchProviderService
	registry *infra_web_search.Registry
}

// NewWebSearchProviderHandler creates a new handler
func NewWebSearchProviderHandler(
	repo interfaces.WebSearchProviderRepository,
	service interfaces.WebSearchProviderService,
	registry *infra_web_search.Registry,
) *WebSearchProviderHandler {
	return &WebSearchProviderHandler{repo: repo, service: service, registry: registry}
}

// --- request DTOs ---

// CreateProviderRequest defines the request body for creating a provider
type CreateProviderRequest struct {
	Name        string                            `json:"name" binding:"required"`
	Provider    types.WebSearchProviderType       `json:"provider" binding:"required"`
	Description string                            `json:"description"`
	Parameters  types.WebSearchProviderParameters `json:"parameters"`
	IsDefault   bool                              `json:"is_default"`
}

// UpdateProviderRequest defines the request body for updating a provider
type UpdateProviderRequest struct {
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	Parameters  types.WebSearchProviderParameters `json:"parameters"`
	IsDefault   bool                              `json:"is_default"`
}

// --- helpers ---

// getTenantID extracts tenant ID from gin context (set by auth middleware).
func (h *WebSearchProviderHandler) getTenantID(c *gin.Context) uint64 {
	return c.GetUint64(types.TenantIDContextKey.String())
}

// getOwnedProvider loads a provider and verifies it belongs to the given tenant.
// Returns (nil, status, msg) on failure so callers can respond immediately.
func (h *WebSearchProviderHandler) getOwnedProvider(
	ctx context.Context, tenantID uint64, id string,
) (*types.WebSearchProviderEntity, int, string) {
	provider, err := h.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, http.StatusInternalServerError, "failed to query provider"
	}
	if provider == nil {
		return nil, http.StatusNotFound, "web search provider not found"
	}
	return provider, http.StatusOK, ""
}

// --- endpoints ---

// CreateProvider creates a new web search provider
func (h *WebSearchProviderHandler) CreateProvider(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized: tenant context missing"})
		return
	}

	var req CreateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warnf(ctx, "Invalid create provider request: %v", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Creating web search provider: tenant=%d, name=%s, type=%s",
		tenantID, secutils.SanitizeForLog(req.Name), secutils.SanitizeForLog(string(req.Provider)))

	provider := &types.WebSearchProviderEntity{
		TenantID:    tenantID,
		Name:        secutils.SanitizeForLog(req.Name),
		Provider:    req.Provider,
		Description: secutils.SanitizeForLog(req.Description),
		Parameters:  req.Parameters,
		IsDefault:   req.IsDefault,
	}

	if err := h.service.CreateProvider(ctx, provider); err != nil {
		logger.Warnf(ctx, "Failed to create web search provider: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    provider,
	})
}

// ListProviders lists all web search providers for the current tenant
func (h *WebSearchProviderHandler) ListProviders(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized: tenant context missing"})
		return
	}

	providers, err := h.repo.List(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list web search providers: %v", err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    providers,
	})
}

// GetProvider retrieves a single web search provider by ID.
//
// GetProvider godoc
// @Summary      获取网络搜索 Provider 详情
// @Description  根据 ID 获取指定 provider 配置
// @Tags         网络搜索
// @Produce      json
// @Param        id   path      string                          true  "Provider ID"
// @Success      200  {object}  types.WebSearchProviderEntity   "Provider 详情"
// @Failure      404  {object}  map[string]interface{}          "Provider 不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /web-search-providers/{id} [get]
func (h *WebSearchProviderHandler) GetProvider(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized: tenant context missing"})
		return
	}

	id := c.Param("id")
	provider, status, msg := h.getOwnedProvider(ctx, tenantID, id)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"success": false, "error": msg})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    provider,
	})
}

// UpdateProvider updates a web search provider.
//
// UpdateProvider godoc
// @Summary      更新网络搜索 Provider
// @Description  更新指定 provider 的名称/描述/参数/是否默认
// @Tags         网络搜索
// @Accept       json
// @Produce      json
// @Param        id       path      string                          true  "Provider ID"
// @Param        request  body      handler.UpdateProviderRequest   true  "更新字段"
// @Success      200      {object}  types.WebSearchProviderEntity   "更新后的 Provider"
// @Failure      400      {object}  map[string]interface{}          "请求参数错误"
// @Failure      404      {object}  map[string]interface{}          "Provider 不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /web-search-providers/{id} [put]
func (h *WebSearchProviderHandler) UpdateProvider(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized: tenant context missing"})
		return
	}

	id := c.Param("id")

	// Ownership check
	existing, status, msg := h.getOwnedProvider(ctx, tenantID, id)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"success": false, "error": msg})
		return
	}

	var req UpdateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Build updated entity, keeping immutable fields from existing
	provider := &types.WebSearchProviderEntity{
		ID:          id,
		TenantID:    tenantID,
		Name:        secutils.SanitizeForLog(req.Name),
		Provider:    existing.Provider, // Provider type is immutable after creation
		Description: secutils.SanitizeForLog(req.Description),
		Parameters:  req.Parameters,
		IsDefault:   req.IsDefault,
	}

	if err := h.service.UpdateProvider(ctx, provider); err != nil {
		logger.Warnf(ctx, "Failed to update web search provider %s: %v", id, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Re-fetch to get the full stored state
	updated, _ := h.repo.GetByID(ctx, tenantID, id)
	if updated != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": updated})
	} else {
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}

// DeleteProvider deletes a web search provider.
//
// DeleteProvider godoc
// @Summary      删除网络搜索 Provider
// @Description  删除指定 provider 配置
// @Tags         网络搜索
// @Produce      json
// @Param        id   path      string                  true  "Provider ID"
// @Success      200  {object}  map[string]interface{}  "success: true"
// @Failure      404  {object}  map[string]interface{}  "Provider 不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /web-search-providers/{id} [delete]
func (h *WebSearchProviderHandler) DeleteProvider(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized: tenant context missing"})
		return
	}

	id := c.Param("id")

	// Ownership check
	if _, status, msg := h.getOwnedProvider(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"success": false, "error": msg})
		return
	}

	if err := h.service.DeleteProvider(ctx, tenantID, id); err != nil {
		logger.Warnf(ctx, "Failed to delete web search provider %s: %v", id, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListProviderTypes returns available provider types and their parameter requirements.
//
// ListProviderTypes godoc
// @Summary      获取网络搜索 Provider 类型元数据
// @Description  返回 UI 表单需要的 provider 类型及参数定义
// @Tags         网络搜索
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "provider 类型列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /web-search-providers/types [get]
func (h *WebSearchProviderHandler) ListProviderTypes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.GetWebSearchProviderTypes(),
	})
}

// TestProviderByID tests an existing saved provider by performing a sample search.
//
// TestProviderByID godoc
// @Summary      测试已保存的 Provider
// @Description  使用数据库中已保存的凭证测试连通性
// @Tags         网络搜索
// @Produce      json
// @Param        id   path      string                  true  "Provider ID"
// @Success      200  {object}  map[string]interface{}  "测试结果"
// @Failure      404  {object}  map[string]interface{}  "Provider 不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /web-search-providers/{id}/test [post]
func (h *WebSearchProviderHandler) TestProviderByID(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized: tenant context missing"})
		return
	}

	id := c.Param("id")
	provider, status, msg := h.getOwnedProvider(ctx, tenantID, id)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"success": false, "error": msg})
		return
	}

	if err := h.doTestSearch(ctx, string(provider.Provider), provider.Parameters); err != nil {
		logger.Warnf(ctx, "Web search provider test failed: %v", err)
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TestProviderRequest defines the body for testing raw credentials
type TestProviderRequest struct {
	Provider   string                            `json:"provider" binding:"required"`
	Parameters types.WebSearchProviderParameters `json:"parameters"`
}

// TestProviderRaw tests a provider with raw credentials (no persistence).
//
// TestProviderRaw godoc
// @Summary      使用原始凭证测试 Provider（不落库）
// @Description  使用前端表单中尚未保存的凭证测试连通性，用于"测试连接"按钮
// @Tags         网络搜索
// @Accept       json
// @Produce      json
// @Param        request  body      handler.TestProviderRequest  true  "{provider, parameters}"
// @Success      200      {object}  map[string]interface{}  "测试结果"
// @Failure      400      {object}  map[string]interface{}  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /web-search-providers/test [post]
func (h *WebSearchProviderHandler) TestProviderRaw(c *gin.Context) {
	ctx := c.Request.Context()

	var req TestProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if err := h.doTestSearch(ctx, req.Provider, req.Parameters); err != nil {
		logger.Warnf(ctx, "Web search provider test failed: %v", err)
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// doTestSearch creates a temporary provider and runs a simple test query
func (h *WebSearchProviderHandler) doTestSearch(ctx context.Context, providerType string, params types.WebSearchProviderParameters) error {
	logger.Infof(ctx, "[WebSearch][Test] testing provider type=%s", providerType)
	searchProvider, err := h.registry.CreateProvider(providerType, params)
	if err != nil {
		logger.Warnf(ctx, "[WebSearch][Test] failed to create provider: %v", err)
		return fmt.Errorf("failed to create provider: %w", err)
	}
	results, err := searchProvider.Search(ctx, "test", 1, false)
	if err != nil {
		logger.Warnf(ctx, "[WebSearch][Test] search failed: %v", err)
		return err
	}
	if len(results) == 0 {
		logger.Warnf(ctx, "[WebSearch][Test] search returned 0 results — API key or configuration may be invalid")
		return fmt.Errorf("search returned 0 results, please verify your API key and configuration")
	}
	logger.Infof(ctx, "[WebSearch][Test] succeeded: type=%s, results=%d", providerType, len(results))
	return nil
}
