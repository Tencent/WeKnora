package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
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
	h := NewPluginHandler(reg)
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
		Data []manifest.Manifest `json:"data"`
	}
	if code := getJSON(t, r, "/plugins", &list); code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	if len(list.Data) != 1 || list.Data[0].Name.Resolve("zh-CN") != "飞书" {
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
