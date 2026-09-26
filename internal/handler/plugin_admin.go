package handler

import (
	stderrors "errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginAdminHandler lets system administrators install and manage plugins
// for the whole platform.
type PluginAdminHandler struct {
	service *install.Service
}

// NewPluginAdminHandler creates a PluginAdminHandler.
func NewPluginAdminHandler(service *install.Service) *PluginAdminHandler {
	return &PluginAdminHandler{service: service}
}

// PluginPackageRequest locates a package by URL; uploads send the archive as
// the multipart "file" field and the digest as a form field instead.
type PluginPackageRequest struct {
	URL string `json:"url"`
	// Digest pins an install to the package reviewed with inspect.
	Digest string `json:"digest"`
	// RemoteURL is where a remote plugin's service runs.
	RemoteURL string `json:"remote_url"`
}

// packageInput is a package with what its install request said about it.
type packageInput struct {
	data      []byte
	source    install.Source
	digest    string
	remoteURL string
}

// readPackage returns the package archive from an upload or a URL.
func (h *PluginAdminHandler) readPackage(c *gin.Context) (packageInput, bool) {
	if strings.HasPrefix(c.ContentType(), "application/json") {
		limitJSONBody(c, skillSourceJSONMaxBytes)
		var req PluginPackageRequest
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.URL) == "" {
			_ = c.Error(errors.NewBadRequestError("url or an uploaded file is required"))
			return packageInput{}, false
		}
		data, err := h.service.FetchURL(c.Request.Context(), strings.TrimSpace(req.URL))
		if err != nil {
			h.fail(c, err)
			return packageInput{}, false
		}
		return packageInput{
			data: data, source: install.Source{Kind: "url", URL: strings.TrimSpace(req.URL)},
			digest: req.Digest, remoteURL: strings.TrimSpace(req.RemoteURL),
		}, true
	}

	limitUploadBody(c, pkg.MaxArchiveBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			_ = c.Error(errors.NewBadRequestError("plugin package is too large"))
		} else {
			_ = c.Error(errors.NewBadRequestError("file is required"))
		}
		return packageInput{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, pkg.MaxArchiveBytes+1))
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("failed to read the uploaded package"))
		return packageInput{}, false
	}
	if len(data) > pkg.MaxArchiveBytes {
		_ = c.Error(errors.NewBadRequestError("plugin package is too large"))
		return packageInput{}, false
	}
	return packageInput{
		data: data, source: install.Source{Kind: "upload", URL: header.Filename},
		digest: c.PostForm("digest"), remoteURL: strings.TrimSpace(c.PostForm("remote_url")),
	}, true
}

func (h *PluginAdminHandler) fail(c *gin.Context, err error) {
	var invalid *install.InvalidError
	switch {
	case stderrors.As(err, &invalid):
		_ = c.Error(errors.NewBadRequestError(err.Error()))
	case stderrors.Is(err, install.ErrNotInstalled):
		_ = c.Error(errors.NewNotFoundError("plugin is not installed"))
	default:
		logger.Errorf(c.Request.Context(), "[plugin] admin request failed: %v", err)
		_ = c.Error(errors.NewInternalServerError("plugin operation failed"))
	}
}

func (h *PluginAdminHandler) ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// InspectPlugin godoc
// @Summary      检查插件包
// @Description  解析上传的插件包（multipart file）或 URL（JSON {url}），返回清单、摘要和安装后的变化，不做任何修改
// @Tags         System
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/inspect [post]
func (h *PluginAdminHandler) InspectPlugin(c *gin.Context) {
	in, ok := h.readPackage(c)
	if !ok {
		return
	}
	preview, err := h.service.Inspect(c.Request.Context(), in.data)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, preview)
}

// InstallPlugin godoc
// @Summary      安装或升级插件
// @Description  安装插件包（multipart file + digest，或 JSON {url, digest}）。digest 取自 inspect，
// @Description  保证安装的就是审阅过的包。安装后全平台可用，各空间需自行启用。
// @Description  remote 插件还需 remote_url（服务地址）；首次安装的响应里 issuedSecret 是签名密钥，只返回这一次
// @Tags         System
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins [post]
func (h *PluginAdminHandler) InstallPlugin(c *gin.Context) {
	in, ok := h.readPackage(c)
	if !ok {
		return
	}
	userID, _ := c.Request.Context().Value(types.UserIDContextKey).(string)
	view, err := h.service.Install(c.Request.Context(), install.Request{
		Data: in.data, Source: in.source, ExpectedDigest: in.digest, UserID: userID, RemoteURL: in.remoteURL,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, view)
}

// ListInstalledPlugins godoc
// @Summary      列出已安装插件
// @Description  列出系统管理员安装的插件（不含内置插件），含版本和各节点加载状态
// @Tags         System
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins [get]
func (h *PluginAdminHandler) ListInstalledPlugins(c *gin.Context) {
	views, err := h.service.List(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, views)
}

// GetInstalledPlugin godoc
// @Summary      获取已安装插件
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id} [get]
func (h *PluginAdminHandler) GetInstalledPlugin(c *gin.Context) {
	view, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, view)
}

// SetInstalledPluginEnabled godoc
// @Summary      全平台启用或停用插件
// @Description  停用后所有节点卸载该插件；各空间的开关和配置保留
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "插件 ID"
// @Param        request  body      SetPluginEnabledRequest  true  "开关"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/enabled [put]
func (h *PluginAdminHandler) SetInstalledPluginEnabled(c *gin.Context) {
	var req SetPluginEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("enabled is required"))
		return
	}
	view, err := h.service.SetEnabled(c.Request.Context(), c.Param("id"), *req.Enabled)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, view)
}

// ActivatePluginVersionRequest picks a stored version.
type ActivatePluginVersionRequest struct {
	Version string `json:"version" binding:"required"`
}

// ActivatePluginVersion godoc
// @Summary      切换插件版本
// @Description  把已存储的某个版本设为当前版本（回滚或重新升级）
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                        true  "插件 ID"
// @Param        request  body      ActivatePluginVersionRequest  true  "版本"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/active-version [put]
func (h *PluginAdminHandler) ActivatePluginVersion(c *gin.Context) {
	var req ActivatePluginVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("version is required"))
		return
	}
	view, err := h.service.Activate(c.Request.Context(), c.Param("id"), req.Version)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, view)
}

// SetPluginRemoteURLRequest moves a remote plugin.
type SetPluginRemoteURLRequest struct {
	URL string `json:"url" binding:"required"`
}

// SetPluginRemoteURL godoc
// @Summary      修改远程插件的服务地址
// @Description  私有网络中的地址需要加入 SSRF_WHITELIST
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "插件 ID"
// @Param        request  body      SetPluginRemoteURLRequest  true  "服务地址"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/remote-url [put]
func (h *PluginAdminHandler) SetPluginRemoteURL(c *gin.Context) {
	var req SetPluginRemoteURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("url is required"))
		return
	}
	view, err := h.service.SetRemoteURL(c.Request.Context(), c.Param("id"), strings.TrimSpace(req.URL))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, view)
}

// RotatePluginSecret godoc
// @Summary      轮换远程插件的签名密钥
// @Description  新密钥只在本次响应的 issuedSecret 中返回；服务换上新密钥前调用会失败
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/secret/rotate [post]
func (h *PluginAdminHandler) RotatePluginSecret(c *gin.Context) {
	view, err := h.service.RotateSecret(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, view)
}

// UninstallPlugin godoc
// @Summary      卸载插件
// @Description  删除插件及其全部版本；各空间的开关和配置保留，重新安装后恢复
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id} [delete]
func (h *PluginAdminHandler) UninstallPlugin(c *gin.Context) {
	if err := h.service.Uninstall(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetPluginSystemConfig godoc
// @Summary      获取插件的平台配置
// @Description  返回插件的平台级配置 Schema 和当前值（密钥脱敏为 ***）
// @Tags         System
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/config [get]
func (h *PluginAdminHandler) GetPluginSystemConfig(c *gin.Context) {
	cfg, err := h.service.GetSystemConfig(c.Request.Context(), c.Param("id"))
	if err != nil {
		configError(c, err)
		return
	}
	h.ok(c, cfg)
}

// UpdatePluginSystemConfig godoc
// @Summary      保存插件的平台配置
// @Description  按插件声明的 Schema 校验并保存平台级配置，对所有空间生效
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        id       path      string               true  "插件 ID"
// @Param        request  body      PluginConfigRequest  true  "配置"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins/{id}/config [put]
func (h *PluginAdminHandler) UpdatePluginSystemConfig(c *gin.Context) {
	var req PluginConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("values are required"))
		return
	}
	cfg, err := h.service.SetSystemConfig(c.Request.Context(), c.Param("id"), req.Values)
	if err != nil {
		configError(c, err)
		return
	}
	h.ok(c, cfg)
}
