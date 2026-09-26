package activate

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const feedPluginManifest = `schemaVersion: 1
id: acme.feeds
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME Feeds }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/feeds }
contributes:
  connectors:
    - id: feed
      name: { en-US: ACME Feed, zh-CN: ACME 订阅 }
      capabilities: [incremental]
      instanceSchema: schemas/feed.yaml
`

const feedSchema = `type: object
required: [feed_urls]
properties:
  token: { type: string, x-secret: true }
  feed_urls: { type: string, x-widget: textarea, x-group: settings }
`

// feedPlugin serves numbered items and resumes from the cursor.
type feedPlugin struct{}

func (feedPlugin) Validate(_ context.Context, _ *pluginsdk.Call, cfg pluginsdk.ConnectorConfig) error {
	if cfg.Settings["feed_urls"] == nil {
		return pluginapi.InvalidConfig("feed_urls is required", map[string]string{"settings.feed_urls": "required"})
	}
	if cfg.Credentials["token"] != "t-1" {
		return pluginapi.Errorf(pluginapi.CodeUnauthorized, "bad token")
	}
	return nil
}

func (feedPlugin) ListResources(
	_ context.Context,
	_ *pluginsdk.Call,
	cfg pluginsdk.ConnectorConfig,
	parent string,
) ([]pluginapi.Resource, error) {
	if parent != "" {
		return nil, nil
	}
	var out []pluginapi.Resource
	for _, u := range strings.Split(fmt.Sprint(cfg.Settings["feed_urls"]), "\n") {
		out = append(out, pluginapi.Resource{ExternalID: u, Name: u, Type: "feed"})
	}
	return out, nil
}

func (feedPlugin) Fetch(
	_ context.Context,
	_ *pluginsdk.Call,
	_ pluginsdk.ConnectorConfig,
	in pluginapi.FetchInput,
	s *pluginsdk.Stream,
) (*pluginapi.Cursor, error) {
	start := 0
	if in.Mode == pluginapi.FetchIncremental && in.Cursor != nil {
		start = int(in.Cursor.State["seen"].(float64))
	}
	for i := start; i < 3; i++ {
		item := pluginapi.FetchedItem{
			ExternalID: fmt.Sprint("post-", i), Title: fmt.Sprint("Post ", i), Content: []byte("# hi"),
		}
		if err := s.Item(item); err != nil {
			return nil, err
		}
		if err := s.Checkpoint(pluginapi.Cursor{State: map[string]any{"seen": i + 1}}); err != nil {
			return nil, err
		}
	}
	return &pluginapi.Cursor{State: map[string]any{"seen": 3}}, nil
}

type recordingHandler struct {
	items       []string
	checkpoints int
}

func (h *recordingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item.ExternalID+"|"+item.ContentType)
	return nil
}

func (h *recordingHandler) Checkpoint(context.Context, *types.SyncCursor) error {
	h.checkpoints++
	return nil
}

func TestPluginConnector(t *testing.T) {
	ctx := context.Background()
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.feeds", Version: "1.0.0"})
	plugin.Connector("feed", feedPlugin{})
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()

	p, err := pkg.Open(plugintest.Zip(t, map[string]string{
		"plugin.yaml": feedPluginManifest, "bin/feeds": "x", "schemas/feed.yaml": feedSchema,
	}))
	if err != nil {
		t.Fatal(err)
	}
	registry := datasource.NewConnectorRegistry()
	a := NewConnectors(NewInvoker(fakeClients{client.New(srv.URL, nil, nil)}), registry)
	if err := a.Activate(ctx, &reconcile.Loaded{Manifest: p.Manifest, Package: p}); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	var meta datasource.ConnectorMetadata
	for _, m := range registry.Metadata() {
		if m.Type == "acme.feeds/feed" {
			meta = m
		}
	}
	if meta.Names["zh-CN"] != "ACME 订阅" || meta.PluginID != "acme.feeds" ||
		meta.ConfigSchema.Properties["token"] == nil || meta.SettingsSchema.Properties["feed_urls"] == nil ||
		meta.ConfigSchema.Properties["feed_urls"] != nil {
		t.Fatalf("metadata = %+v", meta)
	}

	conn, err := registry.Get("acme.feeds/feed")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &types.DataSourceConfig{
		Credentials: map[string]any{"token": "t-1"},
		Settings:    map[string]any{"feed_urls": "https://a.example/rss\nhttps://b.example/rss"},
	}
	err = conn.Validate(ctx, &types.DataSourceConfig{Credentials: map[string]any{"token": "t-1"}})
	var pe *pluginapi.Error
	if err == nil || err.Error() != "feed_urls is required" ||
		!errors.As(err, &pe) || pe.Code != pluginapi.CodeInvalidConfig {
		t.Fatalf("Validate without settings = %v; want the plugin's message with its code reachable", err)
	}
	if err := conn.Validate(ctx, cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	res, err := conn.ListResources(ctx, cfg, "")
	if err != nil || len(res) != 2 || res[1].ExternalID != "https://b.example/rss" {
		t.Fatalf("ListResources = %+v, %v", res, err)
	}

	streaming := conn.(datasource.StreamingConnector)
	h := &recordingHandler{}
	cursor, err := streaming.FetchStream(ctx, cfg, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.items, ",") != "post-0|text/markdown,post-1|text/markdown,post-2|text/markdown" ||
		h.checkpoints != 3 || cursor.ConnectorCursor["seen"].(float64) != 3 || cursor.LastSyncTime.IsZero() {
		t.Fatalf("items=%v checkpoints=%d cursor=%+v", h.items, h.checkpoints, cursor)
	}
	cursor.ConnectorCursor["seen"] = float64(2)
	items, next, err := conn.FetchIncremental(ctx, cfg, cursor)
	if err != nil || len(items) != 1 || items[0].ExternalID != "post-2" ||
		next.ConnectorCursor["seen"].(float64) != 3 {
		t.Fatalf("incremental = %+v %+v %v", items, next, err)
	}

	if err := a.Deactivate(ctx, "acme.feeds"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get("acme.feeds/feed"); err == nil {
		t.Fatal("connector survived Deactivate")
	}
}

func TestPluginConnectorCannotShadowABuiltin(t *testing.T) {
	registry := datasource.NewConnectorRegistry()
	if err := registry.Register(&remoteConnector{typeID: "rss"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterPlugin(&remoteConnector{typeID: "rss"}, datasource.ConnectorMetadata{}); err == nil {
		t.Fatal("a plugin must not replace a builtin connector")
	}
	registry.Unregister("rss")
	if _, err := registry.Get("rss"); err != nil {
		t.Fatal("Unregister must not remove builtins")
	}
}
