package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBrowserOperationResultsAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, args, response string
		success, blocks      bool
	}{
		{
			"evaluate exception",
			`{"method":"evaluate","expression":"document.querySelector('missing').textContent"}`,
			`{"ok":false,"error":{"text":"TypeError"}}`, false, false,
		},
		{"evaluate null", `{"method":"evaluate","expression":"null"}`, `{"ok":true,"value":null}`, true, false},
		{"evaluate missing envelope", `{"method":"evaluate","expression":"1"}`, `{}`, false, false},
		{"page error text is data", `{"method":"observe"}`, `{"text":"Error: page not found"}`, true, false},
		{"help cancelled", `{"method":"request_help","prompt":"Sign in"}`, `{"outcome":"cancelled"}`, false, true},
		{"help timeout", `{"method":"request_help","prompt":"Sign in"}`, `{"outcome":"timed_out"}`, false, true},
		{"help disabled", `{"method":"request_help","prompt":"Sign in"}`, `{"outcome":"disabled"}`, false, true},
		{
			"help navigation is not completion",
			`{"method":"request_help","prompt":"Sign in"}`, `{"outcome":"navigated"}`, false, true,
		},
		{"help missing outcome", `{"method":"request_help","prompt":"Sign in"}`, `{}`, false, true},
		{"help completed", `{"method":"request_help","prompt":"Sign in"}`, `{"outcome":"completed"}`, true, false},
		{"help continued", `{"method":"request_help","prompt":"Sign in"}`, `{"outcome":"continued"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
			ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
			manager := &browserLifecycleManager{response: json.RawMessage(tc.response)}
			tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
			tool.manager = manager
			result, err := tool.Execute(ctx, json.RawMessage(tc.args))
			require.NoError(t, err)
			require.Equal(t, tc.success, result.Success)
			require.JSONEq(t, tc.response, result.Output, "preserve actual operation evidence")
			manager.response = json.RawMessage(`{"text":"current page"}`)
			next, err := tool.Execute(ctx, json.RawMessage(`{"method":"observe","keep_open":false}`))
			require.NoError(t, err)
			require.Equal(t, !tc.blocks, next.Success)
			if tc.blocks {
				require.Equal(t, 1, manager.calls, "cancelled help must not dispatch more operations")
			} else {
				require.Equal(t, 2, manager.calls, "a failed script must still allow state inspection")
			}
			tool.Cleanup(ctx)
			require.Equal(t, []bool{!tc.success}, manager.retained)
		})
	}
}

func TestBrowserErrorRecoveryHints(t *testing.T) {
	for _, tc := range []struct{ method, code, data, hint string }{
		{"click", "not_found", `{"reason":"ref_not_found"}`, "fresh ref"},
		{"click", "permission_denied", `{"reason":"element_not_visible"}`, "active dialog/menu"},
		{"fill", "cdp_failed", `{"reason":"fill_value_mismatch"}`, "remaining difference"},
		{"fill", "timeout", `{"reason":"fill_failed","effect_state":"unknown"}`, "may already have taken effect"},
		{"press", "invalid_params", `{}`, "use fill"},
		{"click", "user_aborted", `{"effect_state":"unknown"}`, "Stop browser actions"},
		{"observe", "task_paused", `{}`, "does not require reconnection"},
		{"request_help", "timeout", `{}`, "conversation browser preview"},
	} {
		t.Run(tc.code+tc.data, func(t *testing.T) {
			err := &browserskill.RPCError{Code: tc.code, Message: "browser failure", Data: json.RawMessage(tc.data)}
			result := browserToolFailure(tc.method, err)
			require.False(t, result.Success)
			require.Equal(t, err.Error(), result.Error)
			var output struct {
				Error *browserskill.RPCError `json:"error"`
				Hint  string                 `json:"recovery_hint"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Output), &output))
			require.Contains(t, output.Hint, tc.hint)
			require.JSONEq(t, tc.data, string(output.Error.Data))
		})
	}
}

func TestBrowserRPCFailureReachesModelAndAllowsFreshObservation(t *testing.T) {
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	manager := &browserLifecycleManager{callErr: &browserskill.RPCError{
		Code: "not_found", Message: "ref missing", Data: json.RawMessage(`{"reason":"ref_not_found"}`),
	}}
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	tool.manager = manager
	registry := NewToolRegistry()
	registry.RegisterTool(tool)
	failed, err := registry.ExecuteTool(ctx, "local_browser", json.RawMessage(`{"method":"click","ref":"e99"}`))
	require.NoError(t, err)
	require.False(t, failed.Success)
	require.Contains(t, failed.Output, "ref_not_found")
	require.Contains(t, failed.Output, "fresh ref")
	manager.callErr = nil
	fresh, err := registry.ExecuteTool(ctx, "local_browser", json.RawMessage(`{"method":"observe"}`))
	require.NoError(t, err)
	require.True(t, fresh.Success)
	require.Equal(t, 2, manager.calls)
	invalid, err := registry.ExecuteTool(
		ctx, "local_browser", json.RawMessage(`{"method":"observe","max_text_chars":3000}`),
	)
	require.NoError(t, err)
	require.False(t, invalid.Success)
	require.Contains(t, invalid.Error, "allowed fields:")
	require.Contains(t, invalid.Error, "max_tokens")
	require.Equal(t, 2, manager.calls, "invalid method fields must fail before dispatch")
}

func TestNavigationTimeoutRetainsTask(t *testing.T) {
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	manager := &browserLifecycleManager{response: json.RawMessage(
		`{"tab_id":1,"reached":"timeout","error_text":"timed out waiting for lifecycle"}`,
	)}
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "review")
	tool.manager = manager
	result, err := tool.Execute(ctx, json.RawMessage(`{"method":"navigate","url":"https://example.com"}`))
	require.NoError(t, err)
	tool.Cleanup(ctx)
	t.Logf("success=%v retained=%v output=%s", result.Success, manager.retained, result.Output)
	require.False(t, result.Success, "navigation that did not reach requested phase must not count as completed")
	require.Equal(t, []bool{true}, manager.retained, "unfinished navigation should retain pages")
}
