package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestLoadMCPDirectoryUsesFreshSnapshotWithoutConnecting(t *testing.T) {
	ctx := catalogTestContext()
	service := &types.MCPService{ID: "svc", Name: "Orders", Enabled: true}
	want := []*types.MCPTool{{Name: "get_order", Description: "lookup"}}
	var gets atomic.Int32
	tools, instructions, err := loadMCPDirectory(ctx, service, nil, nil, nil, &MCPMetadataIO{
		Get: func(_ context.Context, tenant uint64, id string) (*types.MCPMetadata, error) {
			gets.Add(1)
			require.Equal(t, uint64(7), tenant)
			require.Equal(t, "svc", id)
			return &types.MCPMetadata{Tools: want, Instructions: "from-db"}, nil
		},
		Put: func(context.Context, uint64, string, []*types.MCPTool, string) error {
			t.Fatal("fresh snapshot must not be rewritten")
			return nil
		},
	}, false)
	require.NoError(t, err)
	require.Equal(t, int32(1), gets.Load())
	require.Equal(t, want, tools)
	require.Equal(t, "from-db", instructions)
}

func TestLoadMCPDirectoryDoesNotLiveFillStaleSnapshot(t *testing.T) {
	ctx := catalogTestContext()
	service := &types.MCPService{ID: "svc", Name: "Orders", Enabled: true}
	_, _, err := loadMCPDirectory(ctx, service, nil, nil, nil, &MCPMetadataIO{
		Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) {
			return &types.MCPMetadata{
				Stale: true,
				Tools: []*types.MCPTool{{Name: "old"}},
			}, nil
		},
		Put: func(context.Context, uint64, string, []*types.MCPTool, string) error {
			t.Fatal("stale snapshot must not live-fill")
			return nil
		},
	}, false)
	require.ErrorContains(t, err, "stale")
}

func TestLoadMCPDirectoryLiveFillsMissingNonOAuthDirectory(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := sdkserver.NewMCPServer(
		"Orders",
		"1",
		sdkserver.WithToolCapabilities(false),
		sdkserver.WithInstructions("live"),
	)
	server.AddTool(
		sdkmcp.Tool{Name: "get_order", Description: "lookup", RawInputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultText("ok"), nil
		},
	)
	upstream := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
	t.Cleanup(upstream.Close)
	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	service := &types.MCPService{
		ID:            "svc",
		Name:          "Orders",
		Enabled:       true,
		URL:           &upstream.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	var put atomic.Int32
	ctx := catalogTestContext()
	tools, instructions, err := loadMCPDirectory(ctx, service, manager, nil, nil, &MCPMetadataIO{
		Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) { return nil, nil },
		Put: func(_ context.Context, tenant uint64, id string, listed []*types.MCPTool, text string) error {
			put.Add(1)
			require.Equal(t, uint64(7), tenant)
			require.Equal(t, "svc", id)
			require.Equal(t, "live", text)
			require.Len(t, listed, 1)
			require.Equal(t, "get_order", listed[0].Name)
			return nil
		},
	}, false)
	require.NoError(t, err)
	require.Equal(t, int32(1), put.Load())
	require.Len(t, tools, 1)
	require.Equal(t, "get_order", tools[0].Name)
	require.Equal(t, "live", instructions)
}

func TestLoadMCPDirectoryOAuthMissingRequiresToolExecContext(t *testing.T) {
	service := &types.MCPService{
		ID:         "svc",
		Name:       "Orders",
		Enabled:    true,
		AuthConfig: &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth},
	}
	io := &MCPMetadataIO{
		Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) { return nil, nil },
		Put: func(context.Context, uint64, string, []*types.MCPTool, string) error {
			t.Fatal("OAuth preload must not persist a directory")
			return nil
		},
	}
	_, _, err := loadMCPDirectory(catalogTestContext(), service, nil, nil, nil, io, false)
	require.ErrorContains(t, err, "MCP directory is missing")

	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	execCtx := WithToolExecContext(catalogTestContext(), &ToolExecContext{ToolCallID: "describe-1"})
	_, _, err = loadMCPDirectory(execCtx, service, manager, nil, nil, io, false)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "MCP directory is missing")
}

func TestLoadMCPDirectoryExplicitRefreshRelistsExistingAndStaleSnapshots(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := sdkserver.NewMCPServer(
		"Orders",
		"1",
		sdkserver.WithToolCapabilities(false),
		sdkserver.WithInstructions("live"),
	)
	server.AddTool(
		sdkmcp.Tool{Name: "get_order", Description: "lookup", RawInputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultText("ok"), nil
		},
	)
	var upstream atomic.Int32
	inner := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(upstreamServer.Close)
	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	service := &types.MCPService{
		ID:            "svc",
		Name:          "Orders",
		Enabled:       true,
		URL:           &upstreamServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	var put atomic.Int32
	io := &MCPMetadataIO{
		Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) {
			t.Fatal("explicit refresh must not return the saved snapshot")
			return nil, nil
		},
		Put: func(_ context.Context, _ uint64, _ string, listed []*types.MCPTool, text string) error {
			put.Add(1)
			require.Equal(t, "live", text)
			require.Equal(t, "get_order", listed[0].Name)
			return nil
		},
	}
	ctx := catalogTestContext()
	tools, instructions, err := loadMCPDirectory(ctx, service, manager, nil, nil, io, true)
	require.NoError(t, err)
	require.Greater(t, upstream.Load(), int32(0))
	require.Equal(t, int32(1), put.Load())
	require.Equal(t, "get_order", tools[0].Name)
	require.Equal(t, "live", instructions)
}
