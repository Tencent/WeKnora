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
}

// readPackage returns the package archive from an upload or a URL.
func (h *PluginAdminHandler) readPackage(c *gin.Context) ([]byte, install.Source, string, bool) {
	if strings.HasPrefix(c.ContentType(), "application/json") {
		limitJSONBody(c, skillSourceJSONMaxBytes)
		var req PluginPackageRequest
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.URL) == "" {
			_ = c.Error(errors.NewBadRequestError("url or an uploaded file is required"))
			return nil, install.Source{}, "", false
		}
		data, err := h.service.FetchURL(c.Request.Context(), strings.TrimSpace(req.URL))
		if err != nil {
			h.fail(c, err)
			return nil, install.Source{}, "", false
		}
		return data, install.Source{Kind: "url", URL: strings.TrimSpace(req.URL)}, req.Digest, true
	}

	limitUploadBody(c, pkg.MaxArchiveBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			_ = c.Error(errors.NewBadRequestError("plugin package is too large"))
		} else {
			_ = c.Error(errors.NewBadRequestError("file is required"))
		}
		return nil, install.Source{}, "", false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, pkg.MaxArchiveBytes+1))
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("failed to read the uploaded package"))
		return nil, install.Source{}, "", false
	}
	if len(data) > pkg.MaxArchiveBytes {
		_ = c.Error(errors.NewBadRequestError("plugin package is too large"))
		return nil, install.Source{}, "", false
	}
	return data, install.Source{Kind: "upload", URL: header.Filename}, c.PostForm("digest"), true
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
	data, _, _, ok := h.readPackage(c)
	if !ok {
		return
	}
	preview, err := h.service.Inspect(c.Request.Context(), data)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.ok(c, preview)
}

// InstallPlugin godoc
// @Summary      安装或升级插件
// @Description  安装插件包（multipart file + digest，或 JSON {url, digest}）。digest 取自 inspect，保证安装的就是审阅过的包。安装后全平台可用，各空间需自行启用
// @Tags         System
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/plugins [post]
func (h *PluginAdminHandler) InstallPlugin(c *gin.Context) {
	data, source, digest, ok := h.readPackage(c)
	if !ok {
		return
	}
	userID, _ := c.Request.Context().Value(types.UserIDContextKey).(string)
	view, err := h.service.Install(c.Request.Context(), install.Request{
		Data: data, Source: source, ExpectedDigest: digest, UserID: userID,
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
