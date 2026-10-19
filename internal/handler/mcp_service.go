package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	mcpsecurity "github.com/Tencent/WeKnora/internal/mcp"
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
	modelService           interfaces.ModelService
	// agents resolves a shared agent so the @MCP picker can list the services
	// that agent can actually reach, which live in ITS OWNER's workspace.
	agents access.SharedAgentLookup
	// runResumer continues a run suspended on an approval once it is
	// answered. Nil leaves such answers to the reaper, which closes the run.
	runResumer RunResumer
}

// RunResumer continues an agent run that ended suspended on prompt, now
// answered by the request c. It must not block on the run.
type RunResumer interface {
	ResumeSuspendedRun(c *gin.Context, prompt *types.AgentPrompt, ownerGone bool, ownerLeaseUntil *time.Time)
}

// SetRunResumer wires the resumer of suspended runs.
func (h *MCPServiceHandler) SetRunResumer(r RunResumer) { h.runResumer = r }

// NewMCPServiceHandler creates a new MCP service handler
func NewMCPServiceHandler(
	mcpServiceService interfaces.MCPServiceService,
	mcpToolApprovalService interfaces.MCPToolApprovalService,
	toolApprovalGate *approval.Gate,
	modelService interfaces.ModelService,
	agents access.SharedAgentLookup,
) *MCPServiceHandler {
	return &MCPServiceHandler{
		mcpServiceService:      mcpServiceService,
		mcpToolApprovalService: mcpToolApprovalService,
		toolApprovalGate:       toolApprovalGate,
		modelService:           modelService,
		agents:                 agents,
	}
}

func (h *MCPServiceHandler) mcpServiceResponses(
	ctx context.Context,
	tenantID uint64,
	services []*types.MCPService,
) []*dto.MCPServiceResponse {
	resp := dto.NewMCPServiceResponses(ctx, services)
	if len(services) == 0 {
		return resp
	}
	summaries, err := h.mcpServiceService.ListMCPMetadataSummaries(ctx, tenantID, services)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": tenantID})
		return resp
	}
	dto.AttachMCPCatalogs(resp, services, summaries)
	return resp
}

// MCPServiceAuthRequest is auth_config when creating an MCP service.
type MCPServiceAuthRequest struct {
	// auth_type 留空表示无认证；bearer 发送 token，api_key 按 api_key_header 发送密钥
	AuthType string `json:"auth_type" enums:"api_key,bearer,oauth" extensions:"x-agent"`
	// api_key 认证使用的请求头，默认 X-API-Key
	APIKeyHeader  string            `json:"api_key_header" extensions:"x-agent"`
	APIKey        string            `json:"api_key"`
	Token         string            `json:"token"`
	CustomHeaders map[string]string `json:"custom_headers"`
	Scopes        []string          `json:"scopes"`
	ClientID      string            `json:"client_id"`
}

// CreateMCPServiceRequest is the body of POST /mcp-services.
type CreateMCPServiceRequest struct {
	// 服务名称，空间内唯一
	Name        string `json:"name" binding:"required" extensions:"x-agent"`
	Description string `json:"description"`
	// 告诉智能体何时、如何使用该服务
	UsageInstructions string `json:"usage_instructions" extensions:"x-agent,x-agent-max-bytes=48000"`
	Enabled           *bool  `json:"enabled"`
	// 传输方式
	TransportType string `json:"transport_type" binding:"required" enums:"sse,http-streamable" extensions:"x-agent"`
	// 服务地址（http 或 https）
	URL            string                   `json:"url" binding:"required" extensions:"x-agent"`
	Headers        map[string]string        `json:"headers"`
	AuthConfig     *MCPServiceAuthRequest   `json:"auth_config"`
	AdvancedConfig *types.MCPAdvancedConfig `json:"advanced_config"`
}

// UpdateMCPServiceRequest is the body of PUT /mcp-services/{id}. Only the
// fields present change.
type UpdateMCPServiceRequest struct {
	// 新的服务名称
	Name        *string `json:"name" extensions:"x-agent"`
	Description *string `json:"description"`
	// 新的使用说明，1 到 16000 个字符
	UsageInstructions *string `json:"usage_instructions" extensions:"x-agent,x-agent-max-bytes=48000"`
	// 启用或停用该服务
	Enabled *bool `json:"enabled" extensions:"x-agent"`
	// 新的传输方式
	TransportType *string `json:"transport_type" enums:"sse,http-streamable" extensions:"x-agent"`
	// 新的服务地址（http 或 https）
	URL            *string                      `json:"url" extensions:"x-agent"`
	Headers        map[string]string            `json:"headers"`
	AuthConfig     *MCPServiceAuthUpdateRequest `json:"auth_config"`
	AdvancedConfig *types.MCPAdvancedConfig     `json:"advanced_config"`
}

// MCPServiceAuthUpdateRequest is auth_config in an MCP service update. Secrets
// are not read here; they go through /credentials.
type MCPServiceAuthUpdateRequest struct {
	AuthType              string            `json:"auth_type" enums:"api_key,bearer,oauth"`
	APIKeyHeader          string            `json:"api_key_header"`
	CustomHeaders         map[string]string `json:"custom_headers"`
	Scopes                []string          `json:"scopes"`
	AuthServerMetadataURL string            `json:"auth_server_metadata_url"`
	ClientID              string            `json:"client_id"`
}

// CreateMCPService godoc
// @Summary      创建MCP服务
// @Description  创建新的MCP服务配置
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        request  body      CreateMCPServiceRequest  true  "MCP服务配置"
// @Success      200      {object}  handler.DataResponse{data=dto.MCPServiceResponse}  "创建的MCP服务"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.create","risk":"write","role":"admin","label_param":"name","always_confirm":true}
// @x-agent-hint "Adds a remote MCP service (SSE or HTTP Streamable) and turns it on. Take the URL from the user or the service's own documentation. It never carries a key or token: for a service that needs one, set auth_type (api_key, bearer or oauth) and afterwards give the user open_page with mcp_service_id so they enter the credential or authorize. Every agent set to use all MCP services starts calling the new URL, so this always asks the user. Then check it with mcp_service.test."
// @Router       /mcp-services [post]
func (h *MCPServiceHandler) CreateMCPService(c *gin.Context) {
	ctx := c.Request.Context()

	service := types.MCPService{Enabled: true}
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
	if err := mcpsecurity.ValidateServiceOutboundURLs(&service); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for MCP service configuration: %v", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
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
// @Description  获取当前空间的所有MCP服务（含已保存工具目录数量）
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        agent_id               query  string  false  "Agent ID; needs agent_source_tenant_id"
// @Param        agent_source_tenant_id query  int     false  "Shared agent source workspace"
// @Success      200  {object}  handler.DataResponse{data=[]dto.MCPServiceResponse}  "MCP服务列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "无权使用该共享智能体"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.list","risk":"read","max_items":200}
// @x-agent-hint "Lists the workspace's MCP services by name, with whether each is enabled. Connection details and credentials are not shown; the user manages MCP services in the settings."
// @Router       /mcp-services [get]
func (h *MCPServiceHandler) ListMCPServices(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	// A shared agent reaches its OWNER's MCP services, so the picker has to
	// list them there. Without this the picker showed the caller's own
	// services, whose ids can never match the agent's preset — the backend
	// dropped every such @mention with only a warning in the log.
	agent, err := sharedAgentPickerScope(c, h.agents)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if agent != nil {
		h.listSharedAgentMCPServices(c, agent)
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
		"data":    h.mcpServiceResponses(ctx, tenantID, services),
	})
}

// listSharedAgentMCPServices serves the @MCP picker for a borrowed agent.
//
// The set is the agent's explicit preset resolved in its owner's workspace —
// see sharedAgentMCPScope for why it is the preset and not the owner's whole
// inventory. The response uses the narrowed cross-workspace shape, and the
// tool-count summaries are read under the owner too, since that is where the
// services and their saved directories live.
func (h *MCPServiceHandler) listSharedAgentMCPServices(c *gin.Context, agent *types.CustomAgent) {
	ctx := c.Request.Context()
	ids := sharedAgentMCPScope(agent)
	if len(ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": []*dto.MCPServiceResponse{}})
		return
	}

	services, err := h.mcpServiceService.ListMCPServicesByIDs(ctx, agent.TenantID, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": agent.TenantID})
		_ = c.Error(errors.NewInternalServerError("Failed to list MCP services: " + err.Error()))
		return
	}
	enabled := make([]*types.MCPService, 0, len(services))
	for _, svc := range services {
		// Mirror registerMCPTools: a disabled service is not registered, so it
		// must not be offered either.
		if svc != nil && svc.Enabled {
			enabled = append(enabled, svc)
		}
	}

	resp := dto.NewSharedAgentMCPServiceResponses(enabled)
	if summaries, err := h.mcpServiceService.ListMCPMetadataSummaries(
		ctx, agent.TenantID, enabled,
	); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": agent.TenantID})
	} else {
		dto.AttachMCPCatalogs(resp, enabled, summaries)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// GetMCPService godoc
// @Summary      获取MCP服务详情
// @Description  根据ID获取MCP服务详情
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"  extensions(x-agent=mcp_service_id)
// @Success      200  {object}  handler.DataResponse{data=dto.MCPServiceResponse}  "MCP服务详情"
// @Failure      404  {object}  errors.AppError         "服务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.get","risk":"read"}
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
		"data":    h.mcpServiceResponses(ctx, tenantID, []*types.MCPService{service})[0],
	})
}

// UpdateMCPService godoc
// @Summary      更新MCP服务
// @Description  更新MCP服务配置
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "MCP服务ID"  extensions(x-agent=mcp_service_id)
// @Param        request  body      UpdateMCPServiceRequest  true  "更新字段"
// @Success      200      {object}  handler.DataResponse{data=dto.MCPServiceResponse}  "更新后的MCP服务"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.update","risk":"write","role":"admin","target":"mcp_service.get","label":"name"}
// @x-agent-hint "Changes the listed settings of an MCP service; a field left out is kept. Turning a service off stops every agent from using it. Changing url or transport_type drops the API key, token and custom headers saved for the old address, so give the user open_page with mcp_service_id to enter them again; a service that uses OAuth cannot be moved here. Built-in services cannot be changed."
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

	if raw, exists := updateData["usage_instructions"]; exists {
		instructions, ok := raw.(string)
		instructions = strings.TrimSpace(instructions)
		if !ok || instructions == "" || utf8.RuneCountInString(instructions) > 16000 {
			_ = c.Error(errors.NewBadRequestError("Usage instructions must contain between 1 and 16000 characters"))
			return
		}
		service.UsageInstructions = instructions
		updateFields["usage_instructions"] = true
	}

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
		// Secret fields (api_key, token, client_secret) are intentionally NOT
		// read from the main PUT body — they live behind the /credentials subresource so
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
			updateFields["auth_type"] = true
		}
		// api_key_header is non-secret structural config (header name for the
		// api_key strategy); flows through the main PUT like custom_headers.
		if apiKeyHeader, ok := authConfig["api_key_header"].(string); ok {
			service.AuthConfig.APIKeyHeader = apiKeyHeader
			updateFields["api_key_header"] = true
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
		// client_id is non-secret; present-and-empty returns the service to
		// dynamic client registration. Its secret goes through /credentials.
		if clientID, ok := authConfig["client_id"].(string); ok {
			service.AuthConfig.ClientID = strings.TrimSpace(clientID)
			updateFields["client_id"] = true
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
	if err := mcpsecurity.ValidateServiceOutboundURLs(&service); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for MCP service update: %v", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if _, fromAssistant := types.AuditAgentActionFromContext(ctx); fromAssistant {
		if err := h.guardAssistantEndpointMove(ctx, tenantID, &service, updateFields); err != nil {
			_ = c.Error(err)
			return
		}
	}

	if err := h.mcpServiceService.UpdateMCPService(ctx, &service, updateFields); err != nil {
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
		"data":    h.mcpServiceResponses(ctx, tenantID, []*types.MCPService{stored})[0],
	})
}

// guardAssistantEndpointMove keeps saved secrets with the address they were
// issued for when the assistant moves a service. The assistant never sees
// them, but a URL it was talked into would receive them on the next call, so
// the move drops the API key, token and custom headers. An OAuth service is
// refused outright: every user's sign-in would follow it to the new server.
func (h *MCPServiceHandler) guardAssistantEndpointMove(
	ctx context.Context, tenantID uint64, update *types.MCPService, updateFields map[string]bool,
) error {
	if update.URL == nil && update.TransportType == "" {
		return nil
	}
	existing, err := h.mcpServiceService.GetMCPServiceByID(ctx, tenantID, update.ID)
	if err != nil || existing == nil {
		return errors.NewNotFoundError("MCP service not found")
	}
	moved := update.TransportType != "" && update.TransportType != existing.TransportType
	if update.URL != nil && (existing.URL == nil || *existing.URL != *update.URL) {
		moved = true
	}
	if !moved {
		return nil
	}
	if existing.AuthConfig != nil && existing.AuthConfig.AuthType == types.MCPAuthOAuth {
		return errors.NewBadRequestError("This MCP service signs in with OAuth, so its address cannot be " +
			"changed for the user: everyone's sign-in would follow it. Ask the user to change it in the MCP settings.")
	}
	updateFields[types.MCPUpdateDropSecrets] = true
	return nil
}

// DeleteMCPService godoc
// @Summary      删除MCP服务
// @Description  删除指定的MCP服务
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"  extensions(x-agent=mcp_service_id)
// @Success      200  {object}  handler.MessageResponse  "删除成功"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.delete","risk":"destructive","role":"admin","target":"mcp_service.get","label":"name"}
// @x-agent-hint "Deletes an MCP service with its saved credentials; agents that used it lose its tools. Cannot be undone; built-in services cannot be deleted."
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
// @Param        id   path      string  true  "MCP服务ID"  extensions(x-agent=mcp_service_id)
// @Success      200  {object}  handler.DataResponse{data=types.MCPTestResult}  "测试结果"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.test","risk":"probe","role":"admin"}
// @x-agent-hint "Connects to an MCP service with its saved settings and reports whether it works. success false comes with the reason in message; oauth_required means the user must authorize it in the MCP settings. List its tools with mcp_service.list_tools."
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
// @Param        id   path      string  true  "MCP服务ID"  extensions(x-agent=mcp_service_id)
// @Success      200  {object}  handler.DataResponse{data=[]types.MCPTool}  "工具列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"mcp_service.list_tools","risk":"read","max_items":200}
// @x-agent-hint "Lists the tools an MCP service offers, asking the service itself without saving a directory. Prefer mcp_service.get_metadata for ordinary directory queries; use this live query only when a current upstream check without saving is needed; to synchronize it use mcp_service.refresh_metadata. Use it to check that a service can do what an agent needs before binding it. An error means the service is unreachable; mcp_service.test says why."
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
// @Success      200  {object}  handler.DataResponse{data=[]types.MCPResource}  "资源列表"
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

// ListMCPToolApprovals returns persisted per-tool policies for an MCP service.
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
	RequireApproval *bool `json:"require_approval"`
	Enabled         *bool `json:"enabled"`
}

// SetMCPToolApproval updates per-tool MCP policy fields. The route name is kept
// for backwards compatibility with the original approval-only endpoint.
//
// SetMCPToolApproval godoc
// @Summary      设置 MCP 工具策略
// @Description  为指定 MCP 服务下的某个工具更新启用状态和/或人工审批要求。至少提供 require_approval 或 enabled 之一；省略的字段保持原值。
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id         path      string                  true  "MCP 服务 ID"
// @Param        tool_name  path      string                  true  "工具名"
// @Param        request    body      map[string]interface{}  true  "{require_approval?: bool, enabled?: bool}"
// @Success      200        {object}  handler.SuccessResponse  "更新结果"
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
	if body.RequireApproval == nil && body.Enabled == nil {
		c.Error(errors.NewBadRequestError("require_approval or enabled is required"))
		return
	}
	if err := h.mcpToolApprovalService.SetPolicy(
		ctx, tenantID, serviceID, toolName, body.RequireApproval, body.Enabled,
	); err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type resolveToolApprovalBody struct {
	Decision     string          `json:"decision" binding:"required"` // approve | reject
	ModifiedArgs json.RawMessage `json:"modified_args"`
	Reason       string          `json:"reason"`
	// AllowInSession, on approve, also allows later calls of the same kind
	// in the session, when the prompt offered that.
	AllowInSession bool `json:"allow_in_session"`
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
// @Param        request     body      map[string]interface{}  true  "{decision: \"approve\"|\"reject\", reason?: string, modified_args?: object, allow_in_session?: bool}"
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
		dec.AllowInSession = body.AllowInSession
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
	// The embed route carries session_id; binding it stops an embed visitor
	// from resolving a prompt raised in another session.
	res, err := h.toolApprovalGate.ResolveForResume(ctx, approval.ResolveRequest{
		TenantID:  tenantID,
		UserID:    gateUserID,
		SessionID: c.Param("session_id"),
		Kind:      types.AgentPromptKindToolApproval,
		PendingID: pendingID,
		Decision:  dec,
	})
	h.finishResolve(c, pendingID, res, err)
}

// finishResolve maps a resolve error to its HTTP status, or resumes the
// run a suspended prompt belonged to and reports whether it did.
func (h *MCPServiceHandler) finishResolve(c *gin.Context, pendingID string, res hitl.Resolution, err error) {
	ctx := c.Request.Context()
	if err != nil {
		switch {
		case stderrors.Is(err, approval.ErrPendingNotFound):
			_ = c.Error(errors.NewNotFoundError("pending prompt not found or already completed"))
		case stderrors.Is(err, approval.ErrAlreadyResolved):
			_ = c.Error(errors.NewBadRequestError(
				"pending prompt already resolved (timeout / cancel raced your action)"))
		case stderrors.Is(err, approval.ErrTenantMismatch):
			_ = c.Error(errors.NewBadRequestError("workspace mismatch"))
		case stderrors.Is(err, approval.ErrUserMismatch):
			_ = c.Error(errors.NewBadRequestError("user mismatch: only the session owner may answer this prompt"))
		case stderrors.Is(err, approval.ErrInvalidDecision):
			_ = c.Error(errors.NewBadRequestError(err.Error()))
		default:
			logger.ErrorWithFields(ctx, err, map[string]interface{}{"pending_id": pendingID})
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	// A suspended run has ended; this replica now continues it.
	resumed := res.Resume && h.runResumer != nil
	if router, ok := h.runResumer.(interface {
		HandlesDelegatedPrompt(context.Context, *types.AgentPrompt) bool
	}); ok && router.HandlesDelegatedPrompt(ctx, res.Prompt) {
		resumed = false
	}
	if resumed {
		h.runResumer.ResumeSuspendedRun(c, res.Prompt, res.OwnerGone, res.OwnerLeaseUntil)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "resumed": resumed})
}

type resolveAgentPromptBody struct {
	Kind     string          `json:"kind" binding:"required"`
	Response json.RawMessage `json:"response" binding:"required"`
}

// ResolveAgentPrompt godoc
// @Summary      回答 Agent 的交互请求
// @Description  回答 Agent 运行中发起的交互（ask_user 提问、工具审批，或由浏览器自动应答的页面读写）。
// @Description  kind 须与交互一致，response 为该类型的回答 JSON（不接受未知字段）：
// @Description  ask_user 为 {selected?, values?, text?, declined?}；
// @Description  tool_approval 为 {approved: bool, modified_args?: object, reason?: string, allow_in_session?: bool}；
// @Description  page_action 为 {declined?, error?, location?, snapshot?, applied?: [{ref, label?, summary?}],
// @Description  stayed?, return_path?}。
// @Description  返回 resumed=true 表示运行已挂起并在本次回答后恢复，客户端需重新接入该消息的事件流
// @Tags         Agent
// @Accept       json
// @Produce      json
// @Param        prompt_id  path      string                  true  "交互 ID（事件中的 pending_id）"
// @Param request body map[string]interface{} true "{kind: \"ask_user\"|\"tool_approval\"|\"page_action\", response: object}"
// @Success      200        {object}  map[string]interface{}  "{success: true, resumed: bool}"
// @Failure      400        {object}  errors.AppError         "回答不合法或交互已结束"
// @Failure      404        {object}  errors.AppError         "交互不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agent/prompts/{prompt_id} [post]
func (h *MCPServiceHandler) ResolveAgentPrompt(c *gin.Context) {
	ctx := c.Request.Context()
	promptID := c.Param("prompt_id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		_ = c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	if h.toolApprovalGate == nil {
		_ = c.Error(errors.NewInternalServerError("Agent prompts are not configured"))
		return
	}
	var body resolveAgentPromptBody
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	principal, _ := types.PrincipalFromContext(ctx)
	userID := principal.StorageID()
	if strings.TrimSpace(userID) == "" {
		_ = c.Error(errors.NewUnauthorizedError("authenticated user required to answer a prompt"))
		return
	}
	res, err := h.toolApprovalGate.ResolveResponse(ctx, hitl.ResolveRequest{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: c.Param("session_id"),
		Kind:      body.Kind,
		PromptID:  promptID,
		Response:  body.Response,
	})
	h.finishResolve(c, promptID, res, err)
}
