package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	pluginoauth "github.com/Tencent/WeKnora/internal/plugin/oauth"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func formsEngine(t *testing.T) (*gin.Engine, *plugintest.MemTenantSettings, *registry.Registry) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tenantSchema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{
		"site":  map[string]any{"type": "string"},
		"token": map[string]any{"type": "string", "x-secret": true},
		"project": map[string]any{
			"type":      "string",
			"x-options": map[string]any{"name": "projects", "dependsOn": []string{"site", "token"}},
		},
	}})
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.jira", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Jira", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime:     manifest.Runtime{Type: manifest.RuntimeHost, Kind: "binary", Entry: "bin/x"},
		Contributes: manifest.Contributions{manifest.PointWebSearch: {{ID: "x", Name: manifest.Text("X", nil)}}},
		Config:      manifest.ConfigSchemas{TenantSchema: tenantSchema},
	}
	reg := registry.New()
	if err := reg.Register(m); err != nil {
		t.Fatal(err)
	}
	settings := &plugintest.MemTenantSettings{}
	ten := tenancy.NewService(reg, settings)
	ctx := context.Background()
	_ = settings.Upsert(ctx, &types.PluginTenantSetting{TenantID: 7, PluginID: "acme.jira", Enabled: true})
	stored := map[string]any{"site": "old", "token": "stored-secret"}
	if _, err := ten.SetConfig(ctx, 7, "acme.jira", stored, "u"); err != nil {
		t.Fatal(err)
	}

	p := pluginsdk.New(pluginsdk.Info{ID: "acme.jira", Version: "1.0.0"})
	p.Options(
		"projects",
		func(_ context.Context, call *pluginsdk.Call, in pluginapi.OptionsInput) ([]pluginapi.Option, error) {
			if call.Config.Tenant["token"] != "stored-secret" {
				return nil, pluginapi.Errorf(pluginapi.CodeUnauthorized, "bad token %v", call.Config.Tenant["token"])
			}
			site, _ := call.Config.Tenant["site"].(string)
			return []pluginapi.Option{{Value: site + "/" + in.Query, Label: in.Field}}, nil
		},
	)
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	h := NewPluginFormsHandler(reg, ten, activate.NewInvoker(oneClient{client.New(srv.URL, nil, nil)}),
		plugintest.NewMemRepo(), nil, nil, nil)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/plugins/:id/options", func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		h.Options(c)
	})
	return r, settings, reg
}

func TestPluginFormOptions(t *testing.T) {
	r, settings, reg := formsEngine(t)
	post := func(body string) *httptest.ResponseRecorder {
		return call(
			r,
			http.MethodPost,
			"/plugins/acme.jira/options",
			body,
			map[string]string{"Content-Type": "application/json"},
		)
	}

	// The form's unsaved site is used; its redacted token is the stored one.
	w := post(
		`{"name":"projects","field":"project","scope":"tenant","query":"q","values":{"site":"new","token":"***"}}`,
	)
	var resp struct {
		Data pluginapi.OptionsOutput `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resp) != nil ||
		len(
			resp.Data.Options,
		) != 1 || resp.Data.Options[0].Value != "new/q" || resp.Data.Options[0].Label != "project" {
		t.Fatalf("options = %d %s", w.Code, w.Body)
	}

	// A token the user typed replaces the stored one; the plugin's refusal
	// reaches the form as a 400.
	w = post(`{"name":"projects","field":"project","scope":"tenant","values":{"token":"typed"}}`)
	if w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "unauthorized") {
		t.Fatalf("typed token = %d %s", w.Code, w.Body)
	}
	if w := post(`{"name":"nope","field":"project","scope":"tenant"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown source = %d %s", w.Code, w.Body)
	}
	if w := post(`{"name":"projects","field":"project","scope":"system"}`); w.Code != http.StatusForbidden {
		t.Fatalf("system scope for a workspace admin = %d", w.Code)
	}
	if w := post(`{"name":"projects","field":"project","scope":"other"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad scope = %d", w.Code)
	}
	_ = settings.Upsert(context.Background(),
		&types.PluginTenantSetting{TenantID: 7, PluginID: "acme.jira", Enabled: false}, "enabled")
	// The workspace configuration is filled in before switching the plugin
	// on; an integration instance needs it on.
	if w := post(`{"name":"projects","field":"project","scope":"tenant"}`); w.Code != http.StatusOK {
		t.Fatalf("workspace configuration of a switched-off plugin = %d %s", w.Code, w.Body)
	}
	if w := post(`{"name":"projects","field":"project","scope":"instance"}`); w.Code != http.StatusNotFound {
		t.Fatalf("an instance of a switched-off plugin = %d", w.Code)
	}
	reg.SetAudience("acme.jira", []uint64{8})
	if w := post(`{"name":"projects","field":"project","scope":"tenant"}`); w.Code != http.StatusNotFound {
		t.Fatalf("a plugin the workspace cannot see = %d", w.Code)
	}
}

func TestPluginOAuthCallbackPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := registry.New()
	h := NewPluginFormsHandler(reg, nil, nil, nil, nil, nil,
		pluginoauth.NewService(reg, plugintest.NewMemRepo(), nil, nil))
	r := gin.New()
	r.GET(pluginoauth.CallbackPath, h.OAuthCallback)
	w := call(r, http.MethodGet, pluginoauth.CallbackPath+"?state=%3C%2Fscript%3E&code=x", "", nil)
	body := w.Body.String()
	if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(body, "</script>'") || !strings.Contains(body, "ok:  false") {
		t.Fatalf("callback = %d %s", w.Code, body)
	}
	// Without a known state there is no origin to post the result to.
	if !strings.Contains(body, `window.opener && ""`) {
		t.Fatalf("callback posts to an origin: %s", body)
	}
}

func TestPluginOAuthResultAndDesktopOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := registry.New()
	h := NewPluginFormsHandler(reg, nil, nil, nil, nil, nil,
		pluginoauth.NewService(reg, plugintest.NewMemRepo(), nil, nil))
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.GET("/plugins/:id/oauth/result", h.OAuthResult)
	var res struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	w := call(r, http.MethodGet, "/plugins/acme.jira/oauth/result?state=nope", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Data.Status != "pending" {
		t.Fatalf("unknown state = %d %s", w.Code, w.Body.String())
	}
	if w := call(r, http.MethodGet, "/plugins/acme.jira/oauth/result", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("missing state = %d", w.Code)
	}

	t.Setenv("APP_EXTERNAL_URL", "")
	SetDesktopLoopbackURL("http://127.0.0.1:4321/")
	t.Cleanup(func() { SetDesktopLoopbackURL("") })
	for host, want := range map[string]string{
		"wails":            "http://127.0.0.1:4321",
		"wails.localhost":  "http://127.0.0.1:4321",
		"127.0.0.1:4321":   "http://127.0.0.1:4321",
		"192.168.1.9:4321": "http://192.168.1.9:4321",
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Request.Host = host
		origin, base := appOrigin(c)
		if base != want {
			t.Fatalf("%s: base = %q, want %q", host, base, want)
		}
		if (host == "wails" || host == "wails.localhost") != (origin == "") {
			t.Fatalf("%s: origin = %q; only the webview has none", host, origin)
		}
	}
}
