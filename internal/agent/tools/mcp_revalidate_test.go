package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// redeployableMCPServer is a stateless MCP server whose build can be replaced
// while clients stay connected, the way a redeployed server behind a stable
// address looks to WeKnora.
type redeployableMCPServer struct {
	handler atomic.Value // http.Handler
	// initializes counts connection handshakes; lastHandshake is when the latest
	// one arrived.
	initializes   atomic.Int32
	lastHandshake atomic.Value // time.Time
	// rejectCalls answers every tools/call with invalid params, as a server
	// does that validates arguments itself.
	rejectCalls atomic.Bool
	// handshakeGate, when set, holds every new connection's handshake until it
	// is closed; requests on open connections pass.
	handshakeGate atomic.Value // chan struct{}
	URL           string
}

func newRedeployableMCPServer(t *testing.T) *redeployableMCPServer {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	s := &redeployableMCPServer{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		// A connection starts with initialize, or with server/discover on the
		// 2026-07-28 protocol.
		if json.Unmarshal(body, &request) == nil && (request.Method == string(sdkmcp.MethodInitialize) ||
			request.Method == string(sdkmcp.MethodServerDiscover)) {
			s.initializes.Add(1)
			s.lastHandshake.Store(time.Now())
			if gate, ok := s.handshakeGate.Load().(chan struct{}); ok && gate != nil {
				<-gate
			}
		}
		if request.Method == string(sdkmcp.MethodToolsCall) && s.rejectCalls.Load() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(request.ID) +
				`,"error":{"code":-32602,"message":"order id must be a number"}}`))
			return
		}
		s.handler.Load().(http.Handler).ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	s.URL = upstream.URL
	return s
}

// deploy replaces the running build. Every tool answers with the version.
func (s *redeployableMCPServer) deploy(version, instructions string, tools ...string) {
	server := sdkserver.NewMCPServer(
		"Orders", version, sdkserver.WithToolCapabilities(false), sdkserver.WithInstructions(instructions),
	)
	for _, name := range tools {
		server.AddTool(
			sdkmcp.Tool{
				Name: name, Description: name + " " + version, RawInputSchema: json.RawMessage(`{"type":"object"}`),
			},
			func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return sdkmcp.NewToolResultText("ok " + version), nil
			},
		)
	}
	s.handler.Store(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
}

func (s *redeployableMCPServer) service() *types.MCPService {
	return &types.MCPService{
		ID: "svc", Name: "Orders", Enabled: true, URL: &s.URL, TransportType: types.MCPTransportHTTPStreamable,
	}
}

// memoryDirectoryStore stands in for the metadata service.
type memoryDirectoryStore struct {
	mu       sync.Mutex
	snapshot *types.MCPMetadata
	puts     int
	listedAt []time.Time
}

func (m *memoryDirectoryStore) save(snapshot *types.MCPMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = snapshot
}

func (m *memoryDirectoryStore) current() (types.MCPMetadata, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot == nil {
		return types.MCPMetadata{}, m.puts
	}
	return *m.snapshot, m.puts
}

func (m *memoryDirectoryStore) io(revalidator *MCPDirectoryRevalidator) *MCPMetadataIO {
	return &MCPMetadataIO{
		Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.snapshot == nil {
				return nil, nil
			}
			copied := *m.snapshot
			return &copied, nil
		},
		Put: func(
			_ context.Context, _ uint64, _ string,
			listed []*types.MCPTool, instructions string, server types.MCPServerInfo, listedAt time.Time,
		) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.puts++
			m.listedAt = append(m.listedAt, listedAt)
			m.snapshot = &types.MCPMetadata{
				Tools: listed, Instructions: instructions,
				ServerName: server.Name, ServerVersion: server.Version, ServerDescription: server.Description,
				SyncedAt: listedAt,
			}
			return nil
		},
		Revalidator: revalidator,
	}
}

// waitForPuts waits for a background re-list to store n directories.
func (m *memoryDirectoryStore) waitForPuts(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool { _, puts := m.current(); return puts == n }, 5*time.Second, 10*time.Millisecond)
}

func savedDirectory(version, instructions string, syncedAt time.Time, tools ...string) *types.MCPMetadata {
	snapshot := &types.MCPMetadata{
		Instructions: instructions, ServerName: "Orders", ServerVersion: version, SyncedAt: syncedAt,
	}
	for _, name := range tools {
		snapshot.Tools = append(snapshot.Tools, &types.MCPTool{
			Name: name, Description: name + " " + version, InputSchema: json.RawMessage(`{"type":"object"}`),
		})
	}
	return snapshot
}

func toolNames(tools []*types.MCPTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// revalidatingCatalog registers service against store and returns its catalog.
func revalidatingCatalog(
	t *testing.T, service *types.MCPService, store *memoryDirectoryStore, maxAge time.Duration,
) (*MCPCatalog, *internalmcp.MCPManager) {
	t.Helper()
	return engineCatalog(t, service, store, NewMCPDirectoryRevalidator(maxAge))
}

// engineCatalog registers service for one agent engine; engines of the same
// process share one revalidator.
func engineCatalog(
	t *testing.T, service *types.MCPService, store *memoryDirectoryStore, revalidator *MCPDirectoryRevalidator,
) (*MCPCatalog, *internalmcp.MCPManager) {
	t.Helper()
	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	registry := NewToolRegistry()
	_, err := RegisterMCPTools(
		catalogTestContext(), registry, []*types.MCPService{service}, manager, nil, 0, nil, store.io(revalidator),
	)
	require.NoError(t, err)
	return registry.mcpCatalog(), manager
}

func catalogTool(t *testing.T, catalog *MCPCatalog, name string) *MCPTool {
	t.Helper()
	tools, _, err := catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	for _, tool := range tools {
		if tool.mcpTool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not in the catalog", name)
	return nil
}

func TestRevalidatorCooldownCountsEveryAttempt(t *testing.T) {
	r := NewMCPDirectoryRevalidator(time.Hour)
	clock := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return clock }
	var runs int
	failing := func(context.Context) ([]*types.MCPTool, error) { runs++; return nil, errors.New("server unavailable") }
	triggered := func(key string, relist func(context.Context) ([]*types.MCPTool, error)) bool {
		_, ok := r.trigger(context.Background(), key, "test", true, relist)
		return ok
	}

	require.False(t, triggered("k", failing), "a failed re-list is not reported as done")
	require.False(t, triggered("k", failing))
	require.Equal(t, 1, runs, "a failed attempt still starts the cooldown")
	require.False(t, triggered("other", failing))
	require.Equal(t, 2, runs, "the cooldown is per directory")

	clock = clock.Add(mcpRevalidateCooldown + time.Second)
	listed := []*types.MCPTool{{Name: "get_order"}}
	tools, ok := r.trigger(context.Background(), "k", "test", true, func(context.Context) ([]*types.MCPTool, error) {
		runs++
		return listed, nil
	})
	require.True(t, ok)
	require.Equal(t, listed, tools)
	require.Equal(t, 3, runs)
}

func TestRevalidatorWaitingCallerSharesTheRunningAttempt(t *testing.T) {
	r := NewMCPDirectoryRevalidator(time.Hour)
	joined := make(chan struct{})
	r.joined = func(string) { close(joined) }
	release := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	var runs atomic.Int32
	listed := []*types.MCPTool{{Name: "get_order"}}
	relist := func(context.Context) ([]*types.MCPTool, error) {
		runs.Add(1)
		startOnce.Do(func() { close(started) })
		<-release
		return listed, nil
	}
	type result struct {
		tools []*types.MCPTool
		ok    bool
	}
	run := func(out chan<- result) {
		tools, ok := r.trigger(context.Background(), "k", "test", true, relist)
		out <- result{tools, ok}
	}
	first, second := make(chan result), make(chan result)
	go run(first)
	<-started
	go run(second)
	<-joined
	close(release)
	require.True(t, (<-first).ok)
	got := <-second
	require.True(t, got.ok, "a caller that found the attempt running waits for its result")
	require.Equal(t, listed, got.tools)
	require.Equal(t, int32(1), runs.Load())
}

func TestRevalidatorBoundsAWaitedRelist(t *testing.T) {
	r := NewMCPDirectoryRevalidator(time.Hour)
	r.timeout = 50 * time.Millisecond
	started := time.Now()
	_, ok := r.trigger(context.Background(), "k", "test", true, func(ctx context.Context) ([]*types.MCPTool, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.False(t, ok)
	require.Less(t, time.Since(started), 5*time.Second, "a stalled server must not hold the failed call open")
}

func TestRevalidatorBackgroundRunOutlivesTheRequest(t *testing.T) {
	r := NewMCPDirectoryRevalidator(time.Hour)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)))
	done := make(chan uint64, 1)
	_, ok := r.trigger(ctx, "k", "test", false, func(runCtx context.Context) ([]*types.MCPTool, error) {
		<-time.After(20 * time.Millisecond)
		tenant, _ := types.TenantIDFromContext(runCtx)
		if runCtx.Err() == nil {
			done <- tenant
		}
		return nil, nil
	})
	require.False(t, ok, "a background run does not report a result")
	cancel()
	select {
	case tenant := <-done:
		require.Equal(t, uint64(7), tenant, "the background run keeps the request's values")
	case <-time.After(5 * time.Second):
		t.Fatal("the background re-list was cancelled with the request")
	}
}

func TestOldSavedDirectoryIsServedAndRelistedInBackground(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("2", "new guidance", "get_order", "track_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "old guidance", time.Now().Add(-2*time.Hour), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)

	tools, _, err := catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	require.Len(t, tools, 1, "the saved directory is served without waiting for the server")
	store.waitForPuts(t, 1)
	saved, _ := store.current()
	require.Equal(t, []string{"get_order", "track_order"}, toolNames(saved.Tools))
	require.Equal(t, "new guidance", saved.Instructions)
	require.Equal(t, "2", saved.ServerVersion)
	store.mu.Lock()
	listedAt := store.listedAt[0]
	store.mu.Unlock()
	handshake, _ := server.lastHandshake.Load().(time.Time)
	require.True(t, !listedAt.IsZero() && !listedAt.After(handshake),
		"a re-list is dated by when it started, as a settings refresh is: listed at %v, handshake at %v",
		listedAt, handshake)

	tools, _, err = catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	require.Len(t, tools, 2, "the next read in the same engine picks up the re-listed directory")
	require.Equal(t, "new guidance", tools[0].serverInstructions)
}

func TestRecentOrUndatedSavedDirectoryIsNotRelisted(t *testing.T) {
	for name, tc := range map[string]struct {
		syncedAt time.Time
		maxAge   time.Duration
	}{
		"recent":        {syncedAt: time.Now().Add(-time.Minute), maxAge: time.Hour},
		"undated":       {maxAge: time.Hour},
		"age check off": {syncedAt: time.Now().Add(-48 * time.Hour), maxAge: 0},
	} {
		t.Run(name, func(t *testing.T) {
			server := newRedeployableMCPServer(t)
			server.deploy("2", "new guidance", "get_order")
			store := &memoryDirectoryStore{}
			store.save(savedDirectory("1", "old guidance", tc.syncedAt, "get_order"))
			catalog, _ := revalidatingCatalog(t, server.service(), store, tc.maxAge)
			_, _, err := catalog.snapshot(catalogTestContext(), "svc", false)
			require.NoError(t, err)
			time.Sleep(100 * time.Millisecond)
			_, puts := store.current()
			require.Zero(t, puts)
			require.Zero(t, server.initializes.Load(), "reading a saved directory does not connect")
		})
	}
}

func TestCallOnConnectionToChangedServerRelistsInBackground(t *testing.T) {
	for name, tc := range map[string]struct {
		savedVersion, liveVersion, liveInstructions string
	}{
		"version changed":          {savedVersion: "1", liveVersion: "2", liveInstructions: "guidance"},
		"instructions changed":     {savedVersion: "1", liveVersion: "1", liveInstructions: "new guidance"},
		"version was not recorded": {savedVersion: "", liveVersion: "1", liveInstructions: "guidance"},
	} {
		t.Run(name, func(t *testing.T) {
			server := newRedeployableMCPServer(t)
			server.deploy(tc.liveVersion, tc.liveInstructions, "get_order")
			store := &memoryDirectoryStore{}
			store.save(savedDirectory(tc.savedVersion, "guidance", time.Now().Add(-time.Minute), "get_order"))
			catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)

			result, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
			require.NoError(t, err)
			require.True(t, result.Success)
			require.Contains(t, result.Output, "ok "+tc.liveVersion, "the call itself runs unchanged")
			store.waitForPuts(t, 1)
			saved, _ := store.current()
			require.Equal(t, tc.liveVersion, saved.ServerVersion)
			require.Equal(t, tc.liveInstructions, saved.Instructions)
		})
	}
}

func TestCallMatchingSavedDirectoryDoesNotRelist(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("1", "guidance", "get_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "guidance", time.Now().Add(-time.Minute), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)

	result, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	time.Sleep(100 * time.Millisecond)
	_, puts := store.current()
	require.Zero(t, puts)
}

func TestConnectionOlderThanSavedDirectoryIsNotEvidence(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("1", "guidance", "get_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "guidance", time.Now().Add(-time.Minute), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)
	_, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)

	// The server is redeployed while the cached connection stays open, and a
	// re-list over a fresh connection saves what the new build reports. The
	// cached connection still holds the old build's identity, which must not
	// read as a change, or every call would start another re-list.
	server.deploy("2", "new guidance", "get_order")
	store.save(savedDirectory("2", "new guidance", time.Now(), "get_order"))
	catalog.markForReload("svc")
	result, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	time.Sleep(100 * time.Millisecond)
	_, puts := store.current()
	require.Zero(t, puts)
	require.Equal(t, int32(1), server.initializes.Load(), "the cached connection is reused")
}

func TestCallRejectedAsUnknownToolRelistsBeforeAnswering(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("2", "guidance", "get_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("2", "guidance", time.Now().Add(-time.Minute), "get_order", "cancel_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)

	result, err := catalogTool(t, catalog, "cancel_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "has been refreshed")
	saved, puts := store.current()
	require.Equal(t, 1, puts, "the directory is replaced before the model sees the error")
	require.Equal(t, []string{"get_order"}, toolNames(saved.Tools))

	tools, _, err := catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	require.Len(t, tools, 1, "rediscovery in the same engine sees the server's tools")

	// Another rejected call within the cooldown does not list again.
	result, err = catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	current := catalogTool(t, catalog, "get_order")
	stale := NewMCPTool(current.service, &types.MCPTool{
		Name: "cancel_order", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, current.mcpManager, nil, 0)
	stale.serverInstructions, stale.serverVersion = current.serverInstructions, current.serverVersion
	stale.savedAt, stale.revalidate = current.savedAt, current.revalidate
	result, err = stale.Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotContains(t, result.Error, "has been refreshed")
	_, puts = store.current()
	require.Equal(t, 1, puts)
}

func TestToolErrorResultDoesNotRelist(t *testing.T) {
	server := newRedeployableMCPServer(t)
	mcpServer := sdkserver.NewMCPServer(
		"Orders", "1", sdkserver.WithToolCapabilities(false), sdkserver.WithInstructions("guidance"),
	)
	mcpServer.AddTool(
		sdkmcp.Tool{Name: "get_order", RawInputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultError("order not found"), nil
		},
	)
	server.handler.Store(sdkserver.NewStreamableHTTPServer(mcpServer, sdkserver.WithStateLess(true)))
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "guidance", time.Now().Add(-time.Minute), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)

	result, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "order not found")
	require.NotContains(t, result.Error, "has been refreshed")
	time.Sleep(100 * time.Millisecond)
	_, puts := store.current()
	require.Zero(t, puts)
}

func TestRegisteredToolCarriesSavedDirectoryIdentity(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("2", "guidance", "get_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "guidance", time.Now().Add(-time.Minute), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)
	tool := catalogTool(t, catalog, "get_order")
	registered := &MCPRegisteredTool{MCPTool: tool, catalog: catalog, ref: mcpToolRef(tool)}

	result, err := registered.Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	store.waitForPuts(t, 1)
	saved, _ := store.current()
	require.Equal(t, "2", saved.ServerVersion)
}

func TestRelistReadsTheRedeployedServerOverAFreshConnection(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("1", "guidance", "get_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "guidance", time.Now().Add(-time.Minute), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)
	_, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
	require.NoError(t, err)

	// A stateless server redeployed behind the same address leaves the cached
	// connection open, still holding the old build's initialize result. Once
	// the saved directory ages out, the re-list must report the new build.
	server.deploy("2", "new guidance", "get_order")
	store.save(savedDirectory("1", "guidance", time.Now().Add(-2*time.Hour), "get_order"))
	catalog.markForReload("svc")
	_, _, err = catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	store.waitForPuts(t, 1)
	saved, _ := store.current()
	require.Equal(t, "2", saved.ServerVersion)
	require.Equal(t, "new guidance", saved.Instructions)
}

func TestCallRejectedForArgumentsAgainstCurrentDirectoryGetsNoRediscoveryHint(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("1", "guidance", "get_order")
	server.rejectCalls.Store(true)
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("1", "guidance", time.Now().Add(-time.Minute), "get_order"))
	catalog, _ := revalidatingCatalog(t, server.service(), store, time.Hour)

	result, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{"id":"x"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "order id must be a number")
	require.NotContains(t, result.Error, "has been refreshed",
		"the tool is unchanged on the server, so the model should fix its arguments instead")
	_, puts := store.current()
	require.Equal(t, 1, puts, "the directory is still checked once")
}

func TestEngineJoiningARunningRelistReadsTheNewDirectory(t *testing.T) {
	server := newRedeployableMCPServer(t)
	server.deploy("2", "guidance", "get_order")
	store := &memoryDirectoryStore{}
	store.save(savedDirectory("2", "guidance", time.Now().Add(-time.Minute), "get_order", "cancel_order"))
	revalidator := NewMCPDirectoryRevalidator(time.Hour)
	joined := make(chan struct{})
	revalidator.joined = func(string) { close(joined) }
	first, _ := engineCatalog(t, server.service(), store, revalidator)
	second, _ := engineCatalog(t, server.service(), store, revalidator)
	firstTool, secondTool := catalogTool(t, first, "cancel_order"), catalogTool(t, second, "cancel_order")
	// Open each engine's connection first, so that only the re-list's new
	// connection is held below.
	for _, catalog := range []*MCPCatalog{first, second} {
		result, err := catalogTool(t, catalog, "get_order").Execute(catalogTestContext(), json.RawMessage(`{}`))
		require.NoError(t, err)
		require.True(t, result.Success)
	}

	gate := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	server.handshakeGate.Store(gate)
	firstDone := make(chan *types.ToolResult)
	go func() {
		result, _ := firstTool.Execute(catalogTestContext(), json.RawMessage(`{}`))
		firstDone <- result
	}()
	require.Eventually(t, func() bool {
		revalidator.mu.Lock()
		defer revalidator.mu.Unlock()
		return len(revalidator.inflight) == 1
	}, 5*time.Second, 5*time.Millisecond)
	secondDone := make(chan *types.ToolResult)
	go func() {
		result, _ := secondTool.Execute(catalogTestContext(), json.RawMessage(`{}`))
		secondDone <- result
	}()
	<-joined
	release.Do(func() { close(gate) })
	require.Contains(t, (<-firstDone).Error, "has been refreshed")
	require.Contains(t, (<-secondDone).Error, "has been refreshed")
	_, puts := store.current()
	require.Equal(t, 1, puts, "the second engine waited for the first one's re-list")

	tools, _, err := second.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	require.Len(t, tools, 1, "the engine that only waited also reads the new directory")
}

func TestFailedReloadAfterRelistKeepsTheWorkingTools(t *testing.T) {
	service := &types.MCPService{ID: "svc", Name: "Orders", Enabled: true}
	var fail atomic.Bool
	catalog := newMCPCatalog(catalogTestContext(), []*types.MCPService{service}, nil,
		func(context.Context, *types.MCPService, bool) ([]*MCPTool, error) {
			if fail.Load() {
				return nil, errors.New("metadata storage is unavailable")
			}
			return []*MCPTool{catalogTestTool(service, "get_order", "lookup", nil)}, nil
		}, nil)
	tools, status, err := catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	require.Equal(t, "ready", status)

	fail.Store(true)
	catalog.markForReload("svc")
	again, status, err := catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	require.Equal(t, "ready", status)
	require.Equal(t, tools, again)
	require.NotNil(t, catalog.cachedTool(mcpToolRef(tools[0])), "issued references keep resolving")

	fail.Store(false)
	_, _, err = catalog.snapshot(catalogTestContext(), "svc", false)
	require.NoError(t, err)
	catalog.servers["svc"].mu.Lock()
	defer catalog.servers["svc"].mu.Unlock()
	require.False(t, catalog.servers["svc"].reload, "the reload is retried until it succeeds")
}
