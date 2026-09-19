package pluginapi

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func serveModelForConformance(t *testing.T, handler ModelHandler) (PluginControlClient, ModelPluginClient) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	RegisterPluginControlServer(server, handler)
	RegisterModelPluginServer(server, handler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewPluginControlClient(conn), NewModelPluginClient(conn)
}

func TestRunModelConformanceAllCapabilities(t *testing.T) {
	handler := ModelHandler{
		PluginID:     "test.model",
		Capabilities: []string{"chat", "embedding", "rerank", "vllm", "asr"},
		OnChat:       func(context.Context, ChatRequest) (ChatResult, error) { return ChatResult{Content: "hi"}, nil },
		OnEmbed:      func(context.Context, string) ([]float32, error) { return []float32{1, 2, 3}, nil },
		OnRerank:     func(context.Context, string, []string) ([]RerankResult, error) { return []RerankResult{{Index: 0}}, nil },
		OnPredictVLM: func(context.Context, [][]byte, string) (string, error) { return "ok", nil },
		OnTranscribe: func(context.Context, []byte, string) (string, error) { return "ok", nil },
	}
	control, client := serveModelForConformance(t, handler)

	report := RunModelConformance(context.Background(), control, client)
	require.True(t, report.HandshakeOK)
	require.True(t, report.HealthOK)
	require.Equal(t, "test.model", report.PluginID)
	require.True(t, report.ChatOK)
	require.True(t, report.EmbeddingOK)
	require.True(t, report.RerankOK)
	require.True(t, report.VLMOK)
	require.True(t, report.ASROK)
	require.Empty(t, report.Errors)
}

func TestRunModelConformanceSkipsUndeclaredAndFlagsError(t *testing.T) {
	handler := ModelHandler{
		PluginID:     "test.model2",
		Capabilities: []string{"chat", "embedding"},
		OnChat:       func(context.Context, ChatRequest) (ChatResult, error) { return ChatResult{}, nil },
		// OnEmbed intentionally nil: declared but not implemented -> should flag error.
	}
	control, client := serveModelForConformance(t, handler)

	report := RunModelConformance(context.Background(), control, client)
	require.True(t, report.HandshakeOK)
	require.True(t, report.HealthOK)
	require.True(t, report.ChatOK)
	// embedding is declared but the handler returns "not implemented" -> failed.
	require.False(t, report.EmbeddingOK)
	// rerank / vllm / asr are undeclared -> skipped, their flags stay false but no error.
	require.False(t, report.RerankOK)
	require.False(t, report.VLMOK)
	require.False(t, report.ASROK)
	require.NotEmpty(t, report.Errors)
}
