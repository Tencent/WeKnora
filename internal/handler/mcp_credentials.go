package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPCredentialsHandler handles secret credentials for MCP services via a
// dedicated subresource (/mcp-services/{id}/credentials). Splitting this out
// of UpdateMCPService delivers three concrete benefits:
//
//  1. The main MCP PUT body never carries secrets — eliminating the
//     "masked-value round-trip overwrites stored secret" class of bug at the
//     contract level rather than via runtime preserve-on-redacted defenses.
//
//  2. Saving the MCP edit dialog (changing timeout / enabled / etc.) cannot
//     accidentally invalidate or clobber a working credential. Credential
//     operations are explicit and atomic.
//
//  3. The "is this configured?" metadata travels on the main resource
//     response (MCPServiceResponse.Credentials) — no separate GET endpoint
//     needed. Only PUT and DELETE live here.
type MCPCredentialsHandler struct {
	svc interfaces.MCPServiceService
}

// NewMCPCredentialsHandler constructs the handler.
func NewMCPCredentialsHandler(svc interfaces.MCPServiceService) *MCPCredentialsHandler {
	return &MCPCredentialsHandler{svc: svc}
}

// mcpCredentialsPutRequest is the body shape for PUT /credentials. Both
// fields are pointers so the handler can distinguish "absent" (preserve)
// from "present and empty" (treat as no-op; clients should call DELETE to
// remove a credential). Non-empty values replace the stored secret.
type mcpCredentialsPutRequest struct {
	APIKey *string `json:"api_key,omitempty"`
	Token  *string `json:"token,omitempty"`
}

// Put writes (creates or replaces) one or more credential fields on the MCP
// service. Triggers a connection recycle so the next upstream call uses the
// new credential.
//
// Put godoc
// @Summary      设置 MCP 服务凭据
// @Description  为指定字段写入新凭据；省略的字段保留原值；空字符串视为 no-op（如需删除请用 DELETE）
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "MCP 服务 ID"
// @Param        request  body      map[string]interface{}  true  "{api_key?: string, token?: string}"
// @Success      200      {object}  map[string]interface{}  "写入后的凭据状态"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      404      {object}  errors.AppError         "服务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/credentials [put]
func (h *MCPCredentialsHandler) Put(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	var req mcpCredentialsPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Nothing to do — but rather than 400 (benign no-op), look up current
	// state and return it. Client treats this identically to a real save.
	if req.APIKey == nil && req.Token == nil {
		svc, err := h.svc.GetMCPServiceByID(ctx, tenantID, serviceID)
		if err != nil || svc == nil {
			c.Error(errors.NewNotFoundError("MCP service not found"))
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": dto.CredentialsResponse{
			Fields: map[string]dto.CredentialFieldMetadata{
				"api_key": {Configured: svc.AuthConfig != nil && svc.AuthConfig.APIKey != ""},
				"token":   {Configured: svc.AuthConfig != nil && svc.AuthConfig.Token != ""},
			},
		}})
		return
	}

	updated, err := h.svc.UpdateMCPCredentials(ctx, tenantID, serviceID, req.APIKey, req.Token)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"service_id": secutils.SanitizeForLog(serviceID),
		})
		c.Error(errors.NewInternalServerError("failed to update credentials: " + err.Error()))
		return
	}

	resp := dto.CredentialsResponse{
		Fields: map[string]dto.CredentialFieldMetadata{
			"api_key": {Configured: updated.AuthConfig != nil && updated.AuthConfig.APIKey != ""},
			"token":   {Configured: updated.AuthConfig != nil && updated.AuthConfig.Token != ""},
		},
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// DeleteField removes one credential field. Recognized fields: "api_key",
// "token". Returns 204 on success (even if the field was already empty).
//
// DeleteField godoc
// @Summary      移除 MCP 服务的单个凭据字段
// @Description  删除指定字段的存储凭据；删除已为空的字段是幂等的
// @Tags         MCP服务
// @Produce      json
// @Param        id     path      string  true  "MCP 服务 ID"
// @Param        field  path      string  true  "字段名（api_key | token）"
// @Success      204
// @Failure      400  {object}  errors.AppError  "字段名非法"
// @Failure      404  {object}  errors.AppError  "服务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-services/{id}/credentials/{field} [delete]
func (h *MCPCredentialsHandler) DeleteField(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := c.Param("id")
	field := c.Param("field")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}
	if field != "api_key" && field != "token" {
		c.Error(errors.NewBadRequestError("unknown credential field: " + secutils.SanitizeForLog(field)))
		return
	}

	if err := h.svc.ClearMCPCredential(ctx, tenantID, serviceID, field); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"service_id": secutils.SanitizeForLog(serviceID),
			"field":      field,
		})
		c.Error(errors.NewInternalServerError("failed to clear credential: " + err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
