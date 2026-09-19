package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestGRPCWebSearchProxyMapsResultsAndParameters(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginapi.RegisterWebSearchPluginServer(server, pluginapi.WebSearchHandler{
		PluginID: "test.search",
		OnSearch: func(_ context.Context, request pluginapi.WebSearchRequest) (pluginapi.WebSearchResponse, error) {
			require.Equal(t, "query", request.Query)
			require.Equal(t, "api-key", request.APIKey)
			require.Equal(t, "engine", request.EngineID)
			return pluginapi.WebSearchResponse{Results: []*pluginapi.WebSearchResult{{
				Title:       "Title",
				URL:         "https://example.com",
				Snippet:     "Snippet",
				Content:     "Content",
				Source:      "test.search",
				PublishedAt: "2026-08-22T12:00:00Z",
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

	provider := &GRPCWebSearchProxy{
		Client:    pluginapi.NewWebSearchPluginClient(conn),
		NameValue: "test.search",
		Params:    types.WebSearchProviderParameters{APIKey: "api-key", EngineID: "engine"},
	}
	results, err := provider.Search(ctx, "query", 5, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "Title", results[0].Title)
	require.Equal(t, "test.search", provider.Name())
	require.NotNil(t, results[0].PublishedAt)
	require.Equal(t, time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC), *results[0].PublishedAt)
}
