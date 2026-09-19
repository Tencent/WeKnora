package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type parserTestRuntime struct {
	conn *grpc.ClientConn
}

func (r *parserTestRuntime) Start(context.Context) error { return nil }
func (r *parserTestRuntime) Stop(context.Context) error  { return nil }
func (r *parserTestRuntime) Health(context.Context) HealthStatus {
	return HealthStatus{State: StateRunning, CheckedAt: time.Now().UTC()}
}
func (r *parserTestRuntime) Conn() *grpc.ClientConn {
	return r.conn
}

func TestExternalParserRegistrationUsesExistingDocparserRegistry(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	var receivedOverrides map[string]string
	pluginapi.RegisterParserPluginServer(server, pluginapi.ParserHandler{
		PluginID:     "test.external-parser",
		Capabilities: []string{"parse"},
		OnParse: func(_ context.Context, request pluginapi.ParserRequest) (pluginapi.ParserResponse, error) {
			receivedOverrides = request.ParserEngineOverrides
			return pluginapi.ParserResponse{MarkdownContent: "external:" + string(request.FileContent)}, nil
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	engineName := "test_external_parser"
	descriptor := ParserDescriptor{EngineName: engineName, Description: "test parser", FileTypes: []string{"txt"}}
	manifest := Manifest{
		APIVersion:      APIVersionV1,
		ID:              "test.external-parser",
		Name:            "Test External Parser",
		Version:         "1.0.0",
		ExtensionType:   ExtensionParser,
		ProtocolVersion: ProtocolVersionV1,
		Capabilities:    []string{"parse"},
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"settings": map[string]any{
					"type":       "object",
					"required":   []any{"timeout"},
					"properties": map[string]any{"timeout": map[string]any{"type": "integer"}},
				},
			},
		},
		Metadata: map[string]any{"file_types": []any{"txt"}},
	}
	runtime := &parserTestRuntime{conn: conn}
	manager := NewManager("")
	require.NoError(t, RegisterExternalParser(manager, manifest, runtime, descriptor))
	t.Cleanup(func() {
		UnregisterExternalParser(descriptor)
		_ = manager.Unregister(context.Background(), manifest.ID)
	})
	require.NoError(t, manager.Start(ctx, manifest.ID))

	var found bool
	for _, engine := range docparser.ListAllEngines(false, nil, nil) {
		if engine.Name == engineName {
			found = true
			require.True(t, engine.Available)
			require.Equal(t, []string{"txt"}, engine.FileTypes)
			require.True(t, engine.External)
			require.Equal(t, manifest.ID, engine.PluginID)
			require.NotNil(t, engine.ConfigSchema)
		}
	}
	require.True(t, found, "external parser was not listed")

	pluginConfig := &types.ParserEngineConfig{ExternalPluginConfigs: map[string]types.ExternalParserPluginConfig{
		manifest.ID: {Settings: map[string]any{"timeout": 42}},
	}}
	overrides := docparser.OverridesForEngine(pluginConfig, engineName)
	require.Equal(t, map[string]string{"timeout": "42"}, overrides)
	reader, err := docparser.NewReader(ctx, engineName, "txt", false, docparser.ReaderDeps{Overrides: overrides})
	require.NoError(t, err)
	result, err := reader.Read(ctx, &types.ReadRequest{FileContent: []byte("hello"), FileName: "a.txt", FileType: "txt", ParserEngineOverrides: overrides})
	require.NoError(t, err)
	require.Equal(t, "external:hello", result.MarkdownContent)
	require.Equal(t, "42", receivedOverrides["timeout"])
}
