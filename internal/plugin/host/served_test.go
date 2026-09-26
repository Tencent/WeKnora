package host

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func TestServedManifestCoversOwnMCPServers(t *testing.T) {
	want := &manifest.Manifest{ID: "acme.tools", Version: "1.0.0", Contributes: manifest.Contributions{
		manifest.PointMCPServers: {
			{ID: "issues"},
			{ID: "remote", MCP: &manifest.MCPServer{URL: "https://mcp.example.com"}},
		},
	}}
	got := &pluginapi.Manifest{
		ID: "acme.tools", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Contributes: map[string][]string{},
	}
	if err := CheckServedManifest(want, got); err == nil || !strings.Contains(err.Error(), "MCP server issues") {
		t.Fatalf("missing server = %v", err)
	}
	// Remote servers are not the plugin's to serve.
	got.Contributes["mcpServers"] = []string{"issues"}
	if err := CheckServedManifest(want, got); err != nil {
		t.Fatal(err)
	}
}
