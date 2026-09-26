package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// oneClient serves every plugin from one SDK handler.
type oneClient struct{ c *client.Client }

func (o oneClient) Client(context.Context, *manifest.Manifest) (*client.Client, error) {
	return o.c, nil
}
func (o oneClient) OnThisNode(string) bool { return true }

func uiPlugin(t *testing.T, rt manifest.RuntimeType) *reconcile.Loaded {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"ui/index.html": "<!doctype html><p>links</p>", "ui/app.js": "export {}", "main.py": "secret",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := &manifest.Manifest{
		ID: "acme.ui", Version: "1.0.0", Runtime: manifest.Runtime{Type: rt},
		Contributes: manifest.Contributions{
			manifest.PointPages:            {{ID: "links", Entry: "ui/index.html"}},
			manifest.PointSettingsSections: {{ID: "admin", Entry: "ui/index.html"}},
		},
	}
	return &reconcile.Loaded{Manifest: m, Dir: dir}
}

func uiEngine(t *testing.T, rt manifest.RuntimeType, role types.TenantRole) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	pages := activate.NewUIPages()
	if err := pages.Activate(context.Background(), uiPlugin(t, rt)); err != nil {
		t.Fatal(err)
	}
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.ui", Version: "1.0.0"})
	p.UI(func(_ context.Context, _ *pluginsdk.Call, in pluginapi.UIRequest) (*pluginapi.UIResponse, error) {
		return pluginsdk.UIJSON(201, map[string]string{
			"mount": in.Mount, "method": in.Method, "path": in.Path, "role": in.Role,
		})
	})
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	h := NewPluginUIHandler(pages, activate.NewInvoker(oneClient{client.New(srv.URL, nil, nil)}), nil)
	h.SetRoleGuard(func(need types.TenantRole) gin.HandlerFunc {
		return func(c *gin.Context) {
			if !role.HasPermission(need) {
				c.AbortWithStatus(http.StatusForbidden)
			}
		}
	})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.TenantRoleContextKey, role))
		c.Next()
		if len(c.Errors) > 0 && !c.Writer.Written() {
			c.Status(http.StatusNotFound)
		}
	})
	r.GET(PluginUIAssetsPrefix+"/:id/:version/*path", h.ServeAsset)
	r.POST("/plugins/:id/ui-request", h.Request)
	return r
}

func TestPluginPageFiles(t *testing.T) {
	r := uiEngine(t, manifest.RuntimeHost, types.TenantRoleViewer)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, PluginUIAssetsPrefix+path, nil))
		return w
	}
	w := get("/acme.ui/1.0.0/ui/index.html")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'none'") ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("index = %d %v", w.Code, w.Header())
	}
	w = get("/acme.ui/1.0.0/ui/app.js")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") ||
		w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("module = %d %v", w.Code, w.Header())
	}
	for _, path := range []string{
		"/acme.ui/1.0.0/main.py", "/acme.ui/1.0.0/ui/../main.py", "/acme.ui/0.9.0/ui/index.html",
		"/acme.other/1.0.0/ui/index.html", "/acme.ui/1.0.0/ui/missing.html",
	} {
		if w := get(path); w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, w.Code)
		}
	}
}

func uiRequest(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/plugins/acme.ui/ui-request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestPluginPageRequests(t *testing.T) {
	r := uiEngine(t, manifest.RuntimeHost, types.TenantRoleViewer)
	w := uiRequest(r, `{"mount":"pages/links","method":"put","path":"links","body":{"a":1}}`)
	var resp struct {
		Data pluginapi.UIResponse `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.Data.Status != 201 ||
		string(resp.Data.Body) != `{"method":"PUT","mount":"pages/links","path":"/links","role":"viewer"}` {
		t.Fatalf("page request = %d %s", w.Code, w.Body)
	}
	if w := uiRequest(r, `{"mount":"settingsSections/admin","path":"/"}`); w.Code != http.StatusForbidden {
		t.Fatalf("a viewer reached an admin page: %d", w.Code)
	}
	if w := uiRequest(r, `{"mount":"pages/nope","path":"/"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown mount = %d", w.Code)
	}

	admin := uiEngine(t, manifest.RuntimeHost, types.TenantRoleAdmin)
	if w := uiRequest(admin, `{"mount":"settingsSections/admin","path":"/"}`); w.Code != 200 {
		t.Fatalf("admin page for an admin = %d %s", w.Code, w.Body)
	}

	// A kubernetes deployment is served like a remote plugin.
	deployed := uiEngine(t, manifest.RuntimeKubernetes, types.TenantRoleViewer)
	if w := uiRequest(deployed, `{"mount":"pages/links","path":"/"}`); w.Code != 200 {
		t.Fatalf("a kubernetes plugin's page request = %d %s", w.Code, w.Body)
	}

	static := uiEngine(t, manifest.RuntimeDeclarative, types.TenantRoleViewer)
	if w := uiRequest(static, `{"mount":"pages/links","path":"/"}`); w.Code != http.StatusNotFound {
		t.Fatalf("a plugin without code = %d", w.Code)
	}
}
