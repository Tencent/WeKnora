package handler

import (
	"net/http"
	"strconv"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type WebhookEndpointHandler struct {
	svc interfaces.WebhookEndpointService
}

func NewWebhookEndpointHandler(svc interfaces.WebhookEndpointService) *WebhookEndpointHandler {
	return &WebhookEndpointHandler{svc: svc}
}

func (h *WebhookEndpointHandler) tenantID(c *gin.Context) (uint64, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return 0, apperrors.NewBadRequestError("Invalid workspace ID")
	}
	return id, nil
}

// List godoc
// @Summary      列出工作空间事件回调端点
// @Tags         工作空间事件回调
// @Produce      json
// @Param        id  path  int  true  "空间 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/webhooks [get]
func (h *WebhookEndpointHandler) List(c *gin.Context) {
	tenantID, err := h.tenantID(c)
	if err != nil {
		c.Error(err)
		return
	}
	rows, err := h.svc.List(c.Request.Context(), tenantID)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

// ListTypes godoc
// @Summary      列出可订阅的事件类型
// @Tags         工作空间事件回调
// @Produce      json
// @Param        id  path  int  true  "空间 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/types [get]
func (h *WebhookEndpointHandler) ListTypes(c *gin.Context) {
	if _, err := h.tenantID(c); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.svc.EventTypes()})
}

// Create godoc
// @Summary      创建工作空间事件回调端点
// @Tags         工作空间事件回调
// @Accept       json
// @Produce      json
// @Param        id    path  int  true  "空间 ID"
// @Param        body  body  dto.WebhookEndpointCreateRequest  true  "端点"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/webhooks [post]
func (h *WebhookEndpointHandler) Create(c *gin.Context) {
	tenantID, err := h.tenantID(c)
	if err != nil {
		c.Error(err)
		return
	}
	var req dto.WebhookEndpointCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	row, err := h.svc.Create(c.Request.Context(), tenantID, interfaces.WebhookEndpointCreate{
		Name:        req.Name,
		URL:         req.URL,
		Secret:      req.Secret,
		Events:      req.Events,
		Enabled:     req.Enabled,
		Description: req.Description,
	})
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": row})
}

// Update godoc
// @Summary      更新工作空间事件回调端点
// @Tags         工作空间事件回调
// @Accept       json
// @Produce      json
// @Param        id       path  int     true  "空间 ID"
// @Param        hook_id  path  string  true  "端点 ID"
// @Param        body     body  dto.WebhookEndpointUpdateRequest  true  "端点"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/webhooks/{hook_id} [patch]
func (h *WebhookEndpointHandler) Update(c *gin.Context) {
	tenantID, err := h.tenantID(c)
	if err != nil {
		c.Error(err)
		return
	}
	var req dto.WebhookEndpointUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	row, err := h.svc.Update(c.Request.Context(), tenantID, c.Param("hook_id"), interfaces.WebhookEndpointUpdate{
		Name:        req.Name,
		URL:         req.URL,
		Secret:      req.Secret,
		Events:      req.Events,
		Enabled:     req.Enabled,
		Description: req.Description,
	})
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": row})
}

// Delete godoc
// @Summary      删除工作空间事件回调端点
// @Tags         工作空间事件回调
// @Produce      json
// @Param        id       path  int     true  "空间 ID"
// @Param        hook_id  path  string  true  "端点 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/webhooks/{hook_id} [delete]
func (h *WebhookEndpointHandler) Delete(c *gin.Context) {
	tenantID, err := h.tenantID(c)
	if err != nil {
		c.Error(err)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), tenantID, c.Param("hook_id")); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Test godoc
// @Summary      向指定端点发送 webhook.test
// @Tags         工作空间事件回调
// @Produce      json
// @Param        id       path  int     true  "空间 ID"
// @Param        hook_id  path  string  true  "端点 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/webhooks/{hook_id}/test [post]
func (h *WebhookEndpointHandler) Test(c *gin.Context) {
	tenantID, err := h.tenantID(c)
	if err != nil {
		c.Error(err)
		return
	}
	if err := h.svc.Test(c.Request.Context(), tenantID, c.Param("hook_id")); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListDeliveries godoc
// @Summary      列出端点最近投递记录
// @Tags         工作空间事件回调
// @Produce      json
// @Param        id       path   int     true   "空间 ID"
// @Param        hook_id  path   string  true   "端点 ID"
// @Param        limit    query  int     false  "条数上限"  default(50)
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenants/{id}/event/webhooks/{hook_id}/deliveries [get]
func (h *WebhookEndpointHandler) ListDeliveries(c *gin.Context) {
	tenantID, err := h.tenantID(c)
	if err != nil {
		c.Error(err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	rows, err := h.svc.ListDeliveries(c.Request.Context(), tenantID, c.Param("hook_id"), limit)
	if err != nil {
		c.Error(err)
		return
	}
	if rows == nil {
		rows = []*types.TenantWebhookDelivery{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}
