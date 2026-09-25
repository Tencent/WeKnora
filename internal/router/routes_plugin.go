package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterPluginRoutes registers the read-only plugin catalog. It describes
// what this deployment can do, not tenant data, so any member may read it;
// like the other integration catalogs it stays closed to scoped API keys.
func RegisterPluginRoutes(r *gin.RouterGroup, h *handler.PluginHandler, g *rbacGuards) {
	plugins := g.apiKeyGroup(r.Group("/plugins"), apiKeyFullAccess())
	{
		plugins.GET("", g.Viewer(), h.ListPlugins)
		// Registered before /:id so the static segment wins.
		plugins.GET("/contributions", g.Viewer(), h.ListContributions)
		plugins.GET("/:id", g.Viewer(), h.GetPlugin)
	}
}
