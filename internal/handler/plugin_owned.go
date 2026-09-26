package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
)

// TenantPluginHandler lets workspace admins register their own remote
// plugins, when the platform allows it.
type TenantPluginHandler struct {
	service *install.Service
	tenancy *tenancy.Service
	admin   *PluginAdminHandler
}

// NewTenantPluginHandler creates a TenantPluginHandler.
func NewTenantPluginHandler(service *install.Service, t *tenancy.Service) *TenantPluginHandler {
	return &TenantPluginHandler{service: service, tenancy: t, admin: NewPluginAdminHandler(service)}
}

func (h *TenantPluginHandler) fail(c *gin.Context, err error) {
	if stderrors.Is(err, install.ErrTenantPluginsOff) {
		_ = c.Error(errors.NewForbiddenError(err.Error()))
		return
	}
	h.admin.fail(c, err)
}

func tenantOf(c *gin.Context) uint64 { return c.GetUint64(types.TenantIDContextKey.String()) }

// ListTenantPlugins godoc
// @Summary      列出本空间自有插件
// @Description  allowed 表示平台是否允许登记自有插件；plugins 为本空间登记的远程插件
// @Tags         Plugin
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenant-plugins [get]
func (h *TenantPluginHandler) ListTenantPlugins(c *gin.Context) {
	views, err := h.service.ListOwned(c.Request.Context(), tenantOf(c))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"allowed": h.service.TenantPluginsAllowed(c.Request.Context()), "plugins": views,
	}})
}

// InspectTenantPlugin godoc
// @Summary      检查本空间要登记的插件包
// @Description  只接受远程插件，且不能带页面
// @Tags         Plugin
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenant-plugins/inspect [post]
func (h *TenantPluginHandler) InspectTenantPlugin(c *gin.Context) {
	in, ok := readPluginPackage(c, h.service, h.fail)
	if !ok {
		return
	}
	preview, err := h.service.InspectOwned(c.Request.Context(), tenantOf(c), in.data)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": preview})
}

// InstallTenantPlugin godoc
// @Summary      登记或升级本空间自有插件
// @Description  远程插件包 + 服务地址（remote_url）。首次登记的响应里 issuedSecret 是签名密钥，只返回这一次。
// @Description  登记后在本空间启用
// @Tags         Plugin
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenant-plugins [post]
func (h *TenantPluginHandler) InstallTenantPlugin(c *gin.Context) {
	in, ok := readPluginPackage(c, h.service, h.fail)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	userID, _ := types.UserIDFromContext(ctx)
	view, err := h.service.InstallOwned(ctx, tenantOf(c), install.Request{
		Data: in.data, Source: in.source, ExpectedDigest: in.digest, UserID: userID, RemoteURL: in.remoteURL,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	if h.tenancy != nil {
		// Registering is choosing to use it.
		_ = h.tenancy.SetEnabled(ctx, tenantOf(c), view.ID, true, userID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// SetTenantPluginRemoteURL godoc
// @Summary      修改本空间自有插件的服务地址
// @Tags         Plugin
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "插件 ID"
// @Param        request  body      SetPluginRemoteURLRequest  true  "服务地址"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenant-plugins/{id}/remote-url [put]
func (h *TenantPluginHandler) SetTenantPluginRemoteURL(c *gin.Context) {
	var req SetPluginRemoteURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("url is required"))
		return
	}
	view, err := h.service.SetRemoteURLOwned(c.Request.Context(), tenantOf(c), c.Param("id"),
		strings.TrimSpace(req.URL))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// RotateTenantPluginSecret godoc
// @Summary      轮换本空间自有插件的签名密钥
// @Tags         Plugin
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenant-plugins/{id}/secret/rotate [post]
func (h *TenantPluginHandler) RotateTenantPluginSecret(c *gin.Context) {
	view, err := h.service.RotateSecretOwned(c.Request.Context(), tenantOf(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// UninstallTenantPlugin godoc
// @Summary      删除本空间自有插件
// @Tags         Plugin
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /tenant-plugins/{id} [delete]
func (h *TenantPluginHandler) UninstallTenantPlugin(c *gin.Context) {
	if err := h.service.UninstallOwned(c.Request.Context(), tenantOf(c), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
