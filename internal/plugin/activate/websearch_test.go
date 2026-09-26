package activate

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const searchPluginManifest = `schemaVersion: 1
id: acme.search
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME Search }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/search }
contributes:
  webSearch:
    - id: brave
      name: { en-US: ACME Brave, zh-CN: ACME 勇敢搜索 }
      icon: icons/brave.svg
      instanceSchema: schemas/brave.yaml
`

// fakeClients serves one plugin from an in-process SDK handler.
type fakeClients struct{ c *client.Client }

func (f fakeClients) Client(string) (*client.Client, error) { return f.c, nil }

func searchLoaded(t *testing.T, schema string) *reconcile.Loaded {
	t.Helper()
	p, err := pkg.Open(plugintest.Zip(t, map[string]string{
		"plugin.yaml":        searchPluginManifest,
		"bin/search":         "x",
		"icons/brave.svg":    `<svg xmlns="http://www.w3.org/2000/svg"/>`,
		"schemas/brave.yaml": schema,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &reconcile.Loaded{Manifest: p.Manifest, Package: p}
}

const braveSchema = `type: object
required: [api_key, market]
properties:
  api_key: { type: string, title: API key }
  market: { type: string, enum: [en-US, zh-CN], default: en-US }
`

func TestPluginWebSearchProvider(t *testing.T) {
	ctx := context.Background()
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.search", Version: "1.0.0"})
	plugin.WebSearch(
		"brave",
		pluginsdk.WebSearchFunc(
			func(_ context.Context, call *pluginsdk.Call, in pluginapi.SearchInput) (*pluginapi.SearchOutput, error) {
				if call.Config.Instance["api_key"] != "k-1" || call.Config.Instance["market"] != "zh-CN" {
					return nil, pluginapi.Errorf(
						pluginapi.CodeUnauthorized,
						"bad instance config %v",
						call.Config.Instance,
					)
				}
				if in.Region != "" && in.Region != "CN" {
					return nil, pluginapi.Errorf(pluginapi.CodeInvalidConfig, "region %s unsupported", in.Region)
				}
				return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{
					Title: in.Query, URL: fmt.Sprint("https://acme.example/?tenant=", call.TenantID),
				}}}, nil
			},
		),
	)
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()

	registry := infra_web_search.NewRegistry()
	a := NewWebSearch(NewInvoker(fakeClients{client.New(srv.URL, nil, nil)}), registry)
	if err := a.Activate(ctx, searchLoaded(t, braveSchema)); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	pt, ok := registry.PluginType("acme.search/brave")
	if !ok {
		t.Fatal("type not registered")
	}
	info := pt.Info
	if info.Names["zh-CN"] != "ACME 勇敢搜索" || !info.RequiresAPIKey ||
		!strings.HasPrefix(info.Icon, "data:image/svg+xml;base64,") ||
		info.PluginID != "acme.search" {
		t.Fatalf("info = %+v", info)
	}
	if ak := info.ConfigSchema.Properties["api_key"]; ak == nil || !ak.Secret || ak.Group != "credentials" {
		t.Fatalf("api_key schema = %+v", ak)
	}
	if ec := info.ConfigSchema.Properties["extra_config"]; ec == nil || ec.Properties["market"] == nil {
		t.Fatalf("extra_config schema = %+v", ec)
	}
	if err := pt.Validate(types.WebSearchProviderParameters{APIKey: "k"}); err == nil {
		t.Fatal("a missing required extra_config field must fail validation")
	}
	params := types.WebSearchProviderParameters{APIKey: "k-1", ExtraConfig: map[string]string{"market": "zh-CN"}}
	if err := pt.Validate(params); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}

	provider, err := registry.CreateProvider("acme.search/brave", params)
	if err != nil {
		t.Fatal(err)
	}
	tctx := context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	res, err := provider.Search(tctx, "weknora", 3, false)
	if err != nil || len(res) != 1 || res[0].Title != "weknora" || res[0].URL != "https://acme.example/?tenant=7" ||
		res[0].Source != "acme.search/brave" {
		t.Fatalf("search = %+v, %v", res, err)
	}
	filtered := provider.(interfaces.FilteredWebSearchProvider)
	if _, err := filtered.SearchWithFilters(tctx, "q", 3, false, types.WebSearchFilters{Country: "DE"}); err == nil ||
		!strings.Contains(err.Error(), "region DE unsupported") {
		t.Fatalf("an unsupported filter must fail, got %v", err)
	}

	if err := a.Deactivate(ctx, "acme.search"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.PluginType("acme.search/brave"); ok {
		t.Fatal("type survived Deactivate")
	}
}

func TestPluginWebSearchSchemaRules(t *testing.T) {
	a := NewWebSearch(NewInvoker(fakeClients{}), infra_web_search.NewRegistry())
	for name, schema := range map[string]string{
		"secret field": "type: object\nproperties: { token: { type: string, x-secret: true } }\n",
		"number field": "type: object\nproperties: { limit: { type: integer } }\n",
	} {
		if err := a.Activate(context.Background(), searchLoaded(t, schema)); err == nil {
			t.Errorf("%s: want an activation error", name)
		}
	}
}
