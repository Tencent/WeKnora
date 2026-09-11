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
	result, err := tool.Execute(ctx, json.RawMessage(`{"method":"observe","params":{}}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "personal settings")
	require.NotContains(t, result.Error, "sandbox")
	_, err = tool.Execute(
		context.WithValue(ctx, types.UserIDContextKey, "bob"),
		json.RawMessage(`{"method":"observe","params":{}}`),
	)
	require.ErrorContains(t, err, "owner mismatch")
}

func TestBrowserWaitRequiresDocumentedDuration(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	for _, params := range []string{
		`{}`, `{"ms":1000}`, `{"duration_ms":-1}`, `{"duration_ms":0.5}`, `{"duration_ms":10001}`,
	} {
		result, err := tool.Execute(ctx, json.RawMessage(`{"method":"wait_ms","params":`+params+`}`))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Contains(t, result.Error, "params.duration_ms")
	}
}
