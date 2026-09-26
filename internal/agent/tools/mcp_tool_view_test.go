package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPluginToolViewTagsStructuredResults(t *testing.T) {
	svc := &types.MCPService{
		PluginID: "acme.jira", PluginVersion: "1.0.0",
		ToolViews: map[string]json.RawMessage{"search": json.RawMessage(`{"view":"table"}`)},
	}
	data := map[string]any{}
	addPluginToolView(data, svc, "search", map[string]any{"issues": []any{}})
	if data["display_type"] != PluginToolViewDisplayType || data["plugin_id"] != "acme.jira" ||
		data["mcp_tool"] != "search" || string(data["structured"].(json.RawMessage)) != `{"issues":[]}` {
		t.Fatalf("data = %v", data)
	}
	if !ShouldOmitRawToolOutput("", data) {
		t.Fatal("a rendered view replaces the raw output in the transcript")
	}
	for name, tc := range map[string]struct {
		tool       string
		structured any
	}{
		"no view for the tool":  {"other", map[string]any{}},
		"no structured content": {"search", nil},
		"too large":             {"search", strings.Repeat("x", maxPluginViewBytes)},
	} {
		data := map[string]any{}
		addPluginToolView(data, svc, tc.tool, tc.structured)
		if len(data) != 0 {
			t.Errorf("%s: %v", name, data)
		}
	}
	addPluginToolView(data, nil, "search", map[string]any{})
}
