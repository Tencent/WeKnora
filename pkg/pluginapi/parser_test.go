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

func TestParserPluginGRPCContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	handler := ParserHandler{
		PluginID:     "test.markdown-parser",
		Capabilities: []string{"parse"},
		OnParse: func(_ context.Context, request ParserRequest) (ParserResponse, error) {
			return ParserResponse{
				MarkdownContent: "# " + string(request.FileContent),
				Metadata:        map[string]string{"file_name": request.FileName},
			}, nil
		},
	}
	RegisterPluginControlServer(server, handler)
	RegisterParserPluginServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := NewParserPluginClient(conn)
	control := NewPluginControlClient(conn)

	handshakeWire, err := control.Handshake(ctx, &pluginproto.HandshakeRequest{})
	require.NoError(t, err)
	var handshake HandshakeResponse
	require.NoError(t, DecodeHandshake(handshakeWire, &handshake))
	require.Equal(t, "test.markdown-parser", handshake.PluginID)
	require.Equal(t, []string{"parse"}, handshake.Capabilities)

	report := RunParserConformance(ctx, control, client, ParserRequest{FileContent: []byte("hello"), FileName: "README.md", FileType: "md"})
	require.True(t, report.HandshakeOK)
	require.True(t, report.HealthOK)
	require.True(t, report.ParseOK)
	require.Empty(t, report.Errors)

	request, err := EncodeParserRequest(ParserRequest{FileContent: []byte("hello"), FileName: "README.md", FileType: "md"})
	require.NoError(t, err)
	responseWire, err := client.Parse(ctx, request)
	require.NoError(t, err)
	var response ParserResponse
	require.NoError(t, DecodeParserResponse(responseWire, &response))
	require.Equal(t, "# hello", response.MarkdownContent)
	require.Equal(t, "README.md", response.Metadata["file_name"])
}
