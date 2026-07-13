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

// DataSourceCredentialsHandler handles credentials for data source
// connectors via the dedicated /credentials subresource.
//
// Unlike the other three resources (MCP / Model / WebSearch), DataSource
// credentials are a per-connector atomic map — there's no individual-field
// PUT or DELETE because half-configured connector auth doesn't work. So we
// expose a single logical field "credentials": GET returns whether anything
// is stored, PUT replaces the whole map, DELETE wipes it.
type DataSourceCredentialsHandler struct {
	service   interfaces.DataSourceService
	kbService interfaces.KnowledgeBaseService
}

func NewDataSourceCredentialsHandler(
	service interfaces.DataSourceService,
	kbService interfaces.KnowledgeBaseService,
) *DataSourceCredentialsHandler {
	return &DataSourceCredentialsHandler{service: service, kbService: kbService}
}

// ownDataSource is the same tenant-isolation check used in datasource.go,
// duplicated here to avoid coupling the two handlers via internal helpers.
func (h *DataSourceCredentialsHandler) ownDataSource(c *gin.Context) (*types.DataSource, bool) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return nil, false
	}
	id := c.Param("id")
	ds, err := h.service.GetDataSource(ctx, id)
	if err != nil || ds == nil {
		c.Error(errors.NewNotFoundError("data source not found"))
		return nil, false
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID)
	if err != nil || kb == nil || kb.TenantID != tenantID {
		c.Error(errors.NewNotFoundError("data source not found"))
		return nil, false
	}
	return ds, true
}

type dataSourceCredentialsPutRequest struct {
	Credentials map[string]interface{} `json:"credentials" binding:"required"`
}

// Put writes (creates or replaces) the credentials map for a data source.
//
// Put godoc
// @Summary      设置数据源凭证字段
// @Description  覆写数据源的全部凭证；必须提供非空 credentials map（如需清空请用 DELETE /credentials/credentials）
// @Tags         DataSource
// @Accept       json
// @Produce      json
// @Param        id       path      string                  true  "数据源 ID"
// @Param        request  body      map[string]interface{}  true  "{field, value}"
// @Success      200      {object}  map[string]interface{}  "成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /datasource/{id}/credentials [put]
func (h *DataSourceCredentialsHandler) Put(c *gin.Context) {
	ds, ok := h.ownDataSource(c)
	if !ok {
		return
	}
	var req dataSourceCredentialsPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if len(req.Credentials) == 0 {
		c.Error(errors.NewBadRequestError(
			"credentials map must be non-empty; to remove credentials use DELETE /credentials/credentials"))
		return
	}
	updated, err := h.service.UpdateDataSourceCredentials(c.Request.Context(), ds.ID, req.Credentials)
	if err != nil {
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"data_source_id": secutils.SanitizeForLog(ds.ID),
		})
		c.Error(errors.NewBadRequestError("failed to update credentials: " + err.Error()))
		return
	}
	configured := false
	if parsed, err := updated.ParseConfig(); err == nil && parsed != nil {
		configured = parsed.HasConfiguredCredentials(updated.Type)
	}
	resp := dto.CredentialsResponse{
		Fields: map[string]dto.CredentialFieldMetadata{
			"credentials": {Configured: configured},
		},
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// DeleteField removes the credentials field from a data source.
// Recognized field: "credentials". Returns 204 on success (idempotent).
//
// DeleteField godoc
// @Summary      删除数据源凭证字段
// @Description  删除指定字段的存储凭证，仅支持 "credentials" 字段
// @Tags         DataSource
// @Produce      json
// @Param        id     path      string  true  "数据源 ID"
// @Param        field  path      string  true  "凭证字段名"
// @Success      204
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /datasource/{id}/credentials/{field} [delete]
func (h *DataSourceCredentialsHandler) DeleteField(c *gin.Context) {
	ds, ok := h.ownDataSource(c)
	if !ok {
		return
	}
	field := c.Param("field")
	if field != "credentials" {
		c.Error(errors.NewBadRequestError("unknown credential field: " + secutils.SanitizeForLog(field)))
		return
	}
	if err := h.service.ClearDataSourceCredentials(c.Request.Context(), ds.ID); err != nil {
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"data_source_id": secutils.SanitizeForLog(ds.ID),
		})
		c.Error(errors.NewInternalServerError("failed to clear credentials: " + err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
