package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// expiringSessionServer is a Streamable HTTP MCP server on a session-based
// protocol version that forgets the session after initialization: the next
// request gets a 404, as after a server restart.
type expiringSessionServer struct {
	expired atomic.Bool
}

func (s *expiringSessionServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(body, &request)
	if request.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	reply := func(message map[string]any) {
		message["jsonrpc"] = "2.0"
		message["id"] = request.ID
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-1")
		_ = json.NewEncoder(w).Encode(message)
	}
	switch request.Method {
	case "server/discover":
		reply(map[string]any{"error": map[string]any{"code": -32601, "message": "Method not found"}})
	case "initialize":
		reply(map[string]any{"result": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "expiring", "version": "1.0.0"},
		}})
	default:
		if s.expired.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reply(map[string]any{"result": map[string]any{"tools": []any{}}})
	}
}

// A 404 for the session means the server no longer knows it, and the spec
// has the client start a new session. mcp-go reports it as
// transport.ErrSessionTerminated. The client disconnects on it, so that
// GetOrCreateClient builds a fresh connection instead of handing out the
// dead session again.
func TestMCPClientDisconnectsWhenTheSessionIsGone(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := &expiringSessionServer{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	serviceURL := httpServer.URL
	client, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
		ID:            "svc-1",
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &serviceURL,
	}})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, client.Connect(ctx))
	defer func() { _ = client.Disconnect() }()
	_, err = client.Initialize(ctx)
	require.NoError(t, err)
	_, err = client.ListTools(ctx)
	require.NoError(t, err)
	require.True(t, client.IsConnected())

	server.expired.Store(true)
	_, err = client.ListTools(ctx)
	require.Error(t, err)
	require.False(t, client.IsConnected(), "the client should disconnect so the next use reconnects")
}
