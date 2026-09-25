package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterPluginRoutes registers the plugin catalog and the workspace's
// plugin switches. Any member may read the catalog; only admins change the
// switches. Like the other integration catalogs it stays closed to scoped
// API keys.
func RegisterPluginRoutes(r *gin.RouterGroup, h *handler.PluginHandler, g *rbacGuards) {
	plugins := g.apiKeyGroup(r.Group("/plugins"), apiKeyFullAccess())
	{
		plugins.GET("", g.Viewer(), h.ListPlugins)
		// Registered before /:id so the static segment wins.
		plugins.GET("/contributions", g.Viewer(), h.ListContributions)
		plugins.GET("/:id", g.Viewer(), h.GetPlugin)
		// Turning a plugin off hides its integrations workspace-wide — Admin+.
		plugins.PUT("/:id/enabled", g.Admin(), h.SetPluginEnabled)
	}
}

// RegisterPluginAdminRoutes registers plugin installation for system
// administrators. Installing changes every tenant's catalog, so API keys
// stay default-denied like the rest of /system/admin.
func RegisterPluginAdminRoutes(r *gin.RouterGroup, h *handler.PluginAdminHandler, g *rbacGuards) {
	plugins := r.Group("/system/admin/plugins", g.SystemAdmin())
	{
		plugins.GET("", h.ListInstalledPlugins)
		plugins.POST("", h.InstallPlugin)
		// Registered before /:id so the static segment wins.
		plugins.POST("/inspect", h.InspectPlugin)
		plugins.GET("/:id", h.GetInstalledPlugin)
		plugins.DELETE("/:id", h.UninstallPlugin)
		plugins.PUT("/:id/enabled", h.SetInstalledPluginEnabled)
		plugins.PUT("/:id/active-version", h.ActivatePluginVersion)
	}
}
