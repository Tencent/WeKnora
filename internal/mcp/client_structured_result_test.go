package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestClientPreservesStructuredToolResult(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := sdkserver.NewMCPServer("results", "1", sdkserver.WithToolCapabilities(false))
	server.AddTool(
		sdkmcp.NewTool("lookup"),
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{sdkmcp.NewTextContent("not available")},
				StructuredContent: map[string]any{
					"rows":  []any{map[string]any{"id": "42", "active": false}},
					"total": 0,
				},
				IsError: true,
			}, nil
		},
	)
	upstream := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
	t.Cleanup(upstream.Close)
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	client, err := manager.GetOrCreateClient(context.Background(), &types.MCPService{
		ID: "results", Enabled: true, URL: &upstream.URL, TransportType: types.MCPTransportHTTPStreamable,
	})
	require.NoError(t, err)
	result, err := client.CallTool(context.Background(), "lookup", map[string]any{})
	require.NoError(t, err)
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{
  "content":[{"type":"text","text":"not available"}],
  "structuredContent":{"rows":[{"id":"42","active":false}],"total":0},
  "isError":true
}`, string(wire))
	legacy, err := json.Marshal(CallToolResult{Content: []ContentItem{{Type: "text", Text: "legacy"}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"content":[{"type":"text","text":"legacy"}]}`, string(legacy))
}
