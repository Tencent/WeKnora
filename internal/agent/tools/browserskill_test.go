package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBrowserSkillNeedsConnectionNotSandbox(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	tool := NewBrowserSkillTool(browserskill.NewManager(), browserskill.Scope{Tenant: 7, User: "alice"}, "conversation")
	result, err := tool.Execute(ctx, json.RawMessage(`{"method":"observe"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "personal settings")
	require.NotContains(t, result.Error, "sandbox")
	_, err = tool.Execute(
		context.WithValue(ctx, types.UserIDContextKey, "bob"),
		json.RawMessage(`{"method":"observe"}`),
	)
	require.ErrorContains(t, err, "owner mismatch")
}

func TestBrowserWaitRequiresDocumentedDuration(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	for _, raw := range []string{
		`{"method":"wait_ms"}`, `{"method":"wait_ms","ms":1000}`,
		`{"method":"wait_ms","duration_ms":-1}`, `{"method":"wait_ms","duration_ms":0.5}`,
		`{"method":"wait_ms","duration_ms":10001}`,
	} {
		result, err := tool.Execute(ctx, json.RawMessage(raw))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Contains(t, result.Error, "Invalid browser arguments")
	}
}

func TestBrowserToolParticipatesInTurnCleanup(t *testing.T) {
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	var cleanable types.Cleanable = tool
	// Merely registering the tool must not touch browser state on turn end.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cleanable.Cleanup(ctx)
	require.False(t, tool.used.Load())
}

func TestBrowserFlatArgumentsPassRegistryValidation(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	registry := NewToolRegistry()
	registry.RegisterTool(NewBrowserSkillTool(
		browserskill.NewManager(), browserskill.Scope{Tenant: 7, User: "alice"}, "chat",
	))
	result, err := registry.ExecuteTool(ctx, "local_browser",
		json.RawMessage(`{"method":"navigate","url":"https://www.jd.com"}`))
	require.NoError(t, err)
	// Reaching the connection guard proves the flat call reached Execute.
	require.False(t, result.Success)
	require.Contains(t, result.Error, "personal settings")
	require.NotContains(t, result.Error, "Parameter validation failed")
	for _, raw := range []string{
		`{"method":"navigate","params":{"url":"https://www.jd.com"}}`,
		`{"method":"navigate","params":"{\"url\":\"https://www.jd.com\"}"}`,
		`{"method":"observe","params":{}}`,
		`{"method":"navigate"}`,
		`{"method":"wait_ms","duration_ms":-1}`,
	} {
		result, err := registry.ExecuteTool(ctx, "local_browser", json.RawMessage(raw))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Contains(t, result.Error, "Parameter validation failed")
	}
}
