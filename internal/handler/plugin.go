package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
)

// PluginHandler serves the read-only plugin catalog: every plugin this
// process knows about and what each contributes. Builtins are listed like
// any other plugin.
type PluginHandler struct {
	registry *registry.Registry
}

// NewPluginHandler creates a PluginHandler.
func NewPluginHandler(registry *registry.Registry) *PluginHandler {
	return &PluginHandler{registry: registry}
}

// PluginContributionDTO is one contribution with the plugin providing it.
type PluginContributionDTO struct {
	manifest.Contribution
	PluginID    string `json:"pluginId"`
	QualifiedID string `json:"qualifiedId"`
}

// PluginContributionsDTO groups contributions by extension point.
type PluginContributionsDTO struct {
	Points        []manifest.PointInfo                       `json:"points"`
	Contributions map[manifest.Point][]PluginContributionDTO `json:"contributions"`
}

// ListPlugins godoc
// @Summary      列出插件
// @Description  列出当前进程已知的全部插件（含内置插件）及其贡献
// @Tags         Plugin
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /plugins [get]
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.registry.Plugins()})
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
	m, ok := h.registry.Plugin(c.Param("id"))
	if !ok {
		_ = c.Error(errors.NewNotFoundError("plugin not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": m})
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
	for _, info := range points {
		entries := h.registry.Contributions(info.Point)
		list := make([]PluginContributionDTO, 0, len(entries))
		for _, e := range entries {
			list = append(list, PluginContributionDTO{
				Contribution: e.Contribution,
				PluginID:     e.PluginID,
				QualifiedID:  e.QualifiedID,
			})
		}
		out.Contributions[info.Point] = list
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}
