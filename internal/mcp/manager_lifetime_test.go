package mcp

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// Report an expired cache entry while retaining a real HTTP session, so its
// cleanup still has a DELETE to perform against the slow-disconnect fixture.
type disconnectedCacheEntry struct{ MCPClient }

func (*disconnectedCacheEntry) IsConnected() bool { return false }

func TestManagerDisconnectedCleanupDoesNotBlockOrRemoveReplacement(t *testing.T) {
	f := newDisconnectFixture(t)
	defer f.unblock()
	f.manager.clientsMu.Lock()
	f.manager.clients[f.slow.ID] = &disconnectedCacheEntry{f.manager.clients[f.slow.ID]}
	f.manager.clientsMu.Unlock()
	done := make(chan struct{})
	go func() {
		f.manager.removeDisconnectedClients()
		close(done)
	}()
	select {
	case <-f.server.started:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not close the detached HTTP session")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	type connectionResult struct {
		client MCPClient
		err    error
	}
	connected := make(chan connectionResult, 1)
	go func() {
		fast, err := f.manager.GetOrCreateClient(ctx, f.fast)
		if err == nil {
			_, err = fast.ListTools(ctx)
		}
		if err != nil {
			connected <- connectionResult{err: err}
			return
		}
		replacement, err := f.manager.GetOrCreateClient(ctx, f.slow)
		connected <- connectionResult{client: replacement, err: err}
	}()
	var replacement MCPClient
	select {
	case result := <-connected:
		require.NoError(t, result.err)
		replacement = result.client
	case <-ctx.Done():
		t.Fatal("cleanup blocked another service or a replacement on remote teardown")
	}
	f.unblock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not finish after the server recovered")
	}
	current, exists := f.manager.GetClient(f.slow.ID)
	require.True(t, exists)
	require.Same(t, replacement, current, "old cleanup must not remove a concurrent replacement")
}

func TestManagerExpiredSessionReleasesLifetime(t *testing.T) {
	for _, eviction := range []string{"reconnect", "cleanup"} {
		t.Run(eviction, func(t *testing.T) {
			utils.SetSSRFWhitelistFromRaw("127.0.0.1")
			t.Cleanup(utils.ResetSSRFWhitelistForTest)
			peer := &expiringSessionServer{}
			server := httptest.NewServer(peer)
			t.Cleanup(server.Close)
			manager := NewMCPManager(nil)
			t.Cleanup(manager.Shutdown)
			serviceURL := server.URL
			service := &types.MCPService{
				ID: "expired-lifetime", Enabled: true, URL: &serviceURL,
				TransportType: types.MCPTransportHTTPStreamable,
			}
			client, err := manager.GetOrCreateClient(t.Context(), service)
			require.NoError(t, err)
			for range 3 {
				managed := client.(*managedMCPClient)
				var cancellations atomic.Int32
				cancel := managed.cancel
				managed.cancel = func() {
					cancellations.Add(1)
					cancel()
				}
				cached, err := manager.GetOrCreateClient(t.Context(), service)
				require.NoError(t, err)
				require.Same(t, client, cached, "a healthy connection must remain cached")
				require.Zero(t, cancellations.Load())

				peer.expired.Store(true)
				_, err = client.ListTools(t.Context())
				require.Error(t, err)
				require.False(t, client.IsConnected())
				require.Zero(t, cancellations.Load(), "transport teardown does not own the manager lifetime")
				if eviction == "cleanup" {
					manager.removeDisconnectedClients()
					require.EqualValues(t, 1, cancellations.Load(), "cleanup must release the evicted lifetime")
					_, exists := manager.GetClient(service.ID)
					require.False(t, exists)
				}
				peer.expired.Store(false)
				replacement, err := manager.GetOrCreateClient(t.Context(), service)
				require.NoError(t, err)
				require.NotSame(t, client, replacement)
				require.EqualValues(t, 1, cancellations.Load(), "replacement must release the previous lifetime")
				_, err = replacement.ListTools(t.Context())
				require.NoError(t, err)
				client = replacement
			}
		})
	}
}
