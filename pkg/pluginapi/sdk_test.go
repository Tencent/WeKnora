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

func TestDataSourcePluginGRPCContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	handler := DataSourceHandler{
		PluginID: "test.localdir", Capabilities: []string{"incremental"},
		OnFetchAll: func(context.Context, Request) ([]FetchedItem, error) {
			return []FetchedItem{{ExternalID: "file:a", Content: []byte("hello")}}, nil
		},
	}
	RegisterPluginControlServer(server, handler)
	RegisterDataSourcePluginServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := NewDataSourcePluginClient(conn)
	control := NewPluginControlClient(conn)
	handshakeWire, err := control.Handshake(ctx, &pluginproto.HandshakeRequest{})
	require.NoError(t, err)
	var handshake HandshakeResponse
	require.NoError(t, DecodeHandshake(handshakeWire, &handshake))
	require.Equal(t, "test.localdir", handshake.PluginID)

	request, err := EncodeRequest(Request{Config: map[string]any{"root": t.TempDir()}})
	require.NoError(t, err)
	responseWire, err := client.FetchAll(ctx, request)
	require.NoError(t, err)
	var response Response
	require.NoError(t, DecodeResponse(responseWire, &response))
	require.Len(t, response.Items, 1)
	require.Equal(t, []byte("hello"), response.Items[0].Content)
}

func TestDataSourcePluginStreamingContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	handler := DataSourceHandler{
		PluginID: "test.streaming",
		OnFetchAllStream: func(_ context.Context, _ Request, emit func(Response) error) error {
			require.NoError(t, emit(Response{Items: []FetchedItem{{ExternalID: "file:a", Content: []byte("a")}}}))
			return emit(Response{Cursor: map[string]any{"page": float64(1)}})
		},
	}
	RegisterPluginControlServer(server, handler)
	RegisterDataSourcePluginServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := NewDataSourcePluginClient(conn)
	streaming := client.(DataSourceStreamingPluginClient)
	request, err := EncodeRequest(Request{})
	require.NoError(t, err)
	stream, err := streaming.FetchAllStream(ctx, request)
	require.NoError(t, err)
	first, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	second, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, second.Cursor)
}

func TestInvocationContextTravelsInGRPCMetadata(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	seen := make(chan InvocationContext, 1)
	handler := DataSourceHandler{
		PluginID: "test.invocation",
		OnValidate: func(ctx context.Context, _ Request) error {
			seen <- InvocationContextFromContext(ctx)
			return nil
		},
	}
	RegisterPluginControlServer(server, handler)
	RegisterDataSourcePluginServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	inv := InvocationContext{TenantID: 7, KnowledgeBaseID: "kb", DataSourceID: "ds", OperationID: "op", TraceID: "trace"}
	request, err := EncodeRequest(Request{})
	require.NoError(t, err)
	_, err = NewDataSourcePluginClient(conn).Validate(WithInvocationContext(ctx, inv), request)
	require.NoError(t, err)
	require.Equal(t, inv, <-seen)
}
