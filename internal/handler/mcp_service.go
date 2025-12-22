package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPServiceHandler handles MCP service related HTTP requests
type MCPServiceHandler struct {
	mcpServiceService interfaces.MCPServiceService
}

// NewMCPServiceHandler creates a new MCP service handler
func NewMCPServiceHandler(mcpServiceService interfaces.MCPServiceService) *MCPServiceHandler {
	return &MCPServiceHandler{
		mcpServiceService: mcpServiceService,
	}
}

// CreateMCPService godoc
// @Summary      创建MCP服务
// @Description  创建新的MCP服务配置
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        request  body      types.MCPService  true  "MCP服务配置"
// @Success      200      {object}  map[string]interface{}  "创建的MCP服务"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services [post]
func (h *MCPServiceHandler) CreateMCPService(c *gin.Context) {
	ctx := c.Request.Context()

	var service types.MCPService
	if err := c.ShouldBindJSON(&service); err != nil {
		logger.Error(ctx, "Failed to parse MCP service request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}
	service.TenantID = tenantID

	if err := h.mcpServiceService.CreateMCPService(ctx, &service); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_name": secutils.SanitizeForLog(service.Name)})
		c.Error(errors.NewInternalServerError("Failed to create MCP service: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    service,
	})
}

// ListMCPServices godoc
// @Summary      获取MCP服务列表
// @Description  获取当前租户的所有MCP服务
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "MCP服务列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services [get]
func (h *MCPServiceHandler) ListMCPServices(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	services, err := h.mcpServiceService.ListMCPServices(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": tenantID})
		c.Error(errors.NewInternalServerError("Failed to list MCP services: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    services,
	})
}

// GetMCPService godoc
// @Summary      获取MCP服务详情
// @Description  根据ID获取MCP服务详情
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"
// @Success      200  {object}  map[string]interface{}  "MCP服务详情"
// @Failure      404  {object}  errors.AppError         "服务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id} [get]
func (h *MCPServiceHandler) GetMCPService(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	service, err := h.mcpServiceService.GetMCPServiceByID(ctx, tenantID, serviceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.Error(errors.NewNotFoundError("MCP service not found"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    service,
	})
}

// UpdateMCPService godoc
// @Summary      更新MCP服务
// @Description  更新MCP服务配置
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "MCP服务ID"
// @Param        request  body      object  true  "更新字段"
// @Success      200      {object}  map[string]interface{}  "更新后的MCP服务"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id} [put]
func (h *MCPServiceHandler) UpdateMCPService(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	// Use map to handle partial updates, including false values
	var updateData map[string]interface{}
	if err := c.ShouldBindJSON(&updateData); err != nil {
		logger.Error(ctx, "Failed to parse MCP service update request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Convert map to MCPService struct for validation and processing
	var service types.MCPService
	service.ID = serviceID
	service.TenantID = tenantID

	// Track which fields are being updated
	updateFields := make(map[string]bool)

	// Map the update data to service struct
	if name, ok := updateData["name"].(string); ok {
		service.Name = name
		updateFields["name"] = true
	}
	if desc, ok := updateData["description"].(string); ok {
		service.Description = desc
		updateFields["description"] = true
	}
	if enabled, ok := updateData["enabled"].(bool); ok {
		if enabled {
			service.Enabled = true
		} else {
			service.Enabled = false
		}
		updateFields["enabled"] = true
	}
	if transportType, ok := updateData["transport_type"].(string); ok {
		service.TransportType = types.MCPTransportType(transportType)
	}
	if url, ok := updateData["url"].(string); ok && url != "" {
		service.URL = &url
	} else if _, exists := updateData["url"]; exists {
		// Explicitly set to nil if provided as null/empty
		service.URL = nil
	}
	if stdioConfig, ok := updateData["stdio_config"].(map[string]interface{}); ok {
		config := &types.MCPStdioConfig{}
		if command, ok := stdioConfig["command"].(string); ok {
			config.Command = command
		}
		if args, ok := stdioConfig["args"].([]interface{}); ok {
			config.Args = make([]string, len(args))
			for i, arg := range args {
				if str, ok := arg.(string); ok {
					config.Args[i] = str
				}
			}
		}
		service.StdioConfig = config
	}
	if envVars, ok := updateData["env_vars"].(map[string]interface{}); ok {
		service.EnvVars = make(types.MCPEnvVars)
		for k, v := range envVars {
			if str, ok := v.(string); ok {
				service.EnvVars[k] = str
			}
		}
	}
	if headers, ok := updateData["headers"].(map[string]interface{}); ok {
		service.Headers = make(types.MCPHeaders)
		for k, v := range headers {
			if str, ok := v.(string); ok {
				service.Headers[k] = str
			}
		}
	}
	if authConfig, ok := updateData["auth_config"].(map[string]interface{}); ok {
		service.AuthConfig = &types.MCPAuthConfig{}
		if apiKey, ok := authConfig["api_key"].(string); ok {
			service.AuthConfig.APIKey = apiKey
		}
		if token, ok := authConfig["token"].(string); ok {
			service.AuthConfig.Token = token
		}
	}
	if advancedConfig, ok := updateData["advanced_config"].(map[string]interface{}); ok {
		service.AdvancedConfig = &types.MCPAdvancedConfig{}
		if timeout, ok := advancedConfig["timeout"].(float64); ok {
			service.AdvancedConfig.Timeout = int(timeout)
		}
		if retryCount, ok := advancedConfig["retry_count"].(float64); ok {
			service.AdvancedConfig.RetryCount = int(retryCount)
		}
		if retryDelay, ok := advancedConfig["retry_delay"].(float64); ok {
			service.AdvancedConfig.RetryDelay = int(retryDelay)
		}
	}

	if err := h.mcpServiceService.UpdateMCPService(ctx, &service); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.Error(errors.NewInternalServerError("Failed to update MCP service: " + err.Error()))
		return
	}

	logger.Infof(ctx, "MCP service updated successfully: %s", secutils.SanitizeForLog(serviceID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    service,
	})
}

// DeleteMCPService godoc
// @Summary      删除MCP服务
// @Description  删除指定的MCP服务
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id} [delete]
func (h *MCPServiceHandler) DeleteMCPService(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	if err := h.mcpServiceService.DeleteMCPService(ctx, tenantID, serviceID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.Error(errors.NewInternalServerError("Failed to delete MCP service: " + err.Error()))
		return
	}

	logger.Infof(ctx, "MCP service deleted successfully: %s", secutils.SanitizeForLog(serviceID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "MCP service deleted successfully",
	})
}

// TestMCPService godoc
// @Summary      测试MCP服务连接
// @Description  测试MCP服务是否可以正常连接
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"
// @Success      200  {object}  map[string]interface{}  "测试结果"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/test [post]
func (h *MCPServiceHandler) TestMCPService(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Testing MCP service: %s", secutils.SanitizeForLog(serviceID))

	result, err := h.mcpServiceService.TestMCPService(ctx, tenantID, serviceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": types.MCPTestResult{
				Success: false,
				Message: "Test failed: " + err.Error(),
			},
		})
		return
	}

	logger.Infof(ctx, "MCP service test completed: %s, success: %v", secutils.SanitizeForLog(serviceID), result.Success)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// GetMCPServiceTools godoc
// @Summary      获取MCP服务工具列表
// @Description  获取MCP服务提供的工具列表
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"
// @Success      200  {object}  map[string]interface{}  "工具列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/tools [get]
func (h *MCPServiceHandler) GetMCPServiceTools(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	tools, err := h.mcpServiceService.GetMCPServiceTools(ctx, tenantID, serviceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.Error(errors.NewInternalServerError("Failed to get MCP service tools: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tools,
	})
}

// GetMCPServiceResources godoc
// @Summary      获取MCP服务资源列表
// @Description  获取MCP服务提供的资源列表
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"
// @Success      200  {object}  map[string]interface{}  "资源列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/resources [get]
func (h *MCPServiceHandler) GetMCPServiceResources(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	resources, err := h.mcpServiceService.GetMCPServiceResources(ctx, tenantID, serviceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.Error(errors.NewInternalServerError("Failed to get MCP service resources: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resources,
	})
}
