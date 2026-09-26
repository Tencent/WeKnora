package activate

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const toolsManifest = `schemaVersion: 1
id: acme.tools
version: 1.2.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME Tools }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/tools }
config: { tenant: tenant.yaml }
contributes:
  mcpServers:
    - id: issues
      name: ACME Issues
      toolViews:
        search:
          view: table
          items: issues
          columns: [key, { field: summary, title: { en-US: Summary }, link: url }]
`

// A tool a plugin serves itself is reached through plugin calls: the MCP
// client sees an ordinary server, and each call carries the workspace.
func TestPluginServedMCPServer(t *testing.T) {
	ctx := context.Background()
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.tools", Version: "1.2.0"})
	p.Tool("issues", pluginapi.Tool{Name: "search", Description: "Search issues"},
		func(_ context.Context, call *pluginsdk.Call, args json.RawMessage) (*pluginapi.ToolResult, error) {
			var in struct{ Q string }
			_ = json.Unmarshal(args, &in)
			return pluginapi.StructuredResult(map[string]any{
				"issues": []map[string]any{{"key": "ENG-1", "summary": in.Q}},
				"tenant": call.TenantID, "site": call.Config.Tenant["site"],
			}, "one issue"), nil
		})
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()

	opened, err := pkg.Open(plugintest.Zip(t, map[string]string{
		"plugin.yaml": toolsManifest, "bin/tools": "x",
		"tenant.yaml": "type: object\nproperties: { site: { type: string } }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(opened.Manifest); err != nil {
		t.Fatal(err)
	}
	settings := &plugintest.MemTenantSettings{}
	ten := tenancy.NewService(reg, settings)
	plugins := plugintest.NewMemRepo()
	_ = settings.Upsert(ctx, &types.PluginTenantSetting{TenantID: 7, PluginID: "acme.tools", Enabled: true})
	if _, err := ten.SetConfig(ctx, 7, "acme.tools", map[string]any{"site": "acme.example"}, "u"); err != nil {
		t.Fatal(err)
	}
	iv := NewInvoker(fakeClients{client.New(srv.URL, nil, nil)})
	iv.Bind(ten, plugins)
	a := NewMCPServers()
	a.Bind(ten, plugins)
	a.SetInvoker(iv)
	if err := a.Activate(ctx, &reconcile.Loaded{Manifest: opened.Manifest, Package: opened}); err != nil {
		t.Fatal(err)
	}

	svcs := a.Services(ctx, 7)
	if len(svcs) != 1 {
		t.Fatalf("services = %d", len(svcs))
	}
	svc := svcs[0]
	if svc.TransportType != types.MCPTransportPlugin || svc.URL != nil || !svc.Enabled ||
		svc.PluginVersion != "1.2.0" || !strings.Contains(string(svc.ToolViews["search"]), `"link":"url"`) {
		t.Fatalf("service = %+v", svc)
	}

	c, err := mcp.NewMCPClient(&mcp.ClientConfig{Service: svc})
	if err != nil {
		t.Fatal(err)
	}
	// The manager connects without a workspace in the context.
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	res, err := c.CallTool(ctx, "search", map[string]any{"q": "login"})
	if err != nil || res.IsError || res.Content[0].Text != "one issue" {
		t.Fatalf("call = %+v, %v", res, err)
	}
	got, _ := json.Marshal(res.StructuredContent)
	if string(got) != `{"issues":[{"key":"ENG-1","summary":"login"}],"site":"acme.example","tenant":7}` {
		t.Fatalf("structured = %s", got)
	}

	// Another workspace does not see a plugin it has not switched on.
	if len(a.Services(ctx, 8)) != 0 {
		t.Fatal("tenant 8 sees the plugin's server")
	}
	if err := a.Deactivate(ctx, "acme.tools"); err != nil {
		t.Fatal(err)
	}
	if _, err := mcp.NewMCPClient(&mcp.ClientConfig{Service: svc}); err == nil {
		t.Fatal("an unloaded plugin's server still connects")
	}
}
