package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/agent"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TenantHandler implements HTTP request handlers for tenant management
// Provides functionality for creating, retrieving, updating, and deleting tenants
// through the REST API endpoints
type TenantHandler struct {
	service     interfaces.TenantService
	userService interfaces.UserService
	config      *config.Config
}

// authorizeTenantAccess checks that the authenticated user owns the target tenant
// or has cross-tenant access privileges. Returns the current user on success.
func (h *TenantHandler) authorizeTenantAccess(c *gin.Context, targetTenantID uint64) (*types.User, bool) {
	ctx := c.Request.Context()

	user, ok := ctx.Value(types.UserContextKey).(*types.User)
	if !ok || user == nil {
		c.Error(errors.NewUnauthorizedError("Authentication required"))
		return nil, false
	}

	if user.TenantID == targetTenantID {
		return user, true
	}

	if h.config != nil && h.config.Tenant != nil && h.config.Tenant.EnableCrossTenantAccess && user.CanAccessAllTenants {
		return user, true
	}

	logger.Warnf(ctx, "User %s (tenant %d) attempted to access tenant %d without permission",
		user.ID, user.TenantID, targetTenantID)
	c.Error(errors.NewForbiddenError("Access denied: you do not have permission to access this tenant"))
	return nil, false
}

// NewTenantHandler creates a new tenant handler instance with the provided service
// Parameters:
//   - service: An implementation of the TenantService interface for business logic
//   - userService: An implementation of the UserService interface for user operations
//   - config: Application configuration
//
// Returns a pointer to the newly created TenantHandler
func NewTenantHandler(service interfaces.TenantService, userService interfaces.UserService, config *config.Config) *TenantHandler {
	return &TenantHandler{
		service:     service,
		userService: userService,
		config:      config,
	}
}

// CreateTenant godoc
// @Summary      创建租户
// @Description  创建新的租户
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        request  body      types.Tenant  true  "租户信息"
// @Success      201      {object}  map[string]interface{}  "创建的租户"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants [post]
func (h *TenantHandler) CreateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating tenant")

	var tenantData types.Tenant
	if err := c.ShouldBindJSON(&tenantData); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		appErr := errors.NewValidationError("Invalid request parameters").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Infof(ctx, "Creating tenant, name: %s", secutils.SanitizeForLog(tenantData.Name))

	createdTenant, err := h.service.CreateTenant(ctx, &tenantData)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to create tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to create tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(
		ctx,
		"Tenant created successfully, ID: %d, name: %s",
		createdTenant.ID,
		secutils.SanitizeForLog(createdTenant.Name),
	)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdTenant,
	})
}

// GetTenant godoc
// @Summary      获取租户详情
// @Description  根据ID获取租户详情
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "租户ID"
// @Success      200  {object}  map[string]interface{}  "租户详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "租户不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/{id} [get]
func (h *TenantHandler) GetTenant(c *gin.Context) {
	ctx := c.Request.Context()

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	if _, ok := h.authorizeTenantAccess(c, id); !ok {
		return
	}

	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve tenant").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tenant,
	})
}

// UpdateTenant godoc
// @Summary      更新租户
// @Description  更新租户信息
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id       path      int           true  "租户ID"
// @Param        request  body      types.Tenant  true  "租户信息"
// @Success      200      {object}  map[string]interface{}  "更新后的租户"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants/{id} [put]
func (h *TenantHandler) UpdateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	if _, ok := h.authorizeTenantAccess(c, id); !ok {
		return
	}

	var tenantData types.Tenant
	if err := c.ShouldBindJSON(&tenantData); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating tenant, ID: %d, Name: %s", id, secutils.SanitizeForLog(tenantData.Name))

	tenantData.ID = id
	updatedTenant, err := h.service.UpdateTenant(ctx, &tenantData)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(
		ctx,
		"Tenant updated successfully, ID: %d, Name: %s",
		updatedTenant.ID,
		secutils.SanitizeForLog(updatedTenant.Name),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant,
	})
}

// DeleteTenant godoc
// @Summary      删除租户
// @Description  删除指定的租户
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "租户ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /tenants/{id} [delete]
func (h *TenantHandler) DeleteTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	if _, ok := h.authorizeTenantAccess(c, id); !ok {
		return
	}

	logger.Infof(ctx, "Deleting tenant, ID: %d", id)

	if err := h.service.DeleteTenant(ctx, id); err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to delete tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to delete tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant deleted successfully, ID: %d", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant deleted successfully",
	})
}

// ListTenants godoc
// @Summary      获取租户列表
// @Description  获取当前用户可访问的租户列表
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "租户列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Router       /tenants [get]
func (h *TenantHandler) ListTenants(c *gin.Context) {
	ctx := c.Request.Context()

	tenant, ok := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if !ok || tenant == nil {
		c.Error(errors.NewUnauthorizedError("Authentication required"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": []*types.Tenant{tenant},
		},
	})
}

// ListAllTenants godoc
// @Summary      获取所有租户列表
// @Description  获取系统中所有租户（需要跨租户访问权限）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "所有租户列表"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Router       /tenants/all [get]
func (h *TenantHandler) ListAllTenants(c *gin.Context) {
	ctx := c.Request.Context()

	// Get current user from context
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		c.Error(errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error()))
		return
	}

	// Check if cross-tenant access is enabled
	if h.config == nil || h.config.Tenant == nil || !h.config.Tenant.EnableCrossTenantAccess {
		logger.Warnf(ctx, "Cross-tenant access is disabled, user: %s", user.ID)
		c.Error(errors.NewForbiddenError("Cross-tenant access is disabled"))
		return
	}

	// Check if user has permission
	if !user.CanAccessAllTenants {
		logger.Warnf(ctx, "User %s attempted to list all tenants without permission", user.ID)
		c.Error(errors.NewForbiddenError("Insufficient permissions to access all tenants"))
		return
	}

	tenants, err := h.service.ListAllTenants(ctx)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve all tenants list: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve all tenants list").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": tenants,
		},
	})
}

// SearchTenants godoc
// @Summary      搜索租户
// @Description  分页搜索租户（需要跨租户访问权限）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        keyword    query     string  false  "搜索关键词"
// @Param        tenant_id  query     int     false  "租户ID筛选"
// @Param        page       query     int     false  "页码"  default(1)
// @Param        page_size  query     int     false  "每页数量"  default(20)
// @Success      200        {object}  map[string]interface{}  "搜索结果"
// @Failure      403        {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/search [get]
func (h *TenantHandler) SearchTenants(c *gin.Context) {
	ctx := c.Request.Context()

	// Get current user from context
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		c.Error(errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error()))
		return
	}

	// Check if cross-tenant access is enabled
	if h.config == nil || h.config.Tenant == nil || !h.config.Tenant.EnableCrossTenantAccess {
		logger.Warnf(ctx, "Cross-tenant access is disabled, user: %s", user.ID)
		c.Error(errors.NewForbiddenError("Cross-tenant access is disabled"))
		return
	}

	// Check if user has permission
	if !user.CanAccessAllTenants {
		logger.Warnf(ctx, "User %s attempted to search tenants without permission", user.ID)
		c.Error(errors.NewForbiddenError("Insufficient permissions to access all tenants"))
		return
	}

	// Parse query parameters
	keyword := c.Query("keyword")
	tenantIDStr := c.Query("tenant_id")
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("page_size", "20")

	var tenantID uint64
	if tenantIDStr != "" {
		parsedID, err := strconv.ParseUint(tenantIDStr, 10, 64)
		if err == nil {
			tenantID = parsedID
		}
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100 // Limit max page size
	}

	tenants, total, err := h.service.SearchTenants(ctx, keyword, tenantID, page, pageSize)
	if err != nil {
		// Check if this is an application-specific error
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to search tenants: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to search tenants").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     tenants,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AgentConfigRequest represents the request body for updating agent configuration
type AgentConfigRequest struct {
	MaxIterations     int      `json:"max_iterations"`
	ReflectionEnabled bool     `json:"reflection_enabled"`
	AllowedTools      []string `json:"allowed_tools"`
	Temperature       float64  `json:"temperature"`
	SystemPrompt      string   `json:"system_prompt,omitempty"` // Unified system prompt (uses {{web_search_status}} placeholder)
}

// GetTenantAgentConfig godoc
// @Summary      获取租户Agent配置
// @Description  获取租户的全局Agent配置（默认应用于所有会话）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Agent配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/agent-config [get]
func (h *TenantHandler) GetTenantAgentConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	// 从 tools 包集中配置可用工具列表
	availableTools := make([]gin.H, 0)
	for _, t := range agenttools.AvailableToolDefinitions() {
		availableTools = append(availableTools, gin.H{
			"name":        t.Name,
			"label":       t.Label,
			"description": t.Description,
		})
	}

	// 从 agent 包获取占位符定义
	availablePlaceholders := make([]gin.H, 0)
	for _, p := range agent.AvailablePlaceholders() {
		availablePlaceholders = append(availablePlaceholders, gin.H{
			"name":        p.Name,
			"label":       p.Label,
			"description": p.Description,
		})
	}
	if tenant.AgentConfig == nil {
		// Return default config if not set
		logger.Info(ctx, "Tenant has no agent config, returning defaults")

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"max_iterations":           agent.DefaultAgentMaxIterations,
				"reflection_enabled":       agent.DefaultAgentReflectionEnabled,
				"allowed_tools":            agenttools.DefaultAllowedTools(),
				"temperature":              agent.DefaultAgentTemperature,
				"system_prompt":            agent.ProgressiveRAGSystemPrompt,
				"use_custom_system_prompt": false,
				"available_tools":          availableTools,
				"available_placeholders":   availablePlaceholders,
			},
		})
		return
	}

	// Get system prompt, use default if empty
	systemPrompt := tenant.AgentConfig.ResolveSystemPrompt(true) // webSearchEnabled doesn't matter for unified prompt
	if systemPrompt == "" {
		systemPrompt = agent.ProgressiveRAGSystemPrompt
	}

	logger.Infof(ctx, "Retrieved tenant agent config successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"max_iterations":           tenant.AgentConfig.MaxIterations,
			"reflection_enabled":       tenant.AgentConfig.ReflectionEnabled,
			"allowed_tools":            agenttools.DefaultAllowedTools(),
			"temperature":              tenant.AgentConfig.Temperature,
			"system_prompt":            systemPrompt,
			"use_custom_system_prompt": tenant.AgentConfig.UseCustomSystemPrompt,
			"available_tools":          availableTools,
			"available_placeholders":   availablePlaceholders,
		},
	})
}

// updateTenantAgentConfigInternal updates the agent configuration for a tenant
// This sets the global agent configuration for all sessions in this tenant
func (h *TenantHandler) updateTenantAgentConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating tenant agent config")
	var req AgentConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// Validate configuration
	if req.MaxIterations <= 0 || req.MaxIterations > 30 {
		c.Error(errors.NewAgentInvalidMaxIterationsError())
		return
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		c.Error(errors.NewAgentInvalidTemperatureError())
		return
	}

	// Get existing tenant
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	// Update agent configuration
	// Determine if using custom prompt based on whether custom prompts are set
	// Support both new unified SystemPrompt and deprecated separate prompts
	systemPrompt := req.SystemPrompt
	useCustomPrompt := systemPrompt != ""

	agentConfig := &types.AgentConfig{
		MaxIterations:         req.MaxIterations,
		ReflectionEnabled:     req.ReflectionEnabled,
		AllowedTools:          agenttools.DefaultAllowedTools(),
		Temperature:           req.Temperature,
		SystemPrompt:          systemPrompt,
		UseCustomSystemPrompt: useCustomPrompt,
	}

	_, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant agent config").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant agent config updated successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    agentConfig,
		"message": "Agent configuration updated successfully",
	})
}

// GetTenantKV godoc
// @Summary      获取租户KV配置
// @Description  获取租户级别的KV配置（支持agent-config、web-search-config、conversation-config）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        key  path      string  true  "配置键名"
// @Success      200  {object}  map[string]interface{}  "配置值"
// @Failure      400  {object}  errors.AppError         "不支持的键"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/{key} [get]
func (h *TenantHandler) GetTenantKV(c *gin.Context) {
	ctx := c.Request.Context()
	key := secutils.SanitizeForLog(c.Param("key"))

	switch key {
	case "agent-config":
		h.GetTenantAgentConfig(c)
		return
	case "web-search-config":
		h.GetTenantWebSearchConfig(c)
		return
	case "conversation-config":
		h.GetTenantConversationConfig(c)
		return
	case "prompt-templates":
		h.GetPromptTemplates(c)
		return
	case "parser-engine-config":
		h.GetTenantParserEngineConfig(c)
		return
	case "storage-engine-config":
		h.GetTenantStorageEngineConfig(c)
		return
	default:
		logger.Info(ctx, "KV key not supported", "key", key)
		c.Error(errors.NewBadRequestError("unsupported key"))
		return
	}
}

// UpdateTenantKV godoc
// @Summary      更新租户KV配置
// @Description  更新租户级别的KV配置（支持agent-config、web-search-config、conversation-config）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        key      path      string  true  "配置键名"
// @Param        request  body      object  true  "配置值"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "不支持的键"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/{key} [put]
func (h *TenantHandler) UpdateTenantKV(c *gin.Context) {
	ctx := c.Request.Context()
	key := secutils.SanitizeForLog(c.Param("key"))

	switch key {
	case "agent-config":
		h.updateTenantAgentConfigInternal(c)
		return
	case "web-search-config":
		h.updateTenantWebSearchConfigInternal(c)
		return
	case "conversation-config":
		h.updateTenantConversationInternal(c)
		return
	case "parser-engine-config":
		h.updateTenantParserEngineConfigInternal(c)
		return
	case "storage-engine-config":
		h.updateTenantStorageEngineConfigInternal(c)
		return
	default:
		logger.Info(ctx, "KV key not supported", "key", key)
		c.Error(errors.NewBadRequestError("unsupported key"))
		return
	}
}

// updateTenantWebSearchConfigInternal updates tenant's web search config
func (h *TenantHandler) updateTenantWebSearchConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()

	// Bind directly into the strong typed struct
	var cfg types.WebSearchConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// Validate configuration
	if cfg.MaxResults < 1 || cfg.MaxResults > 50 {
		c.Error(errors.NewBadRequestError("max_results must be between 1 and 50"))
		return
	}

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	tenant.WebSearchConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant web search config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.WebSearchConfig,
		"message": "Web search configuration updated successfully",
	})
}

// GetTenantWebSearchConfig godoc
// @Summary      获取租户网络搜索配置
// @Description  获取租户的网络搜索配置
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "网络搜索配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/web-search-config [get]
func (h *TenantHandler) GetTenantWebSearchConfig(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start getting tenant web search config")
	// Get tenant
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	logger.Infof(ctx, "Tenant web search config retrieved successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tenant.WebSearchConfig,
	})
}

// GetTenantParserEngineConfig returns the tenant's parser engine config (MinerU endpoint, API key, etc.).
func (h *TenantHandler) GetTenantParserEngineConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	data := tenant.ParserEngineConfig
	if data == nil {
		data = &types.ParserEngineConfig{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantParserEngineConfigInternal updates the tenant's parser engine config.
func (h *TenantHandler) updateTenantParserEngineConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	var cfg types.ParserEngineConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	tenant.ParserEngineConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant parser engine config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.ParserEngineConfig,
		"message": "解析引擎配置已更新",
	})
}

// GetTenantStorageEngineConfig returns the tenant's storage engine config (Local, MinIO, COS parameters).
func (h *TenantHandler) GetTenantStorageEngineConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	data := tenant.StorageEngineConfig
	if data == nil {
		data = &types.StorageEngineConfig{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantStorageEngineConfigInternal updates the tenant's storage engine config.
func (h *TenantHandler) updateTenantStorageEngineConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	var cfg types.StorageEngineConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	tenant.StorageEngineConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant storage engine config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.StorageEngineConfig,
		"message": "存储引擎配置已更新",
	})
}

func (h *TenantHandler) buildDefaultConversationConfig() *types.ConversationConfig {
	return &types.ConversationConfig{
		Prompt:               h.config.Conversation.Summary.Prompt,
		ContextTemplate:      h.config.Conversation.Summary.ContextTemplate,
		Temperature:          h.config.Conversation.Summary.Temperature,
		MaxCompletionTokens:  h.config.Conversation.Summary.MaxCompletionTokens,
		MaxRounds:            h.config.Conversation.MaxRounds,
		EmbeddingTopK:        h.config.Conversation.EmbeddingTopK,
		KeywordThreshold:     h.config.Conversation.KeywordThreshold,
		VectorThreshold:      h.config.Conversation.VectorThreshold,
		RerankTopK:           h.config.Conversation.RerankTopK,
		RerankThreshold:      h.config.Conversation.RerankThreshold,
		EnableRewrite:        h.config.Conversation.EnableRewrite,
		EnableQueryExpansion: h.config.Conversation.EnableQueryExpansion,
		FallbackStrategy:     h.config.Conversation.FallbackStrategy,
		FallbackResponse:     h.config.Conversation.FallbackResponse,
		FallbackPrompt:       h.config.Conversation.FallbackPrompt,
		RewritePromptUser:    h.config.Conversation.RewritePromptUser,
		RewritePromptSystem:  h.config.Conversation.RewritePromptSystem,
	}
}

func validateConversationConfig(req *types.ConversationConfig) error {
	if req.MaxRounds <= 0 {
		return errors.NewBadRequestError("max_rounds must be greater than 0")
	}
	if req.EmbeddingTopK <= 0 {
		return errors.NewBadRequestError("embedding_top_k must be greater than 0")
	}
	if req.KeywordThreshold < 0 || req.KeywordThreshold > 1 {
		return errors.NewBadRequestError("keyword_threshold must be between 0 and 1")
	}
	if req.VectorThreshold < 0 || req.VectorThreshold > 1 {
		return errors.NewBadRequestError("vector_threshold must be between 0 and 1")
	}
	if req.RerankTopK <= 0 {
		return errors.NewBadRequestError("rerank_top_k must be greater than 0")
	}
	if req.RerankThreshold < 0 || req.RerankThreshold > 1 {
		return errors.NewBadRequestError("rerank_threshold must be between 0 and 1")
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		return errors.NewBadRequestError("temperature must be between 0 and 2")
	}
	if req.MaxCompletionTokens <= 0 || req.MaxCompletionTokens > 100000 {
		return errors.NewBadRequestError("max_completion_tokens must be between 1 and 100000")
	}
	if req.FallbackStrategy != "" &&
		req.FallbackStrategy != string(types.FallbackStrategyFixed) &&
		req.FallbackStrategy != string(types.FallbackStrategyModel) {
		return errors.NewBadRequestError("fallback_strategy is invalid")
	}
	return nil
}

// GetTenantConversationConfig godoc
// @Summary      获取租户对话配置
// @Description  获取租户的全局对话配置（默认应用于普通模式会话）
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "对话配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/conversation-config [get]
func (h *TenantHandler) GetTenantConversationConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	// If tenant has no conversation config, return defaults from config.yaml
	var response *types.ConversationConfig
	logger.Info(ctx, "Tenant has no conversation config, returning defaults")
	response = h.buildDefaultConversationConfig()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// updateTenantConversationInternal updates the conversation configuration for a tenant
// This sets the global conversation configuration for normal mode sessions in this tenant
func (h *TenantHandler) updateTenantConversationInternal(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating tenant conversation config")

	var req types.ConversationConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// Validate configuration
	if err := validateConversationConfig(&req); err != nil {
		c.Error(err)
		return
	}

	// Get existing tenant
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	// Update conversation configuration
	tenant.ConversationConfig = &req

	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant conversation config").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant conversation config updated successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.ConversationConfig,
		"message": "Conversation configuration updated successfully",
	})
}

// GetPromptTemplates godoc
// @Summary      获取提示词模板
// @Description  获取系统配置的提示词模板列表
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "提示词模板配置"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/prompt-templates [get]
func (h *TenantHandler) GetPromptTemplates(c *gin.Context) {
	// Return prompt templates from config.yaml
	templates := h.config.PromptTemplates
	if templates == nil {
		templates = &config.PromptTemplatesConfig{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    templates,
	})
}
