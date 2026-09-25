package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
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
}

// NewPluginHandler creates a PluginHandler. Without a tenancy service every
// plugin reads as enabled.
func NewPluginHandler(registry *registry.Registry, tenancy *tenancy.Service) *PluginHandler {
	return &PluginHandler{registry: registry, tenancy: tenancy}
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
	// Enabled reports whether the caller's tenant has the plugin enabled.
	Enabled bool `json:"enabled"`
}

// PluginContributionsDTO groups contributions by extension point.
type PluginContributionsDTO struct {
	Points        []manifest.PointInfo                       `json:"points"`
	Contributions map[manifest.Point][]PluginContributionDTO `json:"contributions"`
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
			c.JSON(http.StatusOK, gin.H{"success": true, "data": p})
			return
		}
	}
	_ = c.Error(errors.NewNotFoundError("plugin not found"))
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
			list = append(list, PluginContributionDTO{
				Contribution: e.Contribution,
				PluginID:     e.PluginID,
				QualifiedID:  e.QualifiedID,
				Enabled:      enabled[e.PluginID],
			})
		}
		out.Contributions[info.Point] = list
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}
