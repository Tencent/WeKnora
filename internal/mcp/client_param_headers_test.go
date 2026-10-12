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

// Directory refreshes use a temporary client, while Agent calls may only have
// the persisted tool definition. Neither that refresh nor a retired connection
// can populate the schema cache of the connection that actually calls the tool.
func TestCallToolParamHeadersWithoutConnectionDiscovery(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, mode := range []string{"modern", "legacy"} {
		for _, scenario := range []string{"persisted directory", "reconnected"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				peer := newParamHeaderPeer(t, mode == "legacy")
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				manager := NewMCPManager(nil)
				defer manager.CloseAll()
				var directoryClient MCPClient
				var err error
				if scenario == "persisted directory" {
					directoryClient, err = NewMCPClient(&ClientConfig{Service: peer.service})
					require.NoError(t, err)
					require.NoError(t, directoryClient.Connect(ctx))
					_, err = directoryClient.Initialize(ctx)
				} else {
					directoryClient, err = manager.GetOrCreateClient(ctx, peer.service)
				}
				require.NoError(t, err)
				tools, err := directoryClient.ListTools(ctx)
				require.NoError(t, err)
				require.Len(t, tools, 2)
				require.Equal(t, "echo", tools[1].Name)
				if scenario == "persisted directory" {
					require.NoError(t, directoryClient.Disconnect())
				} else {
					require.NoError(t, manager.CloseClient(peer.service.ID))
				}

				c, err := manager.GetOrCreateClient(ctx, peer.service)
				require.NoError(t, err)
				beforeLists, _ := peer.counts()
				for _, value := range []string{"first", "second"} {
					// Use the saved definition without listing tools on this client.
					result, err := c.CallTool(ctx, tools[1].Name, map[string]interface{}{"value": value})
					require.NoError(t, err)
					require.False(t, result.IsError)
					require.Equal(t, "ok", result.Content[0].Text)
				}
				afterLists, headers := peer.counts()
				if mode == "modern" {
					require.Equal(t, []string{"first", "second"}, headers)
					// Two tools plus the empty terminal page. A second call must
					// reuse the complete cache instead of listing again.
					require.Equal(t, 3, afterLists-beforeLists)
				} else {
					require.Equal(t, []string{"", ""}, headers)
					require.Equal(t, beforeLists, afterLists, "legacy calls need no header discovery")
				}
			})
		}
	}
}

func TestCallToolParamHeadersDiscoveryFailure(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	peer := newParamHeaderPeer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewMCPManager(nil)
	defer manager.CloseAll()
	c, err := manager.GetOrCreateClient(ctx, peer.service)
	require.NoError(t, err)
	peer.mu.Lock()
	peer.failPage = true
	peer.mu.Unlock()
	_, err = c.CallTool(ctx, "echo", map[string]interface{}{"value": "first"})
	require.ErrorContains(t, err, "directory unavailable")
	lists, headers := peer.counts()
	require.Equal(t, 2, lists)
	require.Empty(t, headers, "do not execute with missing or partial call metadata")
	rawClient := c.(*managedMCPClient).MCPClient.(*mcpGoClient)
	rawClient.metadataMu.RLock()
	emptyCache := len(rawClient.toolSchemas) == 0
	rawClient.metadataMu.RUnlock()
	require.True(t, emptyCache, "a failed paginated discovery must not cache its first page")

	peer.mu.Lock()
	peer.failPage = false
	peer.mu.Unlock()
	result, err := c.CallTool(ctx, "echo", map[string]interface{}{"value": "retry"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	lists, headers = peer.counts()
	require.Equal(t, 5, lists)
	require.Equal(t, []string{"retry"}, headers)
}

func TestCallToolParamHeadersDiscoveryStopsBeforeExecution(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, scenario := range []string{"cancelled", "missing tool"} {
		t.Run(scenario, func(t *testing.T) {
			peer := newParamHeaderPeer(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			manager := NewMCPManager(nil)
			defer manager.CloseAll()
			c, err := manager.GetOrCreateClient(ctx, peer.service)
			require.NoError(t, err)
			name := "missing"
			if scenario == "cancelled" {
				name = "echo"
				cancel()
			}
			_, err = c.CallTool(ctx, name, map[string]interface{}{"value": "unused"})
			lists, headers := peer.counts()
			if scenario == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, lists)
			} else {
				require.ErrorContains(t, err, `tool "missing" not found`)
				require.Equal(t, 3, lists)
			}
			require.Empty(t, headers, "do not execute without a resolved schema")
		})
	}
}

type paramHeaderPeer struct {
	service  *types.MCPService
	mu       sync.Mutex
	lists    int
	headers  []string
	failPage bool
}

func (p *paramHeaderPeer) counts() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lists, append([]string(nil), p.headers...)
}

func newParamHeaderPeer(t *testing.T, legacy bool) *paramHeaderPeer {
	t.Helper()
	p := &paramHeaderPeer{}
	s := server.NewMCPServer("param-headers", "1", server.WithPaginationLimit(1))
	for _, name := range []string{"alpha", "echo"} {
		s.AddTool(sdk.Tool{Name: name, RawInputSchema: json.RawMessage(`{
			"type":"object","properties":{"value":{"type":"string","x-mcp-header":"Value"}}
		}`)}, func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return sdk.NewToolResultText("ok"), nil
		})
	}
	handler := server.NewStreamableHTTPServer(s, server.WithStateLess(true))
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Cursor string `json:"cursor"`
				} `json:"params"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			p.mu.Lock()
			if request.Method == "tools/list" {
				p.lists++
			}
			if request.Method == "tools/call" {
				p.headers = append(p.headers, r.Header.Get("Mcp-Param-Value"))
			}
			failPage := p.failPage && request.Method == "tools/list" && request.Params.Cursor != ""
			p.mu.Unlock()
			if (legacy && request.Method == "server/discover") || failPage {
				code, message := -32601, "Method not found"
				if failPage {
					code, message = -32603, "directory unavailable"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": request.ID,
					"error": map[string]any{"code": code, "message": message},
				})
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	p.service = &types.MCPService{
		ID: "param-headers", Name: "param-headers", Enabled: true,
		URL: &httpServer.URL, TransportType: types.MCPTransportHTTPStreamable,
	}
	return p
}
