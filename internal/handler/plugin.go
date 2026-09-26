package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginHandler serves the read-only plugin catalog: every plugin this
// process knows about and what each contributes. Builtins are listed like
// any other plugin.
type PluginHandler struct {
	registry *registry.Registry
	tenancy  *tenancy.Service
	drivers  *driver.Set
}

// NewPluginHandler creates a PluginHandler. Without a tenancy service every
// plugin reads as enabled; without drivers no instance status is reported.
func NewPluginHandler(registry *registry.Registry, tenancy *tenancy.Service, drivers *driver.Set) *PluginHandler {
	return &PluginHandler{registry: registry, tenancy: tenancy, drivers: drivers}
}

// PluginDetailDTO is one plugin with where and how its code runs.
type PluginDetailDTO struct {
	tenancy.TenantPlugin
	Instances []driver.InstanceStatus `json:"instances"`
	// InstanceError explains an empty Instances (no driver, lookup failed).
	InstanceError string `json:"instanceError,omitempty"`
}

// tenantPlugins returns every plugin with the caller tenant's switch.
func (h *PluginHandler) tenantPlugins(c *gin.Context) ([]tenancy.TenantPlugin, error) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if h.tenancy == nil || tenantID == 0 {
		plugins := h.registry.Plugins()
		out := make([]tenancy.TenantPlugin, 0, len(plugins))
		for _, m := range plugins {
			out = append(out, tenancy.TenantPlugin{Manifest: m, Enabled: true})
		}
		return out, nil
	}
	return h.tenancy.List(c.Request.Context(), tenantID)
}

// PluginContributionDTO is one contribution with the plugin providing it.
type PluginContributionDTO struct {
	manifest.Contribution
	PluginID    string `json:"pluginId"`
	QualifiedID string `json:"qualifiedId"`
	// Version is the plugin's active version; page files are served under
	// it.
	Version string `json:"version,omitempty"`
	// Enabled reports whether the caller's tenant has the plugin enabled.
	Enabled bool `json:"enabled"`
}

// PluginContributionsDTO groups contributions by extension point.
type PluginContributionsDTO struct {
	Points        []manifest.PointInfo                       `json:"points"`
	Contributions map[manifest.Point][]PluginContributionDTO `json:"contributions"`
	// PluginIcons are the icons (data URIs) of plugins the tenant switched
	// off: their types leave the type lists, and instance lists still need
	// an icon for them.
	PluginIcons map[string]string `json:"pluginIcons,omitempty"`
}

// ListPlugins godoc
// @Summary      列出插件
// @Description  列出当前进程已知的全部插件（含内置插件）、其贡献，以及当前空间是否启用
// @Tags         Plugin
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins [get]
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	plugins, err := h.tenantPlugins(c)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("failed to load plugin settings"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plugins})
}

// GetPlugin godoc
// @Summary      获取插件详情
// @Tags         Plugin
// @Produce      json
// @Param        id   path      string  true  "插件 ID，如 weknora.feishu"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins/{id} [get]
func (h *PluginHandler) GetPlugin(c *gin.Context) {
	plugins, err := h.tenantPlugins(c)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("failed to load plugin settings"))
		return
	}
	for _, p := range plugins {
		if p.Manifest.ID == c.Param("id") {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": h.detail(c, p)})
			return
		}
	}
	_ = c.Error(errors.NewNotFoundError("plugin not found"))
}

func (h *PluginHandler) detail(c *gin.Context, p tenancy.TenantPlugin) PluginDetailDTO {
	out := PluginDetailDTO{TenantPlugin: p, Instances: []driver.InstanceStatus{}}
	if h.drivers == nil {
		return out
	}
	d, err := h.drivers.For(p.Manifest.Runtime.Type)
	if err == nil {
		out.Instances, err = d.Status(c.Request.Context(), p.Manifest.ID)
	}
	if err != nil {
		out.InstanceError = err.Error()
		out.Instances = []driver.InstanceStatus{}
	}
	return out
}

// SetPluginEnabledRequest turns a plugin on or off for the workspace.
type SetPluginEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// SetPluginEnabled godoc
// @Summary      启用或停用插件
// @Description  为当前空间启用或停用插件；停用后其贡献不再出现在类型列表中、不能新建实例，已有实例不受影响。必需插件不能停用
// @Tags         Plugin
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "插件 ID"
// @Param        request  body      SetPluginEnabledRequest  true  "开关"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins/{id}/enabled [put]
func (h *PluginHandler) SetPluginEnabled(c *gin.Context) {
	var req SetPluginEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("enabled is required"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if h.tenancy == nil || tenantID == 0 {
		_ = c.Error(errors.NewBadRequestError("workspace context missing"))
		return
	}
	userID, _ := c.Request.Context().Value(types.UserIDContextKey).(string)
	err := h.tenancy.SetEnabled(c.Request.Context(), tenantID, c.Param("id"), *req.Enabled, userID)
	switch {
	case stderrors.Is(err, tenancy.ErrUnknownPlugin):
		_ = c.Error(errors.NewNotFoundError("plugin not found"))
		return
	case stderrors.Is(err, tenancy.ErrRequiredPlugin):
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	case err != nil:
		_ = c.Error(errors.NewInternalServerError("failed to save plugin setting"))
		return
	}
	h.GetPlugin(c)
}

// ListContributions godoc
// @Summary      按扩展点列出贡献
// @Description  返回各扩展点下的全部贡献；可用 point 参数只取一个扩展点
// @Tags         Plugin
// @Produce      json
// @Param        point  query     string  false  "扩展点，如 connectors"
// @Success      200    {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins/contributions [get]
func (h *PluginHandler) ListContributions(c *gin.Context) {
	points := manifest.Points()
	if p := c.Query("point"); p != "" {
		info, ok := manifest.LookupPoint(manifest.Point(p))
		if !ok {
			_ = c.Error(errors.NewBadRequestError("unknown extension point"))
			return
		}
		points = []manifest.PointInfo{info}
	}
	out := PluginContributionsDTO{
		Points:        points,
		Contributions: make(map[manifest.Point][]PluginContributionDTO, len(points)),
	}
	enabled := map[string]bool{}
	plugins, err := h.tenantPlugins(c)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("failed to load plugin settings"))
		return
	}
	for _, p := range plugins {
		enabled[p.Manifest.ID] = p.Enabled
	}
	for _, info := range points {
		entries := h.registry.Contributions(info.Point)
		list := make([]PluginContributionDTO, 0, len(entries))
		for _, e := range entries {
			dto := PluginContributionDTO{
				Contribution: e.Contribution,
				PluginID:     e.PluginID,
				QualifiedID:  e.QualifiedID,
				Enabled:      enabled[e.PluginID],
			}
			if m, ok := h.registry.Plugin(e.PluginID); ok {
				dto.Version = m.Version
				if !dto.Enabled && m.IconData != "" {
					if out.PluginIcons == nil {
						out.PluginIcons = map[string]string{}
					}
					out.PluginIcons[m.ID] = m.IconData
				}
			}
			list = append(list, dto)
		}
		out.Contributions[info.Point] = list
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// configError maps a plugin configuration failure to an HTTP error.
func configError(c *gin.Context, err error) {
	var fields configschema.FieldErrors
	switch {
	case stderrors.As(err, &fields):
		_ = c.Error(errors.NewBadRequestError(err.Error()).WithDetails(fields))
	case stderrors.Is(err, tenancy.ErrUnknownPlugin), stderrors.Is(err, install.ErrNotInstalled):
		_ = c.Error(errors.NewNotFoundError("plugin not found"))
	case stderrors.Is(err, tenancy.ErrNoTenantConfig), stderrors.Is(err, install.ErrNoSystemConfig):
		_ = c.Error(errors.NewBadRequestError(err.Error()))
	default:
		logger.Errorf(c.Request.Context(), "[plugin] configuration request failed: %v", err)
		_ = c.Error(errors.NewInternalServerError("failed to handle plugin configuration"))
	}
}

// PluginConfigRequest carries configuration values; secrets may be sent back
// as "***" to keep them.
type PluginConfigRequest struct {
	Values map[string]any `json:"values"`
}

// GetPluginConfig godoc
// @Summary      获取插件的空间配置
// @Description  返回插件的空间级配置 Schema 和当前值（密钥脱敏为 ***）
// @Tags         Plugin
// @Produce      json
// @Param        id   path      string  true  "插件 ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins/{id}/config [get]
func (h *PluginHandler) GetPluginConfig(c *gin.Context) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if h.tenancy == nil || tenantID == 0 {
		_ = c.Error(errors.NewBadRequestError("workspace context missing"))
		return
	}
	cfg, err := h.tenancy.Config(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		configError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}

// UpdatePluginConfig godoc
// @Summary      保存插件的空间配置
// @Description  按插件声明的 Schema 校验并保存空间级配置；密钥加密存储，回传 *** 表示保持不变
// @Tags         Plugin
// @Accept       json
// @Produce      json
// @Param        id       path      string               true  "插件 ID"
// @Param        request  body      PluginConfigRequest  true  "配置"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins/{id}/config [put]
func (h *PluginHandler) UpdatePluginConfig(c *gin.Context) {
	var req PluginConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("values are required"))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if h.tenancy == nil || tenantID == 0 {
		_ = c.Error(errors.NewBadRequestError("workspace context missing"))
		return
	}
	userID, _ := c.Request.Context().Value(types.UserIDContextKey).(string)
	cfg, err := h.tenancy.SetConfig(c.Request.Context(), tenantID, c.Param("id"), req.Values, userID)
	if err != nil {
		configError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}
