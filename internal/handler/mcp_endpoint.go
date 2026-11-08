package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPEndpointHandler manages the workspace's published MCP server endpoints.
type MCPEndpointHandler struct {
	svc interfaces.MCPEndpointService
}

// NewMCPEndpointHandler creates the MCP endpoint management handler.
func NewMCPEndpointHandler(svc interfaces.MCPEndpointService) *MCPEndpointHandler {
	return &MCPEndpointHandler{svc: svc}
}

type mcpEndpointRequest struct {
	Name               *string   `json:"name"`
	Description        *string   `json:"description"`
	Enabled            *bool     `json:"enabled"`
	KnowledgeBaseIDs   *[]string `json:"knowledge_base_ids"`
	Tools              *[]string `json:"tools"`
	DefaultAgentID     *string   `json:"default_agent_id"`
	RateLimitPerMinute *int      `json:"rate_limit_per_minute"`
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ListToolCatalog returns the tools an endpoint can expose, grouped.
//
// @Summary      获取 MCP 端点工具目录
// @Description  返回工作空间 MCP 端点可勾选的工具清单、分组和默认勾选项
// @Tags         MCP端点
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "工具目录"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/tools [get]
func (h *MCPEndpointHandler) ListToolCatalog(c *gin.Context) {
	groups := types.MCPEndpointToolGroups()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"groups":        groups,
			"tools":         types.MCPEndpointToolCatalog(),
			"default_tools": types.DefaultMCPEndpointTools(),
		},
	})
}

// CreateMCPEndpoint creates an endpoint and returns it with the one-time token.
//
// @Summary      创建 MCP 端点
// @Description  为当前工作空间发布一个 MCP 端点，响应中的 token 只返回一次
// @Tags         MCP端点
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "端点配置：name、description、enabled、knowledge_base_ids、tools 等"
// @Success      201      {object}  map[string]interface{}  "创建的端点，含一次性 token"
// @Failure      400      {object}  map[string]interface{}  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints [post]
func (h *MCPEndpointHandler) CreateMCPEndpoint(c *gin.Context) {
	h.create(c, spaceMCPEndpointOwner(c))
}

func (h *MCPEndpointHandler) create(c *gin.Context, owner types.MCPEndpointOwner) {
	var req mcpEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	tools := types.DefaultMCPEndpointTools()
	if req.Tools != nil {
		tools = *req.Tools
	}
	var kbIDs []string
	if req.KnowledgeBaseIDs != nil {
		kbIDs = *req.KnowledgeBaseIDs
	}
	rate := 0
	if req.RateLimitPerMinute != nil {
		rate = *req.RateLimitPerMinute
	}
	ep, token, err := h.svc.Create(c.Request.Context(), owner, &types.MCPEndpoint{
		Name:               stringValue(req.Name),
		Description:        stringValue(req.Description),
		Enabled:            enabled,
		KnowledgeBaseIDs:   types.StringArray(kbIDs),
		Tools:              types.StringArray(tools),
		DefaultAgentID:     stringValue(req.DefaultAgentID),
		RateLimitPerMinute: rate,
	})
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": mcpEndpointResponse(ep, token)})
}

// ListMCPEndpoints lists the workspace endpoints without tokens.
//
// @Summary      获取 MCP 端点列表
// @Tags         MCP端点
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "端点列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints [get]
func (h *MCPEndpointHandler) ListMCPEndpoints(c *gin.Context) {
	h.list(c, spaceMCPEndpointOwner(c))
}

func (h *MCPEndpointHandler) list(c *gin.Context, owner types.MCPEndpointOwner) {
	rows, err := h.svc.List(c.Request.Context(), owner)
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, ep := range rows {
		out = append(out, mcpEndpointResponse(ep, ""))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// GetMCPEndpoint returns one endpoint.
//
// @Summary      获取 MCP 端点详情
// @Tags         MCP端点
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Success      200          {object}  map[string]interface{}  "端点详情"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/{endpoint_id} [get]
func (h *MCPEndpointHandler) GetMCPEndpoint(c *gin.Context) {
	h.get(c, spaceMCPEndpointOwner(c))
}

func (h *MCPEndpointHandler) get(c *gin.Context, owner types.MCPEndpointOwner) {
	id := secutils.SanitizeForLog(c.Param("endpoint_id"))
	ep, err := h.svc.Get(c.Request.Context(), owner, id)
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mcpEndpointResponse(ep, "")})
}

// UpdateMCPEndpoint applies a partial update; omitted fields are kept.
//
// @Summary      更新 MCP 端点
// @Tags         MCP端点
// @Accept       json
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Param        request      body      object  true  "要更新的字段，未提供的字段保持不变"
// @Success      200          {object}  map[string]interface{}  "更新后的端点"
// @Failure      400          {object}  map[string]interface{}  "请求参数错误"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/{endpoint_id} [put]
func (h *MCPEndpointHandler) UpdateMCPEndpoint(c *gin.Context) {
	h.update(c, spaceMCPEndpointOwner(c))
}

func (h *MCPEndpointHandler) update(c *gin.Context, owner types.MCPEndpointOwner) {
	id := secutils.SanitizeForLog(c.Param("endpoint_id"))
	var req mcpEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ep, err := h.svc.Update(c.Request.Context(), owner, id, interfaces.MCPEndpointUpdate{
		Name:               req.Name,
		Description:        req.Description,
		Enabled:            req.Enabled,
		KnowledgeBaseIDs:   req.KnowledgeBaseIDs,
		Tools:              req.Tools,
		DefaultAgentID:     req.DefaultAgentID,
		RateLimitPerMinute: req.RateLimitPerMinute,
	})
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mcpEndpointResponse(ep, "")})
}

// DeleteMCPEndpoint soft-deletes an endpoint; connected clients lose access.
//
// @Summary      删除 MCP 端点
// @Tags         MCP端点
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Success      200          {object}  handler.SuccessResponse  "删除成功"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/{endpoint_id} [delete]
func (h *MCPEndpointHandler) DeleteMCPEndpoint(c *gin.Context) {
	h.delete(c, spaceMCPEndpointOwner(c))
}

func (h *MCPEndpointHandler) delete(c *gin.Context, owner types.MCPEndpointOwner) {
	id := secutils.SanitizeForLog(c.Param("endpoint_id"))
	if err := h.svc.Delete(c.Request.Context(), owner, id); err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RotateMCPEndpointToken issues a fresh token and returns it once.
//
// @Summary      轮换 MCP 端点令牌
// @Description  生成新令牌并立即作废旧令牌，响应中的 token 只返回一次
// @Tags         MCP端点
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Success      200          {object}  map[string]interface{}  "含新 token 的端点"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/{endpoint_id}/rotate-token [post]
func (h *MCPEndpointHandler) RotateMCPEndpointToken(c *gin.Context) {
	h.rotateToken(c, spaceMCPEndpointOwner(c))
}

func (h *MCPEndpointHandler) rotateToken(c *gin.Context, owner types.MCPEndpointOwner) {
	id := secutils.SanitizeForLog(c.Param("endpoint_id"))
	ep, token, err := h.svc.RotateToken(c.Request.Context(), owner, id)
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mcpEndpointResponse(ep, token)})
}

// ListPersonalMCPEndpoints lists the caller's own personal endpoints.
//
// @Summary      获取个人 MCP 端点列表
// @Description  只返回当前成员自己创建的个人端点
// @Tags         MCP端点
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "端点列表"
// @Security     Bearer
// @Router       /mcp-personal-endpoints [get]
func (h *MCPEndpointHandler) ListPersonalMCPEndpoints(c *gin.Context) {
	h.list(c, personalMCPEndpointOwner(c))
}

// CreatePersonalMCPEndpoint creates a personal endpoint that acts as the caller.
//
// @Summary      创建个人 MCP 端点
// @Description  端点以当前成员身份调用，能力不超过该成员的角色；响应中的 token 只返回一次
// @Tags         MCP端点
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "端点配置：name、description、enabled、knowledge_base_ids、tools 等"
// @Success      201      {object}  map[string]interface{}  "创建的端点，含一次性 token"
// @Failure      400      {object}  map[string]interface{}  "请求参数错误"
// @Failure      403      {object}  map[string]interface{}  "角色不允许所选工具"
// @Security     Bearer
// @Router       /mcp-personal-endpoints [post]
func (h *MCPEndpointHandler) CreatePersonalMCPEndpoint(c *gin.Context) {
	h.create(c, personalMCPEndpointOwner(c))
}

// GetPersonalMCPEndpoint returns one of the caller's personal endpoints.
//
// @Summary      获取个人 MCP 端点详情
// @Tags         MCP端点
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Success      200          {object}  map[string]interface{}  "端点详情"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Router       /mcp-personal-endpoints/{endpoint_id} [get]
func (h *MCPEndpointHandler) GetPersonalMCPEndpoint(c *gin.Context) {
	h.get(c, personalMCPEndpointOwner(c))
}

// UpdatePersonalMCPEndpoint applies a partial update to a personal endpoint.
//
// @Summary      更新个人 MCP 端点
// @Description  rate_limit_per_minute 对个人端点无效，个人端点共用管理员设置的空间额度
// @Tags         MCP端点
// @Accept       json
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Param        request      body      object  true  "要更新的字段，未提供的字段保持不变"
// @Success      200          {object}  map[string]interface{}  "更新后的端点"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Router       /mcp-personal-endpoints/{endpoint_id} [put]
func (h *MCPEndpointHandler) UpdatePersonalMCPEndpoint(c *gin.Context) {
	h.update(c, personalMCPEndpointOwner(c))
}

// DeletePersonalMCPEndpoint soft-deletes one of the caller's personal endpoints.
//
// @Summary      删除个人 MCP 端点
// @Tags         MCP端点
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Success      200          {object}  handler.SuccessResponse  "删除成功"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Router       /mcp-personal-endpoints/{endpoint_id} [delete]
func (h *MCPEndpointHandler) DeletePersonalMCPEndpoint(c *gin.Context) {
	h.delete(c, personalMCPEndpointOwner(c))
}

// RotatePersonalMCPEndpointToken issues a fresh token for a personal endpoint.
//
// @Summary      轮换个人 MCP 端点令牌
// @Tags         MCP端点
// @Produce      json
// @Param        endpoint_id  path      string  true  "端点 ID"
// @Success      200          {object}  map[string]interface{}  "含新 token 的端点"
// @Failure      404          {object}  map[string]interface{}  "端点不存在"
// @Security     Bearer
// @Router       /mcp-personal-endpoints/{endpoint_id}/rotate-token [post]
func (h *MCPEndpointHandler) RotatePersonalMCPEndpointToken(c *gin.Context) {
	h.rotateToken(c, personalMCPEndpointOwner(c))
}

// GetMCPEndpointSettings returns the workspace MCP endpoint settings.
//
// @Summary      获取 MCP 端点空间设置
// @Description  返回个人端点共用的每分钟调用次数上限
// @Tags         MCP端点
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "空间设置"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/settings [get]
func (h *MCPEndpointHandler) GetMCPEndpointSettings(c *gin.Context) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	settings, err := h.svc.GetSettings(c.Request.Context(), tenantID)
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mcpEndpointSettingsResponse(settings)})
}

// UpdateMCPEndpointSettings changes the workspace MCP endpoint settings.
//
// @Summary      更新 MCP 端点空间设置
// @Description  设置空间内所有个人端点合计的每分钟调用次数上限
// @Tags         MCP端点
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "personal_rate_limit_per_minute"
// @Success      200      {object}  map[string]interface{}  "更新后的空间设置"
// @Failure      400      {object}  map[string]interface{}  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-endpoints/settings [put]
func (h *MCPEndpointHandler) UpdateMCPEndpointSettings(c *gin.Context) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	var req struct {
		PersonalRateLimitPerMinute *int `json:"personal_rate_limit_per_minute"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.PersonalRateLimitPerMinute == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "personal_rate_limit_per_minute is required"})
		return
	}
	settings, err := h.svc.UpdateSettings(c.Request.Context(), tenantID, *req.PersonalRateLimitPerMinute)
	if err != nil {
		writeMCPEndpointMgmtError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mcpEndpointSettingsResponse(settings)})
}

func spaceMCPEndpointOwner(c *gin.Context) types.MCPEndpointOwner {
	return types.SpaceMCPEndpointOwner(c.GetUint64(types.TenantIDContextKey.String()))
}

func personalMCPEndpointOwner(c *gin.Context) types.MCPEndpointOwner {
	return types.PersonalMCPEndpointOwner(
		c.GetUint64(types.TenantIDContextKey.String()),
		c.GetString(types.UserIDContextKey.String()),
	)
}

func mcpEndpointSettingsResponse(settings *types.MCPEndpointSettings) gin.H {
	return gin.H{"personal_rate_limit_per_minute": settings.PersonalRateLimitPerMinute}
}

// MCPEndpointPath is the public path prefix the MCP server is mounted on.
const MCPEndpointPath = "/mcp/"

func mcpEndpointResponse(ep *types.MCPEndpoint, token string) gin.H {
	row := gin.H{
		"id":                    ep.ID,
		"tenant_id":             ep.TenantID,
		"kind":                  ep.Kind,
		"name":                  ep.Name,
		"description":           ep.Description,
		"enabled":               ep.Enabled,
		"token_hint":            ep.TokenHint,
		"knowledge_base_ids":    []string(ep.KnowledgeBaseIDs),
		"tools":                 []string(ep.Tools),
		"default_agent_id":      ep.DefaultAgentID,
		"rate_limit_per_minute": ep.RateLimitPerMinute,
		"path":                  MCPEndpointPath + ep.ID,
		"last_used_at":          ep.LastUsedAt,
		"created_at":            ep.CreatedAt,
		"updated_at":            ep.UpdatedAt,
	}
	if strings.TrimSpace(token) != "" {
		row["token"] = token
	}
	return row
}

func writeMCPEndpointMgmtError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrMCPEndpointNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "mcp endpoint not found"})
	default:
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			switch appErr.Code {
			case apperrors.ErrNotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": appErr.Message})
				return
			case apperrors.ErrForbidden:
				c.JSON(http.StatusForbidden, gin.H{"error": appErr.Message})
				return
			case apperrors.ErrBadRequest, apperrors.ErrValidation:
				c.JSON(http.StatusBadRequest, gin.H{"error": appErr.Message})
				return
			}
		}
		logger.Error(c.Request.Context(), "mcp endpoint management failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
	}
}
