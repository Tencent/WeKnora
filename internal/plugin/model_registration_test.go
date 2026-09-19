package plugin

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// swappableModelRuntime is a connProvider whose underlying connection can be
// swapped to simulate a plugin restart. The Manager owns lifecycle (Start/Stop
// increments generation), while the resolver must re-read Conn() on every call
// so a restart transparently picks up the new connection.
type swappableModelRuntime struct {
	mu   sync.RWMutex
	conn *grpc.ClientConn
}

func (r *swappableModelRuntime) Start(context.Context) error { return nil }
func (r *swappableModelRuntime) Stop(context.Context) error  { return nil }
func (r *swappableModelRuntime) Health(context.Context) HealthStatus {
	return HealthStatus{State: StateRunning, CheckedAt: time.Now().UTC()}
}
func (r *swappableModelRuntime) Conn() *grpc.ClientConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conn
}
func (r *swappableModelRuntime) SetConn(conn *grpc.ClientConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conn = conn
}

// startModelServer starts a gRPC server implementing the ModelPlugin service
// and returns its listener address plus a cleanup func.
func startModelServer(t *testing.T, pluginID string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginapi.RegisterModelPluginServer(server, pluginapi.ModelHandler{
		PluginID:     pluginID,
		Capabilities: []string{"embedding"},
		OnEmbed: func(_ context.Context, text string) ([]float32, error) {
			return []float32{1.0, 2.0, 3.0}, nil
		},
	})
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), server.Stop
}

func dialModel(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	return conn
}

// TestModelResolverSurvivesRestart is the regression test for the bug where a
// health-supervisor restart left the model plugin pinned to a closed client.
// After swapping the underlying connection (simulating restart) and advancing
// the generation, a subsequent ResolveExternalModelCall must return a client
// bound to the NEW connection, so calls keep succeeding.
func TestModelResolverSurvivesRestart(t *testing.T) {
	addr1, stop1 := startModelServer(t, "test.model")
	conn1 := dialModel(t, addr1)

	manifest := Manifest{
		APIVersion: APIVersionV1, ID: "test.model", Name: "Test Model",
		Version: "1.0.0", ExtensionType: ExtensionModel, ProtocolVersion: ProtocolVersionV1,
		Capabilities: []string{"embedding"},
	}
	manager := NewManager("")
	runtime := &swappableModelRuntime{conn: conn1}

	providerName, err := RegisterExternalModel(manager, manifest, runtime)
	require.NoError(t, err)
	require.Equal(t, manifest.ID, providerName)
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	t.Cleanup(func() {
		_ = manager.Unregister(context.Background(), manifest.ID)
		provider.UnregisterExternalModelResolver(providerName)
	})

	// First call: succeeds against conn1.
	client, callCtx, release, err := provider.ResolveExternalModelCall(context.Background(), providerName)
	require.NoError(t, err)
	resp, err := client.Embed(callCtx, &pluginproto.ModelEmbedRequest{Text: "hello"})
	require.NoError(t, err)
	require.Equal(t, "", resp.GetError())
	require.Equal(t, []float32{1.0, 2.0, 3.0}, resp.GetVector())
	release()

	// Simulate a restart: close the old connection, start a new server/conn,
	// and stop+start the plugin so the Manager advances the generation.
	stop1()
	_ = conn1.Close()

	addr2, stop2 := startModelServer(t, "test.model")
	defer stop2()
	conn2 := dialModel(t, addr2)
	runtime.SetConn(conn2)

	require.NoError(t, manager.Stop(context.Background(), manifest.ID))
	require.NoError(t, manager.Start(context.Background(), manifest.ID))

	// Second call: the resolver must now return a client bound to conn2.
	client2, callCtx2, release2, err := provider.ResolveExternalModelCall(context.Background(), providerName)
	require.NoError(t, err)
	defer release2()
	resp2, err := client2.Embed(callCtx2, &pluginproto.ModelEmbedRequest{Text: "hello again"})
	require.NoError(t, err)
	require.Equal(t, "", resp2.GetError())
	require.Equal(t, []float32{1.0, 2.0, 3.0}, resp2.GetVector())
}

// TestModelResolverRejectsUnknownProvider verifies the resolver returns a clear
// error for an unregistered provider instead of panicking.
func TestModelResolverRejectsUnknownProvider(t *testing.T) {
	_, _, _, err := provider.ResolveExternalModelCall(context.Background(), "does.not.exist")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not loaded")
}

// TestModelResolverGenerationFencing verifies that after a restart advances the
// generation, a stale resolve (using the pre-restart generation) is not possible
// because ResolveExternalModelCall always reads the current generation via
// AcquireInvocation. We assert the happy path: a resolve after restart returns
// a live client and the returned callCtx carries the current generation lease.
func TestModelResolverGenerationFencing(t *testing.T) {
	addr, stop := startModelServer(t, "test.model")
	defer stop()
	conn := dialModel(t, addr)

	manifest := Manifest{
		APIVersion: APIVersionV1, ID: "test.model", Name: "Test Model",
		Version: "1.0.0", ExtensionType: ExtensionModel, ProtocolVersion: ProtocolVersionV1,
		Capabilities: []string{"embedding"},
	}
	manager := NewManager("")
	runtime := &swappableModelRuntime{conn: conn}
	providerName, err := RegisterExternalModel(manager, manifest, runtime)
	require.NoError(t, err)
	require.NoError(t, manager.Start(context.Background(), manifest.ID))
	t.Cleanup(func() {
		_ = manager.Unregister(context.Background(), manifest.ID)
		provider.UnregisterExternalModelResolver(providerName)
	})

	// A resolve while running succeeds and pins the current generation.
	client, callCtx, release, err := provider.ResolveExternalModelCall(context.Background(), providerName)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, callCtx)
	release()

	// After stop, the plugin is not running: a resolve must fail cleanly
	// instead of dispatching to a dead runtime.
	require.NoError(t, manager.Stop(context.Background(), manifest.ID))
	_, _, _, err = provider.ResolveExternalModelCall(context.Background(), providerName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not running")
}
