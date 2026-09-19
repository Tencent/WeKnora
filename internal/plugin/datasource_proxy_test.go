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

func TestFromWireItemsUsesTitleAsFileNameForV1Compatibility(t *testing.T) {
	items, err := fromWireItems([]pluginapi.FetchedItem{{
		ExternalID: "file:notes/亚泰.txt",
		Title:      "亚泰.txt",
		Content:    []byte("content"),
	}})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "亚泰.txt", items[0].FileName)
}

func TestGRPCConnectorProxyKeepsCursorOutsideRuntimeIdentity(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginapi.RegisterDataSourcePluginServer(server, pluginapi.DataSourceHandler{
		PluginID: "test.datasource",
		OnFetchIncremental: func(_ context.Context, request pluginapi.Request) ([]pluginapi.FetchedItem, map[string]any, error) {
			if request.Cursor == nil {
				return []pluginapi.FetchedItem{{ExternalID: "file:a", Content: []byte("v1")}}, map[string]any{"revision": float64(1)}, nil
			}
			return []pluginapi.FetchedItem{{ExternalID: "file:a", Content: []byte("v2")}}, map[string]any{"revision": float64(2)}, nil
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	proxy := &GRPCConnectorProxy{ConnectorType: "test.datasource", Client: pluginapi.NewDataSourcePluginClient(conn)}
	config := &types.DataSourceConfig{Type: "test.datasource", Settings: map[string]interface{}{"root": t.TempDir()}}
	first, cursor, err := proxy.FetchIncremental(ctx, config, nil)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, float64(1), cursor.ConnectorCursor["revision"])
	second, next, err := proxy.FetchIncremental(ctx, config, cursor)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, []byte("v2"), second[0].Content)
	require.Equal(t, float64(2), next.ConnectorCursor["revision"])
}

type proxyStreamRecorder struct {
	items   []types.FetchedItem
	cursors []*types.SyncCursor
}

func (r *proxyStreamRecorder) Emit(_ context.Context, item types.FetchedItem) error {
	r.items = append(r.items, item)
	return nil
}

func (r *proxyStreamRecorder) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	r.cursors = append(r.cursors, cursor)
	return nil
}

func TestGRPCConnectorProxyStreamsItemsAndCheckpoints(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginapi.RegisterDataSourcePluginServer(server, pluginapi.DataSourceHandler{
		PluginID: "test.streaming",
		OnFetchAllStream: func(_ context.Context, _ pluginapi.Request, emit func(pluginapi.Response) error) error {
			if err := emit(pluginapi.Response{Items: []pluginapi.FetchedItem{{ExternalID: "file:a", Content: []byte("a")}}}); err != nil {
				return err
			}
			return emit(pluginapi.Response{Cursor: map[string]any{"page": float64(1)}})
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := pluginapi.NewDataSourcePluginClient(conn)
	streaming := client.(pluginapi.DataSourceStreamingPluginClient)
	proxy := &GRPCStreamingConnectorProxy{
		GRPCConnectorProxy: &GRPCConnectorProxy{ConnectorType: "test.streaming", Client: client},
		StreamingClient:    streaming,
	}
	recorder := new(proxyStreamRecorder)
	config := &types.DataSourceConfig{Type: "test.streaming", Settings: map[string]interface{}{}}
	next, err := proxy.FetchStream(ctx, config, nil, recorder)
	require.NoError(t, err)
	require.Len(t, recorder.items, 1)
	require.Len(t, recorder.cursors, 1)
	require.Equal(t, float64(1), recorder.cursors[0].ConnectorCursor["page"])
	require.Equal(t, float64(1), next.ConnectorCursor["page"])
}

func TestGRPCConnectorProxyRejectsStaleRuntimeGenerationBeforeRPC(t *testing.T) {
	called := false
	proxy := &GRPCConnectorProxy{
		ConnectorType:     "test.stale",
		RuntimeGeneration: 1,
		GenerationValid:   func(uint64) bool { return false },
	}
	_ = called
	err := proxy.Validate(context.Background(), &types.DataSourceConfig{Type: "test.stale"})
	require.ErrorContains(t, err, "stale")
}
