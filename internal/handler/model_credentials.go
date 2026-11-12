package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// ModelCredentialsHandler handles secret credentials for models via the
// dedicated /models/:id/credentials subresource. See mcp_credentials.go for
// the rationale; this handler mirrors that contract for Model resources.
//
// Recognized fields: "api_key" (every provider), "app_secret" (WeKnora Cloud).
type ModelCredentialsHandler struct {
	svc interfaces.ModelService
}

func NewModelCredentialsHandler(svc interfaces.ModelService) *ModelCredentialsHandler {
	return &ModelCredentialsHandler{svc: svc}
}

type modelCredentialsPutRequest struct {
	APIKey    *string `json:"api_key,omitempty"`
	AppSecret *string `json:"app_secret,omitempty"`
}

// Put godoc
// @Summary      设置模型凭据
// @Description  写入 api_key 或 app_secret；省略的字段保留原值
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "模型 ID"
// @Param        request  body      map[string]interface{}  true  "{api_key?, app_secret?: string}"
// @Success      200      {object}  handler.DataResponse{data=dto.CredentialsResponse}  "写入后的凭据状态"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      404      {object}  errors.AppError         "模型不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/{id}/credentials [put]
func (h *ModelCredentialsHandler) Put(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	var req modelCredentialsPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if req.APIKey == nil && req.AppSecret == nil {
		m, err := h.svc.GetModelByID(ctx, id)
		if err != nil || m == nil {
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": dto.CredentialsResponse{
			Fields: map[string]dto.CredentialFieldMetadata{
				"api_key":    {Configured: m.Parameters.APIKey != ""},
				"app_secret": {Configured: m.Parameters.AppSecret != ""},
			},
		}})
		return
	}

	updated, err := h.svc.UpdateModelCredentials(ctx, id, req.APIKey, req.AppSecret)
	if err != nil {
		if err == service.ErrModelNotFound {
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model_id": secutils.SanitizeForLog(id)})
		c.Error(errors.NewInternalServerError("failed to update credentials: " + err.Error()))
		return
	}

	resp := dto.CredentialsResponse{
		Fields: map[string]dto.CredentialFieldMetadata{
			"api_key":    {Configured: updated.Parameters.APIKey != ""},
			"app_secret": {Configured: updated.Parameters.AppSecret != ""},
		},
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// DeleteField godoc
// @Summary      移除模型的单个凭据字段
// @Description  删除指定字段的存储凭据；删除已为空的字段是幂等的
// @Tags         模型管理
// @Produce      json
// @Param        id     path      string  true  "模型 ID"
// @Param        field  path      string  true  "字段名（api_key | app_secret）"
// @Success      204
// @Failure      400  {object}  errors.AppError  "字段名非法"
// @Failure      404  {object}  errors.AppError  "模型不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/{id}/credentials/{field} [delete]
func (h *ModelCredentialsHandler) DeleteField(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	field := c.Param("field")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	if field != "api_key" && field != "app_secret" {
		c.Error(errors.NewBadRequestError("unknown credential field: " + secutils.SanitizeForLog(field)))
		return
	}
	if err := h.svc.ClearModelCredential(ctx, id, field); err != nil {
		if err == service.ErrModelNotFound {
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": secutils.SanitizeForLog(id),
			"field":    field,
		})
		c.Error(errors.NewInternalServerError("failed to clear credential: " + err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
