package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

type protocolTestTransport struct {
	transport.HTTPConnection
	send func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error)
}

func (t *protocolTestTransport) SendRequest(
	ctx context.Context, request transport.JSONRPCRequest,
) (*transport.JSONRPCResponse, error) {
	return t.send(ctx, request)
}

func (*protocolTestTransport) Start(context.Context) error                          { return nil }
func (*protocolTestTransport) Close() error                                         { return nil }
func (*protocolTestTransport) SetProtocolVersion(string)                            {}
func (*protocolTestTransport) SetNotificationHandler(func(sdk.JSONRPCNotification)) {}
func (*protocolTestTransport) SendNotification(context.Context, sdk.JSONRPCNotification) error {
	return nil
}

type protocolTestSend func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error)

func newProtocolTestClient(t *testing.T, timeout int, send protocolTestSend) *mcpGoClient {
	t.Helper()
	return newProtocolProbeTestClient(t, timeout, send, send)
}

// The live connection and the independent re-probe connection get separate
// mock transports so tests can tell which one a request used.
func newProtocolProbeTestClient(t *testing.T, timeout int, live, probe protocolTestSend) *mcpGoClient {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	url := "http://127.0.0.1/mcp"
	c, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
		ID: "peer", Name: "peer", URL: &url, TransportType: types.MCPTransportHTTPStreamable,
		AdvancedConfig: &types.MCPAdvancedConfig{Timeout: timeout},
	}})
	require.NoError(t, err)
	wrapped := c.(*mcpGoClient)
	wrapped.discovery.HTTPConnection = &protocolTestTransport{send: live}
	newProbe := wrapped.newProbe
	wrapped.newProbe = func() (*mcpGoClient, error) {
		p, err := newProbe()
		if err != nil {
			return nil, err
		}
		p.discovery.HTTPConnection = &protocolTestTransport{send: probe}
		return p, nil
	}
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { require.NoError(t, c.Disconnect()) })
	return wrapped
}

func TestProtocolDiscoveryRetry(t *testing.T) {
	for _, mode := range []string{
		"method-not-found", "transient-unavailable", "transient-unauthorized", "transient-timeout",
		"silent-legacy", "unavailable", "cancelled", "cancelled-after-handshake",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			discoverCount, liveDiscovers, liveInitializes, probeInitializes := 0, 0, 0, 0
			peer := func(probe bool) protocolTestSend {
				return func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if !probe {
						switch request.Method {
						case "server/discover":
							liveDiscovers++
						case "initialize":
							liveInitializes++
						}
					} else if request.Method == "initialize" {
						probeInitializes++
					}
					return protocolRetryPeer(t, mode, &discoverCount, cancel)(ctx, request)
				}
			}
			c := newProtocolProbeTestClient(t, 1, peer(false), peer(true))
			result, err := c.Initialize(ctx)
			if mode == "cancelled" || mode == "cancelled-after-handshake" || mode == "unavailable" {
				require.Error(t, err)
				require.Nil(t, result)
				require.False(t, c.initialized.Load())
				require.Equal(t, 1, discoverCount)
				if mode != "unavailable" {
					require.ErrorIs(t, err, context.Canceled)
				}
				return
			}
			require.NoError(t, err)
			wantVersion := sdk.LATEST_PROTOCOL_VERSION
			if mode == "silent-legacy" || mode == "method-not-found" {
				wantVersion = sdk.LATEST_LEGACY_PROTOCOL_VERSION
			}
			require.Equal(t, wantVersion, result.ProtocolVersion)
			require.Equal(t, wantVersion, c.client.ProtocolVersion())
			if mode == "method-not-found" {
				require.Equal(t, 1, discoverCount)
			} else {
				require.Equal(t, 2, discoverCount)
			}
			require.Equal(t, 1, liveDiscovers, "the live session must never be probed again")
			require.Equal(t, 1, liveInitializes, "the live session must never be re-initialized")
			require.Zero(t, probeInitializes, "the probe must not open a second legacy session")
			_, err = c.ListTools(ctx)
			require.NoError(t, err)
			call, err := c.CallTool(ctx, "echo", map[string]interface{}{"x": "value"})
			require.NoError(t, err)
			require.Equal(t, "ok", call.Content[0].Text)
		})
	}
}

func protocolRetryPeer(t *testing.T, mode string, discoverCount *int, cancel context.CancelFunc) protocolTestSend {
	return func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
		result := func(raw string) (*transport.JSONRPCResponse, error) {
			return &transport.JSONRPCResponse{Result: json.RawMessage(raw)}, nil
		}
		switch request.Method {
		case "server/discover":
			*discoverCount++
			discoverCount := *discoverCount
			if mode == "method-not-found" {
				return &transport.JSONRPCResponse{Error: &sdk.JSONRPCErrorDetails{
					Code: -32601, Message: "Method not found",
				}}, nil
			}
			if mode == "silent-legacy" || (mode == "transient-timeout" && discoverCount == 1) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			if mode == "cancelled" {
				cancel()
				return nil, ctx.Err()
			}
			if discoverCount == 1 || mode == "unavailable" {
				if mode == "transient-unauthorized" {
					return nil, &transport.AuthorizationRequiredError{}
				}
				return nil, errors.New("request failed with status 503")
			}
			return result(`{"capabilities":{"tools":{}},"protocolVersions":["2026-07-28"]}`)
		case "initialize":
			if mode == "unavailable" {
				return nil, errors.New("request failed with status 503")
			}
			if mode == "cancelled-after-handshake" {
				cancel()
			}
			return result(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},` +
				`"serverInfo":{"name":"peer","version":"1"}}`)
		case "tools/list":
			return result(`{"tools":[{"name":"echo","inputSchema":{"type":"object",` +
				`"properties":{"x":{"type":"string","x-mcp-header":"Value"}}}}]}`)
		case "tools/call":
			wantHeader := "value"
			if mode == "silent-legacy" || mode == "method-not-found" {
				wantHeader = ""
			}
			require.Equal(t, wantHeader, request.Header.Get("Mcp-Param-Value"))
			return result(`{"content":[{"type":"text","text":"ok"}]}`)
		default:
			t.Fatalf("unexpected method %s", request.Method)
			return nil, nil
		}
	}
}

func TestProtocolDiscoveryRetryKeepsLegacySuccess(t *testing.T) {
	for _, mode := range []string{"retry-unavailable", "retry-cancelled", "retry-skipped"} {
		t.Run(mode, func(t *testing.T) {
			timeout := time.Second
			if mode == "retry-skipped" {
				// Leaves less than one 250ms probe after the first handshake.
				timeout = 500 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			discoverCount, initializeCount, probeDiscovers, probeInitializes, liveLists := 0, 0, 0, 0, 0
			probe := func(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
				switch request.Method {
				case "server/discover":
					discoverCount++
					probeDiscovers++
					if mode == "retry-cancelled" {
						cancel()
						return nil, ctx.Err()
					}
					// A stateful legacy peer rejects a sessionless request.
					return nil, transport.ErrSessionTerminated
				case "initialize":
					probeInitializes++
					return nil, errors.New("request failed with status 503")
				default:
					t.Fatalf("unexpected probe method %s", request.Method)
					return nil, nil
				}
			}
			c := newProtocolProbeTestClient(t, 1, func(
				ctx context.Context, request transport.JSONRPCRequest,
			) (*transport.JSONRPCResponse, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				switch request.Method {
				case "server/discover":
					discoverCount++
					return nil, errors.New("request failed with status 503")
				case "initialize":
					initializeCount++
					if initializeCount > 1 {
						return nil, errors.New("request failed with status 503")
					}
					if mode == "retry-skipped" {
						select {
						case <-time.After(300 * time.Millisecond):
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return &transport.JSONRPCResponse{Result: json.RawMessage(
						`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},` +
							`"serverInfo":{"name":"peer","version":"1"}}`,
					)}, nil
				case "tools/list":
					liveLists++
					return &transport.JSONRPCResponse{Result: json.RawMessage(
						`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`,
					)}, nil
				default:
					t.Fatalf("unexpected method %s", request.Method)
					return nil, nil
				}
			}, probe)
			result, err := c.Initialize(ctx)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, sdk.LATEST_LEGACY_PROTOCOL_VERSION, result.ProtocolVersion)
			require.Equal(t, sdk.LATEST_LEGACY_PROTOCOL_VERSION, c.client.ProtocolVersion())
			require.True(t, c.initialized.Load())
			require.Equal(t, 1, initializeCount, "the live session must never be re-initialized")
			require.Zero(t, probeInitializes, "the probe must not open a second legacy session")
			if mode == "retry-skipped" {
				require.Equal(t, 1, discoverCount)
				require.Zero(t, probeDiscovers)
			} else {
				require.Equal(t, 2, discoverCount)
				require.Equal(t, 1, probeDiscovers)
			}
			tools, err := c.ListTools(context.Background())
			require.NoError(t, err)
			require.Len(t, tools, 1)
			require.Equal(t, 1, liveLists, "the original connection keeps serving requests")
		})
	}
}

// statefulLegacyPeer behaves like a session-managed legacy Streamable HTTP
// server: every non-initialize request without its session gets a 404, which
// mcp-go reports as session termination and answers by clearing its session.
type statefulLegacyPeer struct {
	modernRetry bool
	mu          sync.Mutex
	discovers   int
	initializes int
	lists       int
	deleted     []string
}

func (p *statefulLegacyPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	session := r.Header.Get("Mcp-Session-Id")
	if r.Method == http.MethodDelete {
		p.mu.Lock()
		p.deleted = append(p.deleted, session)
		p.mu.Unlock()
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	reply := func(result string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request.ID, "result": json.RawMessage(result),
		})
	}
	modern := r.Header.Get("Mcp-Protocol-Version") == sdk.LATEST_PROTOCOL_VERSION
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case request.Method == "server/discover":
		p.discovers++
		switch {
		case p.discovers == 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case p.modernRetry:
			reply(`{"capabilities":{"tools":{}},"protocolVersions":["2026-07-28"]}`)
		case session == "":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	case request.Method == "initialize":
		p.initializes++
		if p.initializes > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-1")
		reply(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},` +
			`"serverInfo":{"name":"stateful","version":"1"}}`)
	case !modern && session != "sess-1":
		w.WriteHeader(http.StatusNotFound)
	case request.ID == nil:
		w.WriteHeader(http.StatusAccepted)
	case request.Method == "tools/list":
		p.lists++
		reply(`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func TestProtocolDiscoveryRetryKeepsStatefulLegacySession(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, mode := range []string{"probe-legacy", "probe-modern"} {
		t.Run(mode, func(t *testing.T) {
			peer := &statefulLegacyPeer{modernRetry: mode == "probe-modern"}
			httpServer := httptest.NewServer(peer)
			defer httpServer.Close()
			c, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
				ID: "stateful", Name: "stateful", URL: &httpServer.URL,
				TransportType:  types.MCPTransportHTTPStreamable,
				AdvancedConfig: &types.MCPAdvancedConfig{Timeout: 3},
			}})
			require.NoError(t, err)
			wrapped := c.(*mcpGoClient)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			require.NoError(t, c.Connect(ctx))
			result, err := c.Initialize(ctx)
			require.NoError(t, err)
			wantVersion := sdk.LATEST_LEGACY_PROTOCOL_VERSION
			if mode == "probe-modern" {
				wantVersion = sdk.LATEST_PROTOCOL_VERSION
			}
			require.Equal(t, wantVersion, result.ProtocolVersion)
			require.Equal(t, wantVersion, wrapped.client.ProtocolVersion())
			if mode == "probe-legacy" {
				require.Equal(t, "sess-1", wrapped.client.GetSessionId(), "the probe must not clear the live session")
			}
			tools, err := c.ListTools(ctx)
			require.NoError(t, err)
			require.Len(t, tools, 1)
			require.NoError(t, c.Disconnect())
			peer.mu.Lock()
			defer peer.mu.Unlock()
			require.Equal(t, 2, peer.discovers)
			require.Equal(t, 1, peer.initializes, "neither connection may initialize again")
			require.Equal(t, 1, peer.lists)
			require.Equal(t, []string{"sess-1"}, peer.deleted, "the legacy session is closed exactly once")
		})
	}
}

func TestOutboundProtocolNegotiation(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, mode := range []string{
		"modern", "legacy", "silent-legacy", "transient-unavailable", "transient-unauthorized",
		"transient-timeout", "unauthorized", "unavailable", "cancelled",
	} {
		t.Run(mode, func(t *testing.T) {
			wantModern := mode == "modern" || mode == "transient-unavailable" ||
				mode == "transient-unauthorized" || mode == "transient-timeout"
			s := server.NewMCPServer("peer", "1", server.WithPaginationLimit(1))
			schema := json.RawMessage(`{
				"type":"object", "oneOf":[{"required":["x"]},{"required":["y"]}],
				"definitions":{"value":{"type":"string"}},
				"properties":{"x":{"type":"string","x-mcp-header":"Value"},
				"y":{"$ref":"#/definitions/value"}}
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
			discoverCount := 0
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
				if request.Method == "server/discover" {
					discoverCount++
				}
				probe := discoverCount
				mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing bearer")
				}
				if request.Method == "tools/call" {
					if wantModern {
						if got := r.Header.Get("Mcp-Param-Value"); got != "value" {
							t.Errorf("Mcp-Param-Value = %q, want value", got)
						}
					} else {
						if got := r.Header.Get("Mcp-Param-Value"); got != "" {
							t.Errorf("legacy call added Mcp-Param-Value = %q", got)
						}
					}
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
					if probe == 1 {
						switch mode {
						case "transient-unavailable":
							http.Error(w, "unavailable", http.StatusServiceUnavailable)
							return
						case "transient-unauthorized":
							http.Error(w, "unauthorized", http.StatusUnauthorized)
							return
						case "transient-timeout":
							<-r.Context().Done()
							return
						}
					}
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
				AuthConfig:     &types.MCPAuthConfig{AuthType: types.MCPAuthBearer, Token: "fixture"},
				AdvancedConfig: &types.MCPAdvancedConfig{Timeout: 3},
			}})
			require.NoError(t, err)
			defer func() { require.NoError(t, c.Disconnect()) }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
			if wantModern {
				wantVersion = sdk.LATEST_PROTOCOL_VERSION
			}
			require.Equal(t, wantVersion, result.ProtocolVersion)
			require.Equal(t, wantVersion, c.(*mcpGoClient).client.ProtocolVersion())
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
				require.Equal(t, 1, discoverCount)
			} else {
				require.Contains(t, methods, "initialize")
				if mode == "legacy" {
					require.Equal(t, 1, discoverCount, "method-not-found must fall back without retry")
				} else {
					require.Equal(t, 2, discoverCount, "transport failure gets exactly one retry")
				}
			}
		})
	}
}

func TestOutboundSSEProtocolNegotiation(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, mode := range []string{
		"modern", "transient-unavailable", "transient-unauthorized", "transient-timeout", "silent-legacy",
	} {
		t.Run(mode, func(t *testing.T) {
			s := server.NewMCPServer("sse-peer", "1")
			s.AddTool(sdk.Tool{Name: "echo", RawInputSchema: json.RawMessage(`{
				"type":"object","properties":{"x":{"type":"string","x-mcp-header":"Value"}}
			}`)}, func(context.Context, sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return sdk.NewToolResultText("ok"), nil
			})
			sse := server.NewSSEServer(s)
			var mu sync.Mutex
			discoverCount, initializeCount := 0, 0
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					var request struct {
						Method string `json:"method"`
					}
					if json.Unmarshal(body, &request) != nil {
						t.Error("invalid JSON request")
						return
					}
					mu.Lock()
					if request.Method == "server/discover" {
						discoverCount++
					}
					if request.Method == "initialize" {
						initializeCount++
					}
					probe := discoverCount
					mu.Unlock()
					if request.Method == "server/discover" {
						if mode == "silent-legacy" || (mode == "transient-timeout" && probe == 1) {
							<-r.Context().Done()
							return
						}
						if probe == 1 && mode == "transient-unavailable" {
							http.Error(w, "unavailable", http.StatusServiceUnavailable)
							return
						}
						if probe == 1 && mode == "transient-unauthorized" {
							http.Error(w, "unauthorized", http.StatusUnauthorized)
							return
						}
					}
					if request.Method == "tools/call" && mode != "silent-legacy" &&
						r.Header.Get("Mcp-Param-Value") != "value" {
						t.Error("modern SSE call missing Mcp-Param-Value")
					}
				}
				sse.ServeHTTP(w, r)
			}))
			defer httpServer.Close()
			url := httpServer.URL + "/sse"
			c, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
				ID: "sse", Name: "sse", URL: &url, TransportType: types.MCPTransportSSE,
				AdvancedConfig: &types.MCPAdvancedConfig{Timeout: 3},
			}})
			require.NoError(t, err)
			defer func() { require.NoError(t, c.Disconnect()) }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			require.NoError(t, c.Connect(ctx))
			result, err := c.Initialize(ctx)
			require.NoError(t, err)
			wantVersion := sdk.LATEST_PROTOCOL_VERSION
			if mode == "silent-legacy" {
				wantVersion = sdk.LATEST_LEGACY_PROTOCOL_VERSION
			}
			require.Equal(t, wantVersion, result.ProtocolVersion)
			require.Equal(t, wantVersion, c.(*mcpGoClient).client.ProtocolVersion())
			tools, err := c.ListTools(ctx)
			require.NoError(t, err)
			require.Len(t, tools, 1)
			call, err := c.CallTool(ctx, "echo", map[string]interface{}{"x": "value"})
			require.NoError(t, err)
			require.False(t, call.IsError)
			require.Equal(t, "ok", call.Content[0].Text)
			mu.Lock()
			defer mu.Unlock()
			switch mode {
			case "modern":
				require.Equal(t, 1, discoverCount)
				require.Zero(t, initializeCount)
			default:
				// The re-probe uses its own SSE session and never initializes it.
				require.Equal(t, 2, discoverCount)
				require.Equal(t, 1, initializeCount)
			}
		})
	}
}
