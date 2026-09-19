package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type webSearchTestRuntime struct {
	conn *grpc.ClientConn
}

func (r *webSearchTestRuntime) Start(context.Context) error { return nil }
func (r *webSearchTestRuntime) Stop(context.Context) error  { return nil }
func (r *webSearchTestRuntime) Health(context.Context) HealthStatus {
	return HealthStatus{State: StateRunning, CheckedAt: time.Now().UTC()}
}
func (r *webSearchTestRuntime) Conn() *grpc.ClientConn {
	return r.conn
}

func TestExternalWebSearchRegistrationUsesTenantScopedFactory(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginapi.RegisterWebSearchPluginServer(server, pluginapi.WebSearchHandler{
		PluginID: "test.external-search",
		OnSearch: func(_ context.Context, request pluginapi.WebSearchRequest) (pluginapi.WebSearchResponse, error) {
			return pluginapi.WebSearchResponse{Results: []*pluginapi.WebSearchResult{{
				Title:  request.APIKey,
				URL:    "https://example.com",
				Source: "external.search",
			}}}, nil
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	manifest := Manifest{APIVersion: APIVersionV1, ID: "test.external-search", Name: "External Search", Version: "1.0.0", ExtensionType: ExtensionSearch, ProtocolVersion: ProtocolVersionV1, Capabilities: []string{"search"}}
	manager := NewManager("")
	registry := infraWebSearch.NewRegistry()
	providerType, err := RegisterExternalWebSearch(manager, registry, manifest, &webSearchTestRuntime{conn: conn})
	require.NoError(t, err)
	require.Equal(t, manifest.ID, providerType)
	t.Cleanup(func() { _ = manager.Unregister(context.Background(), manifest.ID); registry.Unregister(providerType) })
	require.NoError(t, manager.Start(ctx, manifest.ID))

	provider, err := registry.CreateProvider(providerType, types.WebSearchProviderParameters{APIKey: "tenant-secret"})
	require.NoError(t, err)
	results, err := provider.Search(ctx, "query", 5, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "tenant-secret", results[0].Title)
}
