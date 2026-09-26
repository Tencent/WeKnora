package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

// RegisterPluginRoutes registers the plugin catalog and the workspace's
// plugin switches. Any member may read the catalog; only admins change the
// switches. Like the other integration catalogs it stays closed to scoped
// API keys.
func RegisterPluginRoutes(
	r *gin.RouterGroup, h *handler.PluginHandler, ui *handler.PluginUIHandler, hooks *handler.PluginWebhookHandler,
	forms *handler.PluginFormsHandler, g *rbacGuards,
) {
	plugins := g.apiKeyGroup(r.Group("/plugins"), apiKeyFullAccess())
	{
		plugins.GET("", g.Viewer(), h.ListPlugins)
		// Registered before /:id so the static segment wins.
		plugins.GET("/contributions", g.Viewer(), h.ListContributions)
		plugins.GET("/:id", g.Viewer(), h.GetPlugin)
		// Turning a plugin off hides its integrations workspace-wide — Admin+.
		plugins.PUT("/:id/enabled", g.Admin(), h.SetPluginEnabled)
		// Workspace configuration carries credentials — Admin+ to read too.
		plugins.GET("/:id/config", g.Admin(), h.GetPluginConfig)
		plugins.PUT("/:id/config", g.Admin(), h.UpdatePluginConfig)
		if ui != nil {
			// Any member may use a plugin's pages; a page's minRole is
			// checked per request, since it depends on the page.
			ui.SetRoleGuard(func(need types.TenantRole) gin.HandlerFunc { return middleware.RequireRole(need, g.cfg) })
			plugins.POST("/:id/ui-request", g.Viewer(), ui.Request)
		}
		// Webhook URLs carry their secret — Admin+.
		if hooks != nil {
			plugins.GET("/:id/webhooks", g.Admin(), hooks.List)
		}
		// Plugin forms are filled by workspace admins, or system admins
		// for the platform configuration (checked per request).
		if forms != nil {
			plugins.POST("/:id/options", g.AdminOrSystemAdmin(), forms.Options)
			plugins.POST("/:id/oauth/start", g.AdminOrSystemAdmin(), forms.OAuthStart)
			plugins.GET("/:id/oauth/result", g.AdminOrSystemAdmin(), forms.OAuthResult)
		}
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
		plugins.GET("/market", h.ListMarketPlugins)
		plugins.GET("/tenants", h.ListPluginAudienceTenants)
		plugins.GET("/:id", h.GetInstalledPlugin)
		plugins.DELETE("/:id", h.UninstallPlugin)
		plugins.PUT("/:id/enabled", h.SetInstalledPluginEnabled)
		plugins.PUT("/:id/active-version", h.ActivatePluginVersion)
		plugins.PUT("/:id/remote-url", h.SetPluginRemoteURL)
		plugins.PUT("/:id/audience", h.SetPluginAudience)
		plugins.POST("/:id/secret/rotate", h.RotatePluginSecret)
		plugins.GET("/:id/config", h.GetPluginSystemConfig)
		plugins.PUT("/:id/config", h.UpdatePluginSystemConfig)
	}
}

// RegisterTenantPluginRoutes registers workspace admins' own remote plugins.
// The platform switch (tenant.plugin_remote_enabled) is checked per request.
func RegisterTenantPluginRoutes(r *gin.RouterGroup, h *handler.TenantPluginHandler, g *rbacGuards) {
	if h == nil {
		return
	}
	plugins := r.Group("/tenant-plugins", g.Admin())
	{
		plugins.GET("", h.ListTenantPlugins)
		plugins.POST("", h.InstallTenantPlugin)
		plugins.POST("/inspect", h.InspectTenantPlugin)
		plugins.DELETE("/:id", h.UninstallTenantPlugin)
		plugins.PUT("/:id/remote-url", h.SetTenantPluginRemoteURL)
		plugins.POST("/:id/secret/rotate", h.RotateTenantPluginSecret)
	}
}
