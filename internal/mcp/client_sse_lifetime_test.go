package mcp

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestSSECallBudgetIsNotConnectionLifetime(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, age := range []time.Duration{0, 1500 * time.Millisecond} {
		t.Run(age.String(), func(t *testing.T) { testSSECallAfterIdle(t, age) })
	}
}

func testSSECallAfterIdle(t *testing.T, age time.Duration) {
	t.Helper()
	var executions atomic.Int32
	peer := server.NewMCPServer("lifetime-audit", "1")
	peer.AddTool(sdk.NewTool("slow", sdk.WithDescription("600 ms controlled read")),
		func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			executions.Add(1)
			time.Sleep(600 * time.Millisecond)
			return sdk.NewToolResultText("complete"), nil
		})
	httpServer := httptest.NewServer(server.NewSSEServer(peer))
	t.Cleanup(httpServer.Close)
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	url := httpServer.URL + "/sse"
	svc := &types.MCPService{
		ID: "lifetime", Name: "lifetime", Enabled: true, URL: &url,
		TransportType:  types.MCPTransportSSE,
		AdvancedConfig: &types.MCPAdvancedConfig{Timeout: 2},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	connectedAt := time.Now()
	c, err := manager.GetOrCreateClient(ctx, svc)
	require.NoError(t, err)
	_, err = c.ListTools(ctx)
	require.NoError(t, err)
	time.Sleep(age)
	require.True(t, c.IsConnected(), "connection must be healthy before the call")
	callCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	start := time.Now()
	result, err := c.CallTool(callCtx, "slow", map[string]interface{}{})
	t.Logf("connection_age=%s call_duration=%s executions=%d context_error=%v call_error=%v",
		time.Since(connectedAt), time.Since(start), executions.Load(), callCtx.Err(), err)
	require.NoError(t, err, "a 600 ms tool must fit its fresh 2 s call budget regardless of connection age")
	require.Equal(t, "complete", result.Content[0].Text)
}
