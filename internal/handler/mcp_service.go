package handler

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPServiceHandler handles MCP service related HTTP requests
type MCPServiceHandler struct {
	mcpServiceService      interfaces.MCPServiceService
	mcpToolApprovalService interfaces.MCPToolApprovalService
	toolApprovalGate       *approval.Gate
}

// NewMCPServiceHandler creates a new MCP service handler
func NewMCPServiceHandler(
	mcpServiceService interfaces.MCPServiceService,
	mcpToolApprovalService interfaces.MCPToolApprovalService,
	toolApprovalGate *approval.Gate,
) *MCPServiceHandler {
	return &MCPServiceHandler{
		mcpServiceService:      mcpServiceService,
		mcpToolApprovalService: mcpToolApprovalService,
		toolApprovalGate:       toolApprovalGate,
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	service.TenantID = tenantID

	// SSRF validation for MCP service URL
	if service.URL != nil && *service.URL != "" {
		if err := secutils.ValidateURLForSSRF(*service.URL); err != nil {
			logger.Warnf(ctx, "SSRF validation failed for MCP service URL: %v", err)
			c.Error(errors.NewBadRequestError(secutils.FormatSSRFError("MCP service URL", *service.URL, err)))
			return
		}
	}

	if err := h.mcpServiceService.CreateMCPService(ctx, &service); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_name": secutils.SanitizeForLog(service.Name)})
		c.Error(errors.NewInternalServerError("Failed to create MCP service: " + err.Error()))
		return
	}

	// Response uses dto.MCPServiceResponse which omits secret fields by
	// construction — no runtime redaction needed.
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewMCPServiceResponse(ctx, &service),
	})
}

// ListMCPServices godoc
// @Summary      获取MCP服务列表
// @Description  获取当前空间的所有MCP服务
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
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
		"data":    dto.NewMCPServiceResponses(ctx, services),
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	service, err := h.mcpServiceService.GetMCPServiceByID(ctx, tenantID, serviceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.Error(errors.NewNotFoundError("MCP service not found"))
		return
	}

	// dto.NewMCPServiceResponse omits secret fields and additionally strips
	// transport details (URL/Headers/EnvVars/StdioConfig) for builtin services
	// so the cross-tenant builtin list does not leak per-tenant config.
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewMCPServiceResponse(ctx, service),
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
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

	// SSRF validation for updated MCP service URL
	if service.URL != nil && *service.URL != "" {
		if err := secutils.ValidateURLForSSRF(*service.URL); err != nil {
			logger.Warnf(ctx, "SSRF validation failed for MCP service URL: %v", err)
			c.Error(errors.NewBadRequestError(secutils.FormatSSRFError("MCP service URL", *service.URL, err)))
			return
		}
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
		// Secret fields (api_key, token) are intentionally NOT read from the
		// main PUT body — they live behind the /credentials subresource so
		// editing unrelated config (timeout, enabled, etc.) cannot
		// accidentally clobber a stored credential. Log a warning when a
		// client still tries to send them so we can spot stale callers.
		if _, present := authConfig["api_key"]; present {
			logger.Warnf(ctx,
				"deprecated: api_key in PUT /mcp-services/%s body is ignored; use PUT /credentials instead",
				secutils.SanitizeForLog(serviceID))
		}
		if _, present := authConfig["token"]; present {
			logger.Warnf(ctx,
				"deprecated: token in PUT /mcp-services/%s body is ignored; use PUT /credentials instead",
				secutils.SanitizeForLog(serviceID))
		}
		// CustomHeaders is structural (not a secret) — keep accepting it here.
		// nil preserves existing, non-nil replaces; the service layer treats a
		// nil CustomHeaders as "no change".
		if customHeaders, ok := authConfig["custom_headers"].(map[string]interface{}); ok {
			headers := make(map[string]string, len(customHeaders))
			for k, v := range customHeaders {
				if s, ok := v.(string); ok {
					headers[k] = s
				}
			}
			service.AuthConfig.CustomHeaders = headers
		}
		// auth_type and scopes are non-secret OAuth configuration; allow them
		// through the main PUT so a service can be switched to/from OAuth.
		if authType, ok := authConfig["auth_type"].(string); ok {
			service.AuthConfig.AuthType = types.MCPAuthType(authType)
		}
		// api_key_header is non-secret structural config (header name for the
		// api_key strategy); flows through the main PUT like custom_headers.
		if apiKeyHeader, ok := authConfig["api_key_header"].(string); ok {
			service.AuthConfig.APIKeyHeader = apiKeyHeader
		}
		if scopes, ok := authConfig["scopes"].([]interface{}); ok {
			list := make([]string, 0, len(scopes))
			for _, s := range scopes {
				if str, ok := s.(string); ok {
					list = append(list, str)
				}
			}
			service.AuthConfig.Scopes = list
		}
		if metaURL, ok := authConfig["auth_server_metadata_url"].(string); ok {
			service.AuthConfig.AuthServerMetadataURL = metaURL
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

	// Re-fetch to pick up server-side merges (CustomHeaders preserve, etc.)
	// and respond with the full current state via the secret-free DTO.
	stored, err := h.mcpServiceService.GetMCPServiceByID(ctx, tenantID, serviceID)
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to fetch updated MCP service: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewMCPServiceResponse(ctx, stored),
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
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

// ListMCPToolApprovals returns persisted require_approval flags for tools on an MCP service.
func (h *MCPServiceHandler) ListMCPToolApprovals(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	if h.mcpToolApprovalService == nil {
		c.Error(errors.NewInternalServerError("MCP tool approval is not configured"))
		return
	}
	rows, err := h.mcpToolApprovalService.ListByService(ctx, tenantID, serviceID)
	if err != nil {
		// Distinguish "service not found" from internal errors so the client
		// gets an accurate status code instead of an opaque 404.
		if strings.Contains(err.Error(), "not found") {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": serviceID})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

type setMCPToolApprovalBody struct {
	RequireApproval bool `json:"require_approval"`
}

// SetMCPToolApproval sets whether a tool requires human approval before the agent may call it.
//
// SetMCPToolApproval godoc
// @Summary      设置 MCP 工具人工审批策略
// @Description  为指定 MCP 服务下的某个工具设置/更新审批要求
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id         path      string                  true  "MCP 服务 ID"
// @Param        tool_name  path      string                  true  "工具名"
// @Param        request    body      map[string]interface{}  true  "{require_approval: bool}"
// @Success      200        {object}  map[string]interface{}  "更新结果"
// @Failure      400        {object}  errors.AppError         "请求参数错误"
// @Failure      404        {object}  errors.AppError         "MCP 服务或工具不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/tool-approvals/{tool_name} [put]
func (h *MCPServiceHandler) SetMCPToolApproval(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))
	// Gin already URL-decodes path params; do not call url.PathUnescape again
	// or names containing literal "%" become corrupted.
	toolName := c.Param("tool_name")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	if h.mcpToolApprovalService == nil {
		c.Error(errors.NewInternalServerError("MCP tool approval is not configured"))
		return
	}
	var body setMCPToolApprovalBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := h.mcpToolApprovalService.SetRequireApproval(ctx, tenantID, serviceID, toolName, body.RequireApproval); err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type resolveToolApprovalBody struct {
	Decision     string          `json:"decision" binding:"required"` // approve | reject
	ModifiedArgs json.RawMessage `json:"modified_args"`
	Reason       string          `json:"reason"`
}

// ResolveToolApproval completes a pending MCP tool approval (agent execution resumes).
//
// ResolveToolApproval godoc
// @Summary      处理 MCP 工具调用待审批请求
// @Description  用户审批通过或驳回一次工具调用（用于 Agent 阻塞等待审批的场景）
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        pending_id  path      string                  true  "待审批记录 ID"
// @Param        request     body      map[string]interface{}  true  "{decision: \"approve\"|\"reject\", reason?: string, modified_args?: object}"
// @Success      200         {object}  map[string]interface{}  "审批结果"
// @Failure      400         {object}  errors.AppError         "请求参数错误"
// @Failure      404         {object}  errors.AppError         "待审批记录不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agent/tool-approvals/{pending_id} [post]
func (h *MCPServiceHandler) ResolveToolApproval(c *gin.Context) {
	ctx := c.Request.Context()
	pendingID := c.Param("pending_id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	if h.toolApprovalGate == nil {
		c.Error(errors.NewInternalServerError("Tool approval gate is not configured"))
		return
	}
	var body resolveToolApprovalBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	dec := approval.Decision{Reason: body.Reason}
	switch body.Decision {
	case "approve":
		dec.Approved = true
		// Reject "null" / non-object payloads up front. Without this, "null"
		// (4 bytes) passes the len>0 check and the downstream tool sees a nil
		// argument map, silently losing the original args.
		trimmed := strings.TrimSpace(string(body.ModifiedArgs))
		if len(trimmed) > 0 && trimmed != "null" {
			var probe map[string]interface{}
			if err := json.Unmarshal(body.ModifiedArgs, &probe); err != nil || probe == nil {
				c.Error(errors.NewBadRequestError("modified_args must be a non-null JSON object"))
				return
			}
			dec.ModifiedArgs = body.ModifiedArgs
		}
	case "reject":
		dec.Approved = false
	default:
		c.Error(errors.NewBadRequestError("decision must be approve or reject"))
		return
	}
	principal, _ := types.PrincipalFromContext(ctx)
	gateUserID := principal.StorageID()
	// Reject calls without an authenticated principal up front. The gate's
	// per-principal authorization is fail-close, but surfacing 401 here gives
	// a clearer signal that auth middleware did not populate the context.
	if strings.TrimSpace(gateUserID) == "" {
		c.Error(errors.NewUnauthorizedError("authenticated user required to resolve tool approval"))
		return
	}
	if err := h.toolApprovalGate.Resolve(tenantID, gateUserID, pendingID, dec); err != nil {
		switch {
		case stderrors.Is(err, approval.ErrPendingNotFound):
			c.Error(errors.NewNotFoundError("pending approval not found or already completed"))
		case stderrors.Is(err, approval.ErrAlreadyResolved):
			c.Error(errors.NewBadRequestError("pending approval already resolved (timeout / cancel raced your action)"))
		case stderrors.Is(err, approval.ErrTenantMismatch):
			c.Error(errors.NewBadRequestError("workspace mismatch"))
		case stderrors.Is(err, approval.ErrUserMismatch):
			c.Error(errors.NewBadRequestError("user mismatch: only the session owner may resolve this approval"))
		default:
			logger.ErrorWithFields(ctx, err, map[string]interface{}{"pending_id": pendingID})
			c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
