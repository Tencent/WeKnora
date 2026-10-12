package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

type slowDisconnectServer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	starts  atomic.Int32
}

func (s *slowDisconnectServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		if r.URL.Path == "/slow" {
			s.once.Do(func() { close(s.started) })
			select {
			case <-s.release:
			case <-r.Context().Done():
			}
		}
		w.WriteHeader(http.StatusNoContent)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	switch request.Method {
	case "server/discover":
		response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
	case "initialize":
		if r.URL.Path == "/slow" {
			s.starts.Add(1)
		}
		w.Header().Set("Mcp-Session-Id", "session"+r.URL.Path)
		response["result"] = map[string]any{
			"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "disconnect-test", "version": "1"},
		}
	default:
		response["result"] = map[string]any{"tools": []any{}}
	}
	_ = json.NewEncoder(w).Encode(response)
}

type disconnectFixture struct {
	server  *slowDisconnectServer
	manager *MCPManager
	slow    *types.MCPService
	fast    *types.MCPService
	unblock func()
}

func newDisconnectFixture(t *testing.T) *disconnectFixture {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	s := &slowDisconnectServer{started: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewServer(s)
	manager := NewMCPManager(nil)
	unblock := sync.OnceFunc(func() { close(s.release) })
	t.Cleanup(func() {
		unblock()
		manager.Shutdown()
		server.Close()
	})
	slowURL, fastURL := server.URL+"/slow", server.URL+"/fast"
	f := &disconnectFixture{
		server: s, manager: manager, unblock: unblock,
		slow: &types.MCPService{
			ID: "slow", Enabled: true, URL: &slowURL,
			TransportType: types.MCPTransportHTTPStreamable,
		},
		fast: &types.MCPService{
			ID: "fast", Enabled: true, URL: &fastURL,
			TransportType: types.MCPTransportHTTPStreamable,
		},
	}
	_, err := manager.GetOrCreateClient(t.Context(), f.slow)
	require.NoError(t, err)
	_, err = manager.GetOrCreateClient(t.Context(), f.fast)
	require.NoError(t, err)
	return f
}

func TestManagerSlowDisconnectDoesNotBlockOtherServices(t *testing.T) {
	for _, operation := range []string{"close", "replace"} {
		t.Run(operation, func(t *testing.T) { testSlowDisconnectIsolation(t, operation) })
	}
}

func testSlowDisconnectIsolation(t *testing.T, operation string) {
	t.Helper()
	f := newDisconnectFixture(t)
	defer f.unblock()
	done := make(chan error, 1)
	go func() {
		if operation == "close" {
			done <- f.manager.CloseClient(f.slow.ID)
			return
		}
		snapshot := *f.slow
		snapshot.UpdatedAt = time.Now()
		_, err := f.manager.GetOrCreateClient(t.Context(), &snapshot)
		done <- err
	}()
	select {
	case <-f.server.started:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not reach the HTTP server")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	fastDone := make(chan error, 1)
	go func() {
		client, err := f.manager.GetOrCreateClient(ctx, f.fast)
		if err == nil {
			_, err = client.ListTools(ctx)
		}
		fastDone <- err
	}()
	select {
	case err := <-fastDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("an unrelated healthy MCP service is blocked by remote disconnect I/O")
	}
	f.unblock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not complete after the server recovered")
	}
}

func TestManagerReplacementWaiterCanCancelDuringDisconnect(t *testing.T) {
	f := newDisconnectFixture(t)
	defer f.unblock()
	snapshot := *f.slow
	snapshot.UpdatedAt = time.Now()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.manager.GetOrCreateClient(ctx, &snapshot); done <- err }()
	select {
	case <-f.server.started:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement did not start closing the old connection")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("caller cancellation is blocked by remote disconnect I/O")
	}
	require.EqualValues(t, 1, f.server.starts.Load(), "replacement must wait for old session cleanup")
	f.unblock()
	readyCtx, readyCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer readyCancel()
	client, err := f.manager.GetOrCreateClient(readyCtx, &snapshot)
	require.NoError(t, err, "one cancelled waiter must not cancel the shared replacement")
	require.True(t, client.IsConnected())
	require.EqualValues(t, 2, f.server.starts.Load())
}

func TestManagerCloseDoesNotRemoveConcurrentReplacement(t *testing.T) {
	f := newDisconnectFixture(t)
	defer f.unblock()
	done := make(chan error, 1)
	go func() { done <- f.manager.CloseClient(f.slow.ID) }()
	select {
	case <-f.server.started:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not reach the HTTP server")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	// The old client is already detached, so a later request can reconnect.
	client, err := f.manager.GetOrCreateClient(ctx, f.slow)
	require.NoError(t, err)
	f.unblock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("close did not finish after the server recovered")
	}
	current, ok := f.manager.GetClient(f.slow.ID)
	require.True(t, ok)
	require.Same(t, client, current, "old cleanup must not remove the newly cached connection")
}
