package tools

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A panic raised by the MCP client on a catalog preload worker used to kill the
// whole process: the preload goroutines in mcp_exposure.go had no recover, and
// the recover sites in registry.go and act.go only cover their own goroutine.
// Before the fix this test never reaches its assertions — the test binary dies
// on prepareMCPToolsWithMode.func1.1.1 (mcp_exposure.go:139 -> snapshot -> load).
func TestMCPPreloadWorkerPanicIsContained(t *testing.T) {
	ctx := catalogTestContext()
	service := &types.MCPService{ID: "panic-mcp", Name: "Panic", Enabled: true}
	catalog := newMCPCatalog(
		ctx,
		[]*types.MCPService{service},
		nil,
		func(context.Context, *types.MCPService, bool) ([]*MCPTool, error) {
			// The loader is the seam snapshot() reaches the MCP client through.
			panic("test: MCP client panicked on a preload worker")
		},
		nil,
	)
	registry := NewToolRegistry()
	installMCPCatalog(registry, catalog)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		registry.PrepareMCPTools(ctx)
	}()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("PrepareMCPTools did not return after a preload worker panicked")
	}

	entry := catalog.servers[service.ID]
	require.NotNil(t, entry)
	entry.mu.Lock()
	status, tools := entry.status, entry.tools
	entry.mu.Unlock()
	require.Equal(t, "error", status, "a panicking service must be marked failed, not left loading")
	require.Empty(t, tools, "a panicking service must not keep a partial snapshot")
}

// The coordinator barrier records the services a panicking preload pass never
// finished and leaves a ready snapshot alone. Both reuse the existing "error"
// status snapshot() stores when a load fails; no new state is introduced.
func TestMCPPreloadFailureMarkingKeepsReadySnapshots(t *testing.T) {
	ctx := catalogTestContext()
	ready := &types.MCPService{ID: "ready-mcp", Name: "Ready", Enabled: true}
	loading := &types.MCPService{ID: "loading-mcp", Name: "Loading", Enabled: true}
	catalog := newMCPCatalog(ctx, []*types.MCPService{ready, loading}, nil, nil, nil)
	tool := NewMCPTool(ready, &types.MCPTool{Name: "ping"}, nil, nil, 0)
	catalog.servers[ready.ID].store(ready, []*MCPTool{tool}, "ready")
	catalog.servers[loading.ID].store(loading, nil, "loading")

	catalog.markPreloadFailed()

	readyEntry := catalog.servers[ready.ID]
	readyEntry.mu.Lock()
	readyStatus, readyTools := readyEntry.status, readyEntry.tools
	readyEntry.mu.Unlock()
	require.Equal(t, "ready", readyStatus, "a service that loaded fine keeps its snapshot")
	require.Len(t, readyTools, 1)

	loadingEntry := catalog.servers[loading.ID]
	loadingEntry.mu.Lock()
	loadingStatus, loadingTools := loadingEntry.status, loadingEntry.tools
	loadingEntry.mu.Unlock()
	require.Equal(t, "error", loadingStatus)
	require.Empty(t, loadingTools)
}
