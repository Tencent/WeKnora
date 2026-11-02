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

type browserScopeManager struct {
	browserLifecycleManager
	scopes []browserskill.Scope
}

func (m *browserScopeManager) Call(
	ctx context.Context, s browserskill.Scope, session, method string, params map[string]any,
) (json.RawMessage, error) {
	m.scopes = append(m.scopes, s)
	return m.browserLifecycleManager.Call(ctx, s, session, method, params)
}

// A shared agent executes in its owner's workspace; the browser must still be
// the one the asking member paired in their own workspace.
func TestBrowserSkillScopeFollowsCallerInSharedAgent(t *testing.T) {
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	shared := types.WithExecutionTenant(ctx, 99)
	own := browserskill.Scope{Tenant: 7, User: "alice"}
	require.Equal(t, own, BrowserSkillScope(ctx))
	require.Equal(t, own, BrowserSkillScope(shared))

	manager := &browserScopeManager{}
	tool := NewBrowserSkillTool(nil, BrowserSkillScope(shared), "chat")
	tool.manager = manager
	result, err := tool.Execute(shared, json.RawMessage(`{"method":"observe"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []browserskill.Scope{own}, manager.scopes)

	bob := types.WithExecutionTenant(context.WithValue(ctx, types.UserIDContextKey, "bob"), 99)
	_, err = tool.Execute(bob, json.RawMessage(`{"method":"observe"}`))
	require.ErrorContains(t, err, "owner mismatch")
	_, err = tool.Execute(context.WithValue(ctx, types.TenantIDContextKey, uint64(8)),
		json.RawMessage(`{"method":"observe"}`))
	require.ErrorContains(t, err, "owner mismatch", "another workspace's member must not drive this browser")
}

func TestBrowserWaitRequiresDocumentedDuration(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	require.NoError(t, tool.ValidateArguments(
		json.RawMessage(`{"method":"wait_ms","duration_ms":1000,"tab_id":3}`),
	))
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
	methods    []string
	retained   []bool
	cleanupErr error
	protocol   string
	extension  string
}

func (m *browserLifecycleManager) GetStatus(context.Context, browserskill.Scope, string) (browserskill.Status, error) {
	return browserskill.Status{
		Connected: true, ProtocolVersion: m.protocol, ExtensionVersion: m.extension,
	}, nil
}

func (m *browserLifecycleManager) Control(context.Context, browserskill.Scope, string, string) error {
	return nil
}

func (m *browserLifecycleManager) Call(
	_ context.Context, _ browserskill.Scope, _ string, method string, params map[string]any,
) (json.RawMessage, error) {
	m.methods = append(m.methods, method)
	if method == "cooperate" {
		return json.RawMessage(`{"context_id":"test-context","observation_id":"test-observation"}`), nil
	}
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

func TestBrowserActionsUseFreshCooperationContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	manager := &browserLifecycleManager{}
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	tool.manager = manager

	result, err := tool.Execute(ctx, json.RawMessage(`{"method":"click","ref":"e1"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"cooperate", "click"}, manager.methods)
	require.Equal(t, "test-context", manager.params["context_id"])

	manager.methods = nil
	result, err = tool.Execute(ctx, json.RawMessage(`{"method":"observe"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"observe"}, manager.methods)
}

func TestBrowserTargetContextReuseAndBootstrap(t *testing.T) {
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	manager := &browserLifecycleManager{protocol: "1.8", extension: "0.4.0"}
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	tool.manager = manager

	// The first navigation bootstraps control; syncing the blank tab would race it.
	result, err := tool.Execute(ctx, json.RawMessage(`{"method":"navigate","url":"https://example.com"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"navigate"}, manager.methods)

	manager.methods = nil
	result, err = tool.Execute(ctx, json.RawMessage(`{"method":"click","ref":"e1"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"cooperate", "click"}, manager.methods)
	require.Equal(t, "test-context", manager.params["context_id"])
	_, hasObservation := manager.params["observation_id"]
	require.False(t, hasObservation, "targeted DOM actions use context, not a single-use observation")

	// A targeted DOM action reuses the context; it must not sync again.
	manager.methods = nil
	result, err = tool.Execute(ctx, json.RawMessage(`{"method":"fill","ref":"e2","value":"x"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"fill"}, manager.methods)
	require.Equal(t, "test-context", manager.params["context_id"])

	// Passive reads do not consume or invalidate a target context.
	manager.methods = nil
	result, err = tool.Execute(ctx, json.RawMessage(`{"method":"observe"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"observe"}, manager.methods)

	// A script is untargeted: it needs a fresh observation and invalidates the context.
	manager.methods = nil
	result, err = tool.Execute(ctx, json.RawMessage(`{"method":"wait_ms","duration_ms":10}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"cooperate", "wait_ms"}, manager.methods)
	require.Equal(t, "test-observation", manager.params["observation_id"])

	manager.methods = nil
	result, err = tool.Execute(ctx, json.RawMessage(`{"method":"click","ref":"e3"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"cooperate", "click"}, manager.methods)
}

func TestBrowserControlPolicyUsesProtocol(t *testing.T) {
	require.Equal(t, "target", browserControlPolicy("1.8"))
	require.Equal(t, "target", browserControlPolicy("1.9"))
	require.Equal(t, "target", browserControlPolicy("2.0"))
	require.Equal(t, "strict", browserControlPolicy("1.7"))
	require.Equal(t, "target", browserControlPolicy(""))
}

func TestBrowserStrictProtocolSyncsEveryAction(t *testing.T) {
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	manager := &browserLifecycleManager{protocol: "1.7", extension: "0.4.0"}
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	tool.manager = manager

	for _, raw := range []string{
		`{"method":"click","ref":"e1","expect":[{"target":"@e1","field":"text","equals":"OK"}]}`,
		`{"method":"click","ref":"e2"}`,
	} {
		manager.methods = nil
		result, err := tool.Execute(ctx, json.RawMessage(raw))
		require.NoError(t, err)
		require.True(t, result.Success)
		require.Equal(t, []string{"cooperate", "click"}, manager.methods)
		_, hasExpect := manager.params["expect"]
		require.False(t, hasExpect, "0.4.0 strict peers do not accept target-only expect")
	}
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
