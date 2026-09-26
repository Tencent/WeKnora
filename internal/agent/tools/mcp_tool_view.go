package tools

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
)

// PluginToolViewDisplayType marks a tool result the chat renders with the
// result view its plugin declared (toolViews in plugin.yaml).
const PluginToolViewDisplayType = "plugin_tool_view"

// maxPluginViewBytes caps the structured result kept for a view; larger
// results show as plain output.
const maxPluginViewBytes = 256 << 10

// addPluginToolView attaches a plugin's result view and the structured
// result it shows. The model still reads the text content.
func addPluginToolView(data map[string]any, svc *types.MCPService, tool string, structured any) {
	if svc == nil || structured == nil {
		return
	}
	view, ok := svc.ToolViews[tool]
	if !ok {
		return
	}
	raw, err := json.Marshal(structured)
	if err != nil || len(raw) > maxPluginViewBytes {
		return
	}
	data["display_type"] = PluginToolViewDisplayType
	data["plugin_view"] = view
	data["plugin_id"] = svc.PluginID
	data["plugin_version"] = svc.PluginVersion
	data["mcp_server"] = svc.PluginServer
	data["mcp_tool"] = tool
	data["structured"] = json.RawMessage(raw)
}
