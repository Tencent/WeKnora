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
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
)

func newPluginHandlerTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	reg := registry.New()
	err := reg.Register(&manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		ID:            "weknora.feishu",
		Version:       "1.0.0",
		Name:          manifest.Text("Feishu", map[string]string{"zh-CN": "飞书"}),
		Publisher:     manifest.Publisher{ID: manifest.BuiltinPublisher},
		Builtin:       true,
		Runtime:       manifest.Runtime{Type: manifest.RuntimeBuiltin},
		Contributes: manifest.Contributions{
			manifest.PointConnectors: {{ID: "feishu", Name: manifest.Text("Feishu", nil), Aliases: []string{"feishu"}}},
			manifest.PointIMChannels: {{ID: "feishu", Name: manifest.Text("Feishu", nil), Aliases: []string{"feishu"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := NewPluginHandler(reg, nil)
	r.GET("/plugins", h.ListPlugins)
	r.GET("/plugins/contributions", h.ListContributions)
	r.GET("/plugins/:id", h.GetPlugin)
	return r
}

func getJSON(t *testing.T, r *gin.Engine, path string, out any) int {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, w.Body.String())
		}
	}
	return w.Code
}

func TestPluginHandlerListsPluginsAndContributions(t *testing.T) {
	r := newPluginHandlerTestRouter(t)

	var list struct {
		Data []struct {
			Manifest manifest.Manifest `json:"manifest"`
			Enabled  bool              `json:"enabled"`
		} `json:"data"`
	}
	if code := getJSON(t, r, "/plugins", &list); code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	if len(list.Data) != 1 || list.Data[0].Manifest.Name.Resolve("zh-CN") != "飞书" || !list.Data[0].Enabled {
		t.Fatalf("unexpected plugins: %+v", list.Data)
	}

	var contribs struct {
		Data struct {
			Points        []manifest.PointInfo        `json:"points"`
			Contributions map[string][]map[string]any `json:"contributions"`
		} `json:"data"`
	}
	if code := getJSON(t, r, "/plugins/contributions?point=connectors", &contribs); code != http.StatusOK {
		t.Fatalf("contributions status = %d", code)
	}
	if len(contribs.Data.Points) != 1 || len(contribs.Data.Contributions) != 1 {
		t.Fatalf("point filter not applied: %+v", contribs.Data)
	}
	got := contribs.Data.Contributions["connectors"]
	if len(got) != 1 || got[0]["qualifiedId"] != "weknora.feishu/feishu" || got[0]["pluginId"] != "weknora.feishu" {
		t.Fatalf("unexpected connectors: %+v", got)
	}
}

func TestPluginHandlerErrors(t *testing.T) {
	r := newPluginHandlerTestRouter(t)
	if code := getJSON(t, r, "/plugins/acme.missing", nil); code != http.StatusNotFound {
		t.Fatalf("missing plugin status = %d, want 404", code)
	}
	if code := getJSON(t, r, "/plugins/contributions?point=widgets", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown point status = %d, want 400", code)
	}
	if code := getJSON(t, r, "/plugins/weknora.feishu", nil); code != http.StatusOK {
		t.Fatalf("get plugin status = %d", code)
	}
}

type memPluginSettings struct {
	rows map[string]types.PluginTenantSetting
}

func (m *memPluginSettings) List(context.Context, uint64) ([]types.PluginTenantSetting, error) {
	out := make([]types.PluginTenantSetting, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memPluginSettings) Upsert(_ context.Context, s *types.PluginTenantSetting) error {
	m.rows[s.PluginID] = *s
	return nil
}

func TestPluginHandlerTogglesPluginsPerTenant(t *testing.T) {
	reg := registry.New()
	builtinPlugin := func(id string, required bool, point manifest.Point, localID string) *manifest.Manifest {
		return &manifest.Manifest{
			SchemaVersion: manifest.SchemaVersion, ID: id, Version: "1.0.0", Name: manifest.Text(id, nil),
			Publisher: manifest.Publisher{ID: manifest.BuiltinPublisher}, Builtin: true, Required: required,
			Runtime: manifest.Runtime{Type: manifest.RuntimeBuiltin},
			Contributes: manifest.Contributions{
				point: {{ID: localID, Name: manifest.Text(localID, nil)}},
			},
		}
	}
	for _, m := range []*manifest.Manifest{
		builtinPlugin("weknora.notion", false, manifest.PointConnectors, "notion"),
		builtinPlugin("weknora.agent-tools", true, manifest.PointTools, "thinking"),
	} {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	settings := &memPluginSettings{rows: map[string]types.PluginTenantSetting{}}
	h := NewPluginHandler(reg, tenancy.NewService(reg, settings))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), uint64(7)) })
	r.PUT("/plugins/:id/enabled", h.SetPluginEnabled)
	r.GET("/plugins/contributions", h.ListContributions)

	put := func(id, body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/plugins/"+id+"/enabled", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := put("weknora.notion", `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("disable status = %d", code)
	}
	if code := put("weknora.agent-tools", `{"enabled":false}`); code != http.StatusBadRequest {
		t.Fatalf("disabling a required plugin = %d, want 400", code)
	}
	if code := put("acme.missing", `{"enabled":false}`); code != http.StatusNotFound {
		t.Fatalf("unknown plugin = %d, want 404", code)
	}
	if code := put("weknora.notion", `{}`); code != http.StatusBadRequest {
		t.Fatalf("missing enabled = %d, want 400", code)
	}

	var contribs struct {
		Data struct {
			Contributions map[string][]map[string]any `json:"contributions"`
		} `json:"data"`
	}
	if code := getJSON(t, r, "/plugins/contributions?point=connectors", &contribs); code != http.StatusOK {
		t.Fatalf("contributions status = %d", code)
	}
	if got := contribs.Data.Contributions["connectors"]; len(got) != 1 || got[0]["enabled"] != false {
		t.Fatalf("disabled plugin's contribution should read disabled: %v", got)
	}
}
