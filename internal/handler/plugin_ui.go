package handler

import (
	"encoding/json"
	stderrors "errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// PluginUIAssetsPrefix is where plugin page files are served:
// {prefix}/{pluginId}/{version}/ui/...
const PluginUIAssetsPrefix = "/api/v1/plugin-ui/assets"

// pluginPageCSP confines a plugin page to its own files. It can reach
// nothing over the network (connect-src 'none'): everything goes through the
// bridge. The page also runs in an iframe sandbox without
// allow-same-origin, so it cannot read WeKnora's storage.
// The sandbox directive keeps a page opened on its own (a link to its URL)
// out of WeKnora's origin too, not only inside PluginFrame's iframe.
const pluginPageCSP = "default-src 'none'; script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; " +
	"media-src 'self' data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'; " +
	"frame-ancestors 'self'; sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"

// maxUIRequestBody bounds what a page can send in one request.
const maxUIRequestBody = 1 << 20

// PluginUIHandler serves plugin pages and relays their requests to the
// plugin.
type PluginUIHandler struct {
	pages   *activate.UIPages
	invoker *activate.Invoker
	tenancy *tenancy.Service
	// roleGuard enforces a page's minRole with the same rules as the route
	// guards (API keys, cross-tenant superusers, the RBAC switch).
	roleGuard func(types.TenantRole) gin.HandlerFunc
}

// NewPluginUIHandler creates the handler.
func NewPluginUIHandler(pages *activate.UIPages, invoker *activate.Invoker, t *tenancy.Service) *PluginUIHandler {
	return &PluginUIHandler{pages: pages, invoker: invoker, tenancy: t}
}

// SetRoleGuard supplies the role check; routes set it when they register.
func (h *PluginUIHandler) SetRoleGuard(g func(types.TenantRole) gin.HandlerFunc) { h.roleGuard = g }

// ServeAsset serves one file of a plugin's pages. It needs no login: the
// sandboxed iframe loading it has no credentials, and page files are part of
// an installed package, like the app's own scripts.
func (h *PluginUIHandler) ServeAsset(c *gin.Context) {
	full, err := h.pages.File(c.Param("id"), c.Param("version"), strings.TrimPrefix(c.Param("path"), "/"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	ctype := mime.TypeByExtension(ext)
	switch ext {
	case ".js", ".mjs":
		ctype = "text/javascript; charset=utf-8"
	case ".html":
		ctype = "text/html; charset=utf-8"
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	hdr := c.Writer.Header()
	hdr.Set("Content-Type", ctype)
	hdr.Set("Content-Security-Policy", pluginPageCSP)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Referrer-Policy", "no-referrer")
	// The sandboxed page has an opaque origin: module scripts it loads
	// from here are cross-origin requests.
	hdr.Set("Access-Control-Allow-Origin", "*")
	hdr.Set("Cache-Control", "public, max-age=300")
	f, err := os.Open(full)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	// ServeContent, not ServeFile: ServeFile redirects .../index.html.
	http.ServeContent(c.Writer, c.Request, filepath.Base(full), info.ModTime(), f)
}

// PluginUIRequest is what a page sent through the bridge.
type PluginUIRequest struct {
	Mount  string          `json:"mount"  binding:"required"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// Request godoc
// @Summary      转发插件页面的请求
// @Description  插件页面在沙箱 iframe 中运行，只能经宿主桥发请求；这里校验空间已启用该插件、调用者满足页面的 minRole，再转给插件后端
// @Tags         Plugins
// @Accept       json
// @Produce      json
// @Param        id       path      string            true  "插件 ID"
// @Param        request  body      PluginUIRequest  true  "页面请求"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /plugins/{id}/ui-request [post]
func (h *PluginUIHandler) Request(c *gin.Context) {
	limitJSONBody(c, maxUIRequestBody)
	var req PluginUIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("mount is required"))
		return
	}
	pluginID := c.Param("id")
	mount, err := h.pages.Mount(pluginID, req.Mount)
	if err != nil {
		_ = c.Error(errors.NewNotFoundError("no such plugin page"))
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	// Fails closed: the request carries workspace data to the plugin.
	if h.tenancy != nil {
		if on, err := h.tenancy.PluginEnabled(ctx, tenantID, pluginID); err != nil || !on {
			_ = c.Error(errors.NewNotFoundError("the plugin is not enabled in this workspace"))
			return
		}
	}
	if need := types.TenantRole(mount.MinRole()); need != types.TenantRoleViewer && h.roleGuard != nil {
		if h.roleGuard(need)(c); c.IsAborted() {
			return
		}
	}
	if !mount.Manifest.Runtime.Type.HasCode() {
		_ = c.Error(errors.NewNotFoundError("this plugin's pages make no requests"))
		return
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	path := req.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	in := pluginapi.UIRequest{
		Mount: req.Mount, Method: method, Path: path, Body: req.Body,
		Role: string(types.TenantRoleFromContext(ctx)),
	}
	var out pluginapi.UIResponse
	if err := h.invoker.Call(ctx, mount.Manifest, pluginapi.UIRequestPath, nil, in, &out); err != nil {
		var pe *pluginapi.Error
		if stderrors.As(err, &pe) && pe.Code == pluginapi.CodeNotFound {
			_ = c.Error(errors.NewNotFoundError("this plugin's pages make no requests"))
			return
		}
		logger.Warnf(ctx, "[plugin] page request to %s failed: %v", pluginID, err)
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
		return
	}
	if out.Status == 0 {
		out.Status = http.StatusOK
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}
