package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/plugin/webhook"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func webhookEngine(t *testing.T) (*gin.Engine, *webhook.Tokens, *plugintest.MemTenantSettings) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	reg := registry.New()
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.hooks", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Hooks", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime: manifest.Runtime{Type: manifest.RuntimeHost, Kind: "binary", Entry: "bin/x"},
		Contributes: manifest.Contributions{
			manifest.PointWebhooks: {{ID: "inbox", Name: manifest.Text("Inbox", nil)}},
		},
	}
	if err := reg.Register(m); err != nil {
		t.Fatal(err)
	}
	settings := &plugintest.MemTenantSettings{}
	_ = settings.Upsert(
		context.Background(),
		&types.PluginTenantSetting{TenantID: 7, PluginID: "acme.hooks", Enabled: true},
	)

	p := pluginsdk.New(pluginsdk.Info{ID: "acme.hooks", Version: "1.0.0"})
	p.Webhook(
		"inbox",
		func(
			_ context.Context, call *pluginsdk.Call, req pluginapi.WebhookRequest,
		) (*pluginapi.WebhookResponse, error) {
			if req.Headers["X-Signature"] != "ok" {
				return &pluginapi.WebhookResponse{Status: 401}, nil
			}
			body, _ := json.Marshal(map[string]any{
				"tenant": call.TenantID, "method": req.Method, "path": req.Path, "query": req.Query,
				"body": string(req.Body), "cookie": req.Headers["Cookie"],
			})
			return &pluginapi.WebhookResponse{Status: 202, ContentType: "application/json", Body: body}, nil
		},
	)
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	tokens := webhook.NewTokens([]byte("k"))
	h := NewPluginWebhookHandler(reg, tenancy.NewService(reg, settings),
		activate.NewInvoker(oneClient{client.New(srv.URL, nil, nil)}), tokens)
	r := gin.New()
	r.Any(webhook.PathPrefix+"/:id/:hook/:token", h.Receive)
	r.Any(webhook.PathPrefix+"/:id/:hook/:token/*path", h.Receive)
	r.GET("/plugins/:id/webhooks", func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		h.List(c)
	})
	return r, tokens, settings
}

func call(r *gin.Engine, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(w, req)
	return w
}

func TestPluginWebhooksRelayCalls(t *testing.T) {
	r, tokens, settings := webhookEngine(t)
	url := tokens.Path("acme.hooks", "inbox", 7)
	signed := map[string]string{"X-Signature": "ok", "Cookie": "session=secret"}

	w := call(r, http.MethodPost, url+"/issues?since=1", `{"id":1}`, signed)
	if w.Code != 202 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("relay = %d %s", w.Code, w.Body)
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["tenant"] != float64(7) || got["path"] != "/issues" || got["query"] != "since=1" ||
		got["body"] != `{"id":1}` || got["cookie"] != "" {
		t.Fatalf("plugin saw %v", got)
	}
	if w := call(r, http.MethodPost, url, `{}`, nil); w.Code != 401 {
		t.Fatalf("the plugin's answer is relayed: %d", w.Code)
	}

	for _, bad := range []string{
		tokens.Path("acme.hooks", "inbox", 8) + "x",
		strings.Replace(url, "/inbox/", "/other/", 1),
		webhook.PathPrefix + "/acme.hooks/inbox/7.nope",
		tokens.Path("acme.nope", "inbox", 7),
	} {
		if w := call(r, http.MethodPost, bad, `{}`, signed); w.Code != http.StatusNotFound {
			t.Errorf("%s = %d", bad, w.Code)
		}
	}
	_ = settings.Upsert(
		context.Background(),
		&types.PluginTenantSetting{TenantID: 7, PluginID: "acme.hooks", Enabled: false},
	)
	if w := call(r, http.MethodPost, url, `{}`, signed); w.Code != http.StatusNotFound {
		t.Fatalf("a switched-off plugin's webhook = %d", w.Code)
	}
}

func TestPluginWebhookList(t *testing.T) {
	r, tokens, _ := webhookEngine(t)
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example.com/")
	w := call(r, http.MethodGet, "/plugins/acme.hooks/webhooks", "", nil)
	var resp struct {
		Data []PluginWebhookDTO `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &resp) != nil || len(resp.Data) != 1 {
		t.Fatalf("list = %s", w.Body)
	}
	want := tokens.Path("acme.hooks", "inbox", 7)
	if resp.Data[0].Path != want || resp.Data[0].URL != "https://weknora.example.com"+want {
		t.Fatalf("webhook = %+v", resp.Data[0])
	}
}
