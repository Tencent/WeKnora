package handler

import (
	"errors"
	"net/http"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// WithToolSessionPolicies configures the IM allowlist management endpoints.
func (h *MCPServiceHandler) WithToolSessionPolicies(service interfaces.MCPToolSessionPolicyService) *MCPServiceHandler {
	h.toolSessionPolicies = service
	return h
}

func (h *MCPServiceHandler) toolSessionPolicyTenant(c *gin.Context) (uint64, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		_ = c.Error(apperrors.NewBadRequestError("Workspace ID cannot be empty"))
		return 0, false
	}
	if h.toolSessionPolicies == nil {
		_ = c.Error(apperrors.NewInternalServerError("MCP tool session policies are not configured"))
		return 0, false
	}
	return tenantID, true
}

func toolSessionPolicyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, types.ErrInvalidMCPToolSessionPolicy):
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
	case strings.Contains(err.Error(), "not found"):
		_ = c.Error(apperrors.NewNotFoundError("MCP service not found"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"tenant_id": c.GetUint64(types.TenantIDContextKey.String()),
		})
		_ = c.Error(apperrors.NewInternalServerError("Unable to update or read MCP tool session policy"))
	}
}

// ListMCPToolSessionPolicies returns the IM restrictions for one service.
// @Summary List MCP tool IM conversation policies
// @Tags MCP服务
// @Produce json
// @Param id path string true "MCP service ID"
// @Success 200 {object} handler.SuccessResponse
// @Failure 404 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /mcp-services/{id}/tool-session-policies [get]
func (h *MCPServiceHandler) ListMCPToolSessionPolicies(c *gin.Context) {
	tenantID, ok := h.toolSessionPolicyTenant(c)
	if !ok {
		return
	}
	rows, err := h.toolSessionPolicies.ListByService(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		toolSessionPolicyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

// SetMCPToolSessionPolicy replaces a tool's IM allowlist. An empty list denies
// all IM conversations; DELETE removes the additional restriction.
// @Summary Replace an MCP tool's IM conversation allowlist
// @Tags MCP服务
// @Accept json
// @Produce json
// @Param id path string true "MCP service ID"
// @Param tool_name path string true "Tool name"
// @Param request body MCPToolSessionPolicyRequest true "Conversation allowlist; [] denies all IM conversations"
// @Success 200 {object} handler.SuccessResponse
// @Failure 400 {object} errors.AppError
// @Failure 404 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /mcp-services/{id}/tool-session-policies/{tool_name} [put]
func (h *MCPServiceHandler) SetMCPToolSessionPolicy(c *gin.Context) {
	tenantID, ok := h.toolSessionPolicyTenant(c)
	if !ok {
		return
	}
	var body MCPToolSessionPolicyRequest
	if err := c.ShouldBindJSON(&body); err != nil || body.AllowedSessions == nil {
		_ = c.Error(apperrors.NewBadRequestError("allowed_sessions must be an array"))
		return
	}
	err := h.toolSessionPolicies.SetPolicy(
		c.Request.Context(), tenantID, c.Param("id"), c.Param("tool_name"), body.AllowedSessions,
	)
	if err != nil {
		toolSessionPolicyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MCPToolSessionPolicyRequest replaces the complete allowlist; nil is invalid.
type MCPToolSessionPolicyRequest struct {
	AllowedSessions []types.MCPToolSessionRule `json:"allowed_sessions"`
}

// DeleteMCPToolSessionPolicy removes the additional IM restriction.
// @Summary Remove an MCP tool's IM conversation allowlist
// @Tags MCP服务
// @Produce json
// @Param id path string true "MCP service ID"
// @Param tool_name path string true "Tool name"
// @Success 200 {object} handler.SuccessResponse
// @Failure 404 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /mcp-services/{id}/tool-session-policies/{tool_name} [delete]
func (h *MCPServiceHandler) DeleteMCPToolSessionPolicy(c *gin.Context) {
	tenantID, ok := h.toolSessionPolicyTenant(c)
	if !ok {
		return
	}
	err := h.toolSessionPolicies.DeletePolicy(c.Request.Context(), tenantID, c.Param("id"), c.Param("tool_name"))
	if err != nil {
		toolSessionPolicyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
