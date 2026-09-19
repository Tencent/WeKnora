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

func TestGRPCParserProxyMapsReadResult(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pluginapi.RegisterParserPluginServer(server, pluginapi.ParserHandler{
		PluginID: "test.parser",
		OnParse: func(_ context.Context, request pluginapi.ParserRequest) (pluginapi.ParserResponse, error) {
			return pluginapi.ParserResponse{
				MarkdownContent: "# " + string(request.FileContent),
				ImageDirPath:    "images",
				Metadata:        map[string]string{"source": request.FileName},
				ImageRefs: []pluginapi.ParserImageRef{{
					Filename:    "image.png",
					OriginalRef: "embedded:image.png",
					MIMEType:    "image/png",
					StorageKey:  "objects/image.png",
					ImageData:   []byte("png"),
					IsOriginal:  true,
				}},
			}, nil
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	reader := &GRPCParserProxy{Client: pluginapi.NewParserPluginClient(conn)}
	result, err := reader.Read(ctx, &types.ReadRequest{FileContent: []byte("hello"), FileName: "a.md", FileType: "md", RequestID: "req-1"})
	require.NoError(t, err)
	require.Equal(t, "# hello", result.MarkdownContent)
	require.Equal(t, "images", result.ImageDirPath)
	require.Equal(t, "a.md", result.Metadata["source"])
	require.Len(t, result.ImageRefs, 1)
	require.Equal(t, []byte("png"), result.ImageRefs[0].ImageData)
	require.True(t, result.ImageRefs[0].IsOriginal)
}

func TestGRPCParserProxyRejectsNilRequest(t *testing.T) {
	_, err := (&GRPCParserProxy{}).Read(context.Background(), nil)
	require.EqualError(t, err, "parser request is nil")
}
