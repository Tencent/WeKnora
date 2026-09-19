package pluginapi

import (
	"context"
	"net"
	"testing"
	"time"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestWebSearchPluginGRPCContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	handler := WebSearchHandler{
		PluginID:     "test.search",
		Capabilities: []string{"search"},
		OnSearch: func(_ context.Context, request WebSearchRequest) (WebSearchResponse, error) {
			return WebSearchResponse{Results: []*WebSearchResult{{
				Title:       request.Query,
				URL:         "https://example.com/result",
				Snippet:     "snippet",
				Source:      "test.search",
				PublishedAt: "2026-08-22T00:00:00Z",
			}}}, nil
		},
	}
	RegisterPluginControlServer(server, handler)
	RegisterWebSearchPluginServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := NewWebSearchPluginClient(conn)
	control := NewPluginControlClient(conn)

	handshakeWire, err := control.Handshake(ctx, &pluginproto.HandshakeRequest{})
	require.NoError(t, err)
	var handshake HandshakeResponse
	require.NoError(t, DecodeHandshake(handshakeWire, &handshake))
	require.Equal(t, "test.search", handshake.PluginID)
	report := RunWebSearchConformance(ctx, control, client, WebSearchRequest{Query: "weknora", MaxResults: 3})
	require.True(t, report.HandshakeOK)
	require.True(t, report.HealthOK)
	require.True(t, report.SearchOK)
	require.Empty(t, report.Errors)

	request, err := EncodeWebSearchRequest(WebSearchRequest{Query: "weknora", MaxResults: 3, APIKey: "secret"})
	require.NoError(t, err)
	responseWire, err := client.Search(ctx, request)
	require.NoError(t, err)
	var response WebSearchResponse
	require.NoError(t, DecodeWebSearchResponse(responseWire, &response))
	require.Len(t, response.Results, 1)
	require.Equal(t, "weknora", response.Results[0].Title)
}
