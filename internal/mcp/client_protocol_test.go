package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestOutboundProtocolNegotiation(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, mode := range []string{"modern", "legacy", "silent-legacy", "unauthorized", "unavailable", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s := server.NewMCPServer("peer", "1", server.WithPaginationLimit(1))
			schema := json.RawMessage(`{
				"type":"object", "oneOf":[{"required":["x"]},{"required":["y"]}],
				"definitions":{"value":{"type":"string"}},
				"properties":{"x":{"$ref":"#/definitions/value"},"y":{"type":"string"}}
			}`)
			for _, name := range []string{"first", "second"} {
				s.AddTool(sdk.Tool{Name: name, RawInputSchema: schema},
					func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
						return sdk.NewToolResultText("ok"), nil
					})
			}
			handler := server.NewStreamableHTTPServer(s, server.WithStateLess(true))
			var mu sync.Mutex
			var methods []string
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					handler.ServeHTTP(w, r)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if json.Unmarshal(body, &request) != nil {
					t.Error("invalid JSON request")
					return
				}
				mu.Lock()
				methods = append(methods, request.Method)
				mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing bearer")
				}
				if mode == "unauthorized" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if mode == "unavailable" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				if request.Method == "server/discover" {
					switch mode {
					case "silent-legacy", "cancelled":
						<-r.Context().Done()
						return
					case "legacy":
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(map[string]any{
							"jsonrpc": "2.0", "id": request.ID,
							"error": map[string]any{"code": -32601, "message": "Method not found"},
						})
						return
					}
				}
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()
			c, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
				ID: "peer", Name: "peer", URL: &httpServer.URL, TransportType: types.MCPTransportHTTPStreamable,
				AuthConfig: &types.MCPAuthConfig{AuthType: types.MCPAuthBearer, Token: "fixture"},
			}})
			require.NoError(t, err)
			defer func() { require.NoError(t, c.Disconnect()) }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.NoError(t, c.Connect(ctx))
			if mode == "cancelled" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			result, err := c.Initialize(ctx)
			if mode == "unauthorized" || mode == "unavailable" || mode == "cancelled" {
				require.Error(t, err)
				require.Nil(t, result)
				require.False(t, c.(*mcpGoClient).initialized.Load())
				if mode == "cancelled" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
				mu.Lock()
				defer mu.Unlock()
				if mode != "cancelled" {
					require.Equal(t, []string{"server/discover", "initialize"}, methods)
				}
				return
			}
			require.NoError(t, err)
			wantVersion := sdk.LATEST_LEGACY_PROTOCOL_VERSION
			if mode == "modern" {
				wantVersion = sdk.LATEST_PROTOCOL_VERSION
			}
			require.Equal(t, wantVersion, result.ProtocolVersion)
			tools, err := c.ListTools(ctx)
			require.NoError(t, err)
			require.Len(t, tools, 2)
			for _, tool := range tools {
				require.JSONEq(t, string(schema), string(tool.InputSchema))
			}
			mu.Lock()
			listCount := 0
			for _, method := range methods {
				if method == "tools/list" {
					listCount++
				}
			}
			mu.Unlock()
			// The SDK emits a cursor for a full page, including the last one;
			// the third request reads the empty terminal page.
			require.Equal(t, 3, listCount, "all raw directory pages use the negotiated protocol")
			call, err := c.CallTool(ctx, "first", map[string]interface{}{"x": "value"})
			require.NoError(t, err)
			require.False(t, call.IsError)
			require.Equal(t, "ok", call.Content[0].Text)
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, "server/discover", methods[0])
			if mode == "modern" {
				require.NotContains(t, methods, "initialize")
			} else {
				require.Contains(t, methods, "initialize")
			}
		})
	}
}

func TestOutboundSSEProtocolNegotiation(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	s := server.NewMCPServer("sse-peer", "1")
	s.AddTool(sdk.NewTool("echo"), func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return sdk.NewToolResultText("ok"), nil
	})
	sse := server.NewSSEServer(s)
	httpServer := httptest.NewServer(sse)
	defer httpServer.Close()
	url := httpServer.URL + "/sse"
	c, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
		ID: "sse", Name: "sse", URL: &url, TransportType: types.MCPTransportSSE,
	}})
	require.NoError(t, err)
	defer func() { require.NoError(t, c.Disconnect()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	result, err := c.Initialize(ctx)
	require.NoError(t, err)
	t.Logf("SSE negotiated %s", result.ProtocolVersion)
	tools, err := c.ListTools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	call, err := c.CallTool(ctx, "echo", nil)
	require.NoError(t, err)
	require.False(t, call.IsError)
	require.Equal(t, "ok", call.Content[0].Text)
}
