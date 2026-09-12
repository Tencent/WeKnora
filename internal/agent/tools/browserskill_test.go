package tools

import (
	"context"
	"encoding/json"
	"errors"
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

// The fake captures the actual Execute -> Cleanup lifecycle without starting a browser.
type browserLifecycleManager struct {
	*browserskill.Manager
	params     map[string]any
	callErr    error
	response   json.RawMessage
	calls      int
	retained   []bool
	cleanupErr error
}

func (m *browserLifecycleManager) GetStatus(context.Context, browserskill.Scope, string) (browserskill.Status, error) {
	return browserskill.Status{Connected: true}, nil
}

func (m *browserLifecycleManager) Control(context.Context, browserskill.Scope, string, string) error {
	return nil
}

func (m *browserLifecycleManager) Call(
	_ context.Context, _ browserskill.Scope, _ string, method string, params map[string]any,
) (json.RawMessage, error) {
	m.params = params
	m.calls++
	if m.response != nil {
		return m.response, m.callErr
	}
	if method == "request_help" {
		return json.RawMessage(`{"outcome":"continued"}`), m.callErr
	}
	return json.RawMessage(`{}`), m.callErr
}

func (m *browserLifecycleManager) FinishTurn(ctx context.Context, _ browserskill.Scope, _ string, keep bool) error {
	m.cleanupErr = ctx.Err()
	m.retained = append(m.retained, keep)
	return nil
}

func TestBrowserTurnRetention(t *testing.T) {
	for _, tc := range []struct {
		name               string
		calls              []string
		cancel, fail, keep bool
	}{
		{name: "research closes", calls: []string{`{"method":"observe"}`}},
		{
			name: "requested open page", keep: true,
			calls: []string{
				`{"method":"navigate","url":"https://example.com","keep_open":true}`, `{"method":"observe"}`,
			},
		},
		{name: "human handoff", calls: []string{`{"method":"request_help","prompt":"Please sign in"}`}, keep: true},
		{
			name: "resolved handoff",
			calls: []string{
				`{"method":"request_help","prompt":"Please sign in"}`,
				`{"method":"observe","keep_open":false}`,
			},
		},
		{name: "cancel preserves", calls: []string{`{"method":"observe"}`}, cancel: true, keep: true},
		{name: "failure preserves", calls: []string{`{"method":"observe"}`}, fail: true, keep: true},
		{name: "invalid followup preserves", calls: []string{`{"method":"observe"}`, `{"method":"click"}`}, keep: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			manager := &browserLifecycleManager{}
			if tc.fail {
				manager.callErr = errors.New("operation failed")
			}
			tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
			tool.manager = manager
			for _, raw := range tc.calls {
				_, err := tool.Execute(ctx, json.RawMessage(raw))
				require.NoError(t, err)
			}
			require.NotContains(t, manager.params, "keep_open")
			if tc.cancel {
				cancel()
			}
			tool.Cleanup(ctx)
			tool.Cleanup(ctx)
			require.Equal(t, []bool{tc.keep}, manager.retained)
			require.NoError(t, manager.cleanupErr)
		})
	}
}
