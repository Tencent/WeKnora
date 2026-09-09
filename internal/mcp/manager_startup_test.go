package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestManagerConcurrentStartupCancellationAndConfigReplacement(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	started, release := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	var slowInitializations, fastInitializations atomic.Int32
	server := sdkserver.NewMCPServer("test", "1", sdkserver.WithInstructions("full server instructions"))
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &request)
		if request.Method == "initialize" {
			if r.URL.Path == "/slow" {
				slowInitializations.Add(1)
				startOnce.Do(func() { close(started) })
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			} else {
				fastInitializations.Add(1)
			}
		}
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	slowURL, fastURL := httpServer.URL+"/slow", httpServer.URL+"/fast"
	slow := &types.MCPService{ID: "slow", Enabled: true, URL: &slowURL, TransportType: types.MCPTransportHTTPStreamable}
	fast := &types.MCPService{ID: "fast", Enabled: true, URL: &fastURL, TransportType: types.MCPTransportHTTPStreamable}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := manager.GetOrCreateClient(ctx, slow); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow connection did not start")
	}
	fastCtx, fastCancel := context.WithTimeout(context.Background(), time.Second)
	defer fastCancel()
	client, err := manager.GetOrCreateClient(fastCtx, fast)
	require.NoError(t, err, "slow server must not hold the global manager lock")
	require.Equal(t, "full server instructions", client.(interface{ ServerInstructions() string }).ServerInstructions())
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("caller cancellation blocked")
	}
	releaseOnce.Do(func() { close(release) })
	readyCtx, readyCancel := context.WithTimeout(context.Background(), time.Second)
	defer readyCancel()
	_, err = manager.GetOrCreateClient(readyCtx, slow)
	require.NoError(t, err, "cancelling one waiter must not kill a shared connection")
	require.EqualValues(t, 1, slowInitializations.Load())
	snapshot := *fast
	snapshot.UpdatedAt = time.Now()
	replacement, err := manager.GetOrCreateClient(readyCtx, &snapshot)
	require.NoError(t, err)
	require.NotSame(t, client, replacement)
	require.EqualValues(t, 2, fastInitializations.Load())
}

func TestManagerCloseRetiresPendingConnection(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	service := &types.MCPService{
		ID:            "pending",
		Enabled:       true,
		URL:           &server.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	done := make(chan error, 1)
	go func() { _, err := manager.GetOrCreateClient(context.Background(), service); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("startup never reached server")
	}
	require.NoError(t, manager.CloseClient(service.ID))
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("retired startup still waiting")
	}
	require.Zero(t, manager.GetActiveClients())
}
