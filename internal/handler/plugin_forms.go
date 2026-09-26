package handler

import (
	"encoding/json"
	stderrors "errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	pluginoauth "github.com/Tencent/WeKnora/internal/plugin/oauth"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// PluginFormsHandler serves what plugin forms need from the plugin while
// they are being filled: dynamic choices (x-options) and OAuth connections
// (x-oauth).
type PluginFormsHandler struct {
	registry    *registry.Registry
	tenancy     *tenancy.Service
	invoker     *activate.Invoker
	plugins     interfaces.PluginRepository
	dataSources interfaces.DataSourceRepository
	webSearch   interfaces.WebSearchProviderRepository
	oauth       *pluginoauth.Service
}

// NewPluginFormsHandler creates the handler.
func NewPluginFormsHandler(
	reg *registry.Registry, t *tenancy.Service, iv *activate.Invoker, plugins interfaces.PluginRepository,
	dataSources interfaces.DataSourceRepository, webSearch interfaces.WebSearchProviderRepository,
	oauth *pluginoauth.Service,
) *PluginFormsHandler {
	return &PluginFormsHandler{
		registry: reg, tenancy: t, invoker: iv, plugins: plugins, dataSources: dataSources, webSearch: webSearch,
		oauth: oauth,
	}
}

// PluginOptionsRequest asks a plugin for a field's choices.
type PluginOptionsRequest struct {
	Name  string `json:"name"         binding:"required"`
	Field string `json:"field"        binding:"required"`
	// Scope is system, tenant or instance.
	Scope string `json:"scope"        binding:"required"`
	// Contribution is "<point>/<id>" of the instance ("connectors/jira").
	Contribution string `json:"contribution"`
	// InstanceID is the instance being edited; its stored secrets fill in
	// the fields the form did not change.
	InstanceID string `json:"instanceId"`
	// Values are what the form holds now, in the scope's shape.
	Values map[string]any `json:"values"`
	Query  string         `json:"query"`
}

// Options godoc
// @Summary      插件表单的动态选项
// @Description  用表单当前的值（未修改的密钥用已存的值补齐）向插件取某个字段的可选项（x-options）
// @Tags         Plugins
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "插件 ID"
// @Param        request  body      PluginOptionsRequest  true  "请求"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /plugins/{id}/options [post]
func (h *PluginFormsHandler) Options(c *gin.Context) {
	limitJSONBody(c, 1<<20)
	var req PluginOptionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("name, field and scope are required"))
		return
	}
	ctx := c.Request.Context()
	m, ok := h.codePlugin(c, c.Param("id"), req.Scope)
	if !ok {
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	values := req.Values
	if values == nil {
		values = map[string]any{}
	}
	var override activate.ConfigOverride
	var instance map[string]any
	switch req.Scope {
	case pluginapi.OptionsScopeSystem:
		stored, _, err := install.OpenSystemConfig(ctx, h.plugins, m)
		if err != nil {
			h.fail(c, err)
			return
		}
		override.System = mergeForm(m.Config.SystemSchema, stored, values)
	case pluginapi.OptionsScopeTenant:
		stored, _, err := h.tenancy.OpenConfig(ctx, tenantID, m.ID)
		if err != nil {
			h.fail(c, err)
			return
		}
		override.Tenant = mergeForm(m.Config.TenantSchema, stored, values)
	case pluginapi.OptionsScopeInstance:
		var err error
		if instance, err = h.instanceValues(c, tenantID, m.ID, req, values); err != nil {
			h.fail(c, err)
			return
		}
	default:
		_ = c.Error(errors.NewBadRequestError("scope must be system, tenant or instance"))
		return
	}
	in := pluginapi.OptionsInput{Field: req.Field, Scope: req.Scope, Contribution: req.Contribution, Query: req.Query}
	var out pluginapi.OptionsOutput
	err := h.invoker.CallOverriding(ctx, m, pluginapi.OptionsPath(req.Name), instance, override, in, &out)
	if err != nil {
		h.fail(c, err)
		return
	}
	if out.Options == nil {
		out.Options = []pluginapi.Option{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// codePlugin finds a plugin with code the caller may ask for this scope.
// The workspace configuration is filled in before the plugin is switched
// on (connecting an account first, as its GET/PUT /config allow), so the
// tenant scope only needs a plugin the workspace can see; an instance
// belongs to an integration, which needs the plugin on.
func (h *PluginFormsHandler) codePlugin(c *gin.Context, id, scope string) (*manifest.Manifest, bool) {
	ctx := c.Request.Context()
	m, ok := h.registry.Plugin(id)
	if !ok || !m.Runtime.Type.HasCode() {
		_ = c.Error(errors.NewNotFoundError("no such plugin with code"))
		return nil, false
	}
	if scope == pluginapi.OptionsScopeSystem {
		if !types.IsSystemAdminFromContext(ctx) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden: system administrators only"})
			return nil, false
		}
		return m, true
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if scope == pluginapi.OptionsScopeTenant {
		if tenantID == 0 || !h.registry.VisibleTo(id, tenantID) {
			_ = c.Error(errors.NewNotFoundError("no such plugin in this workspace"))
			return nil, false
		}
		return m, true
	}
	if on, err := h.tenancy.PluginEnabled(ctx, tenantID, id); err != nil || !on {
		_ = c.Error(errors.NewNotFoundError("the plugin is not enabled in this workspace"))
		return nil, false
	}
	return m, true
}

// mergeForm fills the fields a form left redacted ("***") or empty with the
// stored values.
func mergeForm(rawSchema json.RawMessage, stored, values map[string]any) map[string]any {
	if len(rawSchema) == 0 {
		return values
	}
	schema, err := configschema.Parse(rawSchema)
	if err != nil {
		return values
	}
	return configschema.Merge(schema, stored, values)
}

// instanceValues is the instance the form holds, with the stored instance's
// secrets filled in where the form has none (forms never receive secrets).
// Only an instance of this plugin's contribution lends its secrets.
func (h *PluginFormsHandler) instanceValues(
	c *gin.Context, tenantID uint64, pluginID string, req PluginOptionsRequest, values map[string]any,
) (map[string]any, error) {
	point, local, _ := strings.Cut(req.Contribution, "/")
	qualified := manifest.QualifiedID(pluginID, local)
	switch manifest.Point(point) {
	case manifest.PointConnectors:
		creds, _ := values["credentials"].(map[string]any)
		if creds == nil {
			creds = map[string]any{}
		}
		if req.InstanceID != "" {
			ds, err := h.dataSources.FindByID(c.Request.Context(), req.InstanceID)
			if err != nil || ds == nil || ds.TenantID != tenantID || ds.Type != qualified {
				return nil, errInstanceNotFound
			}
			cfg, err := ds.ParseConfig()
			if err != nil {
				return nil, err
			}
			for k, v := range cfg.Credentials {
				if s, _ := creds[k].(string); s == "" || s == types.RedactedSecretPlaceholder {
					creds[k] = v
				}
			}
		}
		out := map[string]any{
			"credentials": creds,
			"settings":    values["settings"],
			"resourceIds": values["resourceIds"],
		}
		if out["settings"] == nil {
			out["settings"] = map[string]any{}
		}
		return out, nil
	case manifest.PointWebSearch:
		out := map[string]any{}
		for k, v := range values {
			out[k] = v
		}
		if key, _ := out["api_key"].(string); req.InstanceID != "" &&
			(key == "" || key == types.RedactedSecretPlaceholder) {
			p, err := h.webSearch.GetByID(c.Request.Context(), tenantID, req.InstanceID)
			if err != nil || p == nil || string(p.Provider) != qualified {
				return nil, errInstanceNotFound
			}
			out["api_key"] = p.Parameters.APIKey
		}
		return out, nil
	default:
		return values, nil
	}
}

var errInstanceNotFound = stderrors.New("instance not found")

// fail maps a plugin form failure: the plugin's own field problems go back
// to the form as they are.
func (h *PluginFormsHandler) fail(c *gin.Context, err error) {
	if stderrors.Is(err, errInstanceNotFound) {
		_ = c.Error(errors.NewNotFoundError("instance not found"))
		return
	}
	if pe, ok := pluginapi.AsError(err); ok {
		switch pe.Code {
		case pluginapi.CodeInvalidConfig, pluginapi.CodeUnauthorized, pluginapi.CodeBadRequest:
			body := gin.H{"success": false, "error": gin.H{"code": string(pe.Code), "message": pe.Message}}
			if pe.Details != nil && len(pe.Details.Fields) > 0 {
				body["error"].(gin.H)["fields"] = pe.Details.Fields
			}
			c.JSON(http.StatusBadRequest, body)
			return
		case pluginapi.CodeNotFound:
			_ = c.Error(errors.NewNotFoundError(pe.Message))
			return
		}
	}
	logger.Warnf(c.Request.Context(), "[plugin] form request to %s failed: %v", c.Param("id"), err)
	c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
}

// PluginOAuthStartRequest starts connecting an x-oauth field.
type PluginOAuthStartRequest struct {
	Scope        string `json:"scope"        binding:"required"`
	Contribution string `json:"contribution"`
	Field        string `json:"field"        binding:"required"`
}

// OAuthStart godoc
// @Summary      开始插件字段的 OAuth 授权
// @Description  为 x-oauth 字段生成授权地址；授权完成后回调页把连接 ID 交回表单，令牌只保存在服务端
// @Tags         Plugins
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "插件 ID"
// @Param        request  body      PluginOAuthStartRequest  true  "字段"
// @Success      200      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /plugins/{id}/oauth/start [post]
func (h *PluginFormsHandler) OAuthStart(c *gin.Context) {
	var req PluginOAuthStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("scope and field are required"))
		return
	}
	m, ok := h.codePlugin(c, c.Param("id"), req.Scope)
	if !ok {
		return
	}
	target := pluginoauth.Target{
		PluginID: m.ID, Scope: req.Scope, Contribution: req.Contribution, Field: req.Field,
	}
	if req.Scope != pluginapi.OptionsScopeSystem {
		target.TenantID = c.GetUint64(types.TenantIDContextKey.String())
	}
	userID, _ := types.UserIDFromContext(c.Request.Context())
	origin, base := appOrigin(c)
	url, state, err := h.oauth.Start(c.Request.Context(), target, userID, origin, base)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"authorizeUrl": url, "state": state, "redirectUri": base + pluginoauth.CallbackPath,
	}})
}

// OAuthResult godoc
// @Summary      查询插件字段 OAuth 授权的结果
// @Description  回调页无法把结果交回表单时（桌面端在系统浏览器中授权），表单轮询此接口；结果只能取一次
// @Tags         Plugins
// @Produce      json
// @Param        id     path      string  true  "插件 ID"
// @Param        state  query     string  true  "oauth/start 返回的 state"
// @Success      200    {object}  map[string]interface{}
// @Security     Bearer
// @Router       /plugins/{id}/oauth/result [get]
func (h *PluginFormsHandler) OAuthResult(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		_ = c.Error(errors.NewBadRequestError("state is required"))
		return
	}
	userID, _ := types.UserIDFromContext(c.Request.Context())
	o, err := h.oauth.Outcome(c.Request.Context(), state, userID)
	if stderrors.Is(err, pluginoauth.ErrOutcomePending) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"status": "pending"}})
		return
	}
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("read the authorization result"))
		return
	}
	data := gin.H{"status": "done", "ok": o.OK, "error": o.Error}
	if o.OK {
		data["connection"] = configschema.OAuthRefPrefix + o.ConnectionID
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// desktopLoopbackURL is where the desktop app's backend listens, for a
// system browser to come back to; cmd/desktop sets it.
var desktopLoopbackURL string

// SetDesktopLoopbackURL is called by cmd/desktop once its backend listens.
func SetDesktopLoopbackURL(u string) { desktopLoopbackURL = strings.TrimSuffix(u, "/") }

// fromDesktopWebview reports a request the desktop app's webview made
// through its proxy, which keeps the webview's host: "wails" on macOS,
// "wails.localhost" on Windows.
func fromDesktopWebview(c *gin.Context) bool {
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return desktopLoopbackURL != "" && (host == "wails" || host == "wails.localhost")
}

// appOrigin is the app's origin (where the callback page reports back) and
// the base URL authorization servers send the browser to: APP_EXTERNAL_URL
// when set, else the origin the browser called from. The desktop webview's
// origin is no place a browser can go back to; there the system browser
// returns to the backend's loopback address and the form polls for the
// result (no origin to report to).
func appOrigin(c *gin.Context) (origin, base string) {
	if ext := strings.TrimSuffix(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")), "/"); ext != "" {
		if u, err := url.Parse(ext); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host, ext
		}
	}
	if fromDesktopWebview(c) {
		return "", desktopLoopbackURL
	}
	if o := c.GetHeader("Origin"); strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://") {
		return o, o
	}
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := c.Request.Host
	if fh := c.GetHeader("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	o := scheme + "://" + host
	return o, o
}

var oauthDoneTemplate = template.Must(template.New("oauth").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>WeKnora</title></head>
<body style="font:14px system-ui,sans-serif;padding:24px">
<p id="msg">{{.Message}}</p>
<script>
(function () {
  var msg = {
    type: 'weknora-plugin-oauth', state: {{.State}}, ok: {{.OK}},
    connection: {{.Connection}}, error: {{.Error}}
  };
  if (window.opener && {{.Origin}}) {
    window.opener.postMessage(msg, {{.Origin}});
    window.close();
  }
})();
</script>
</body></html>`))

// OAuthCallback is where authorization servers send the browser back. It
// needs no login (the state names the pending authorization) and answers
// with a page that hands the result to the form that opened it.
func (h *PluginFormsHandler) OAuthCallback(c *gin.Context) {
	res := h.oauth.Complete(c.Request.Context(), c.Query("state"), c.Query("code"), c.Query("error"))
	data := map[string]any{
		"State": res.State, "Origin": res.Origin, "OK": res.Err == nil, "Connection": "", "Error": "",
		"Message": "Connected. You can close this window.",
	}
	if res.Err != nil {
		data["Error"], data["Message"] = res.Err.Error(), res.Err.Error()
	} else {
		data["Connection"] = configschema.OAuthRefPrefix + res.ConnectionID
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	status := http.StatusOK
	if res.Err != nil {
		status = http.StatusBadRequest
	}
	c.Status(status)
	_ = oauthDoneTemplate.Execute(c.Writer, data)
}
