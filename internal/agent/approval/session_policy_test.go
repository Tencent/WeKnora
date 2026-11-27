package approval

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/policy"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sessionPolicyReader struct {
	rows  []*types.MCPToolSessionPolicy
	err   error
	calls int
}

func (r *sessionPolicyReader) ListByService(context.Context, uint64, string) ([]*types.MCPToolSessionPolicy, error) {
	r.calls++
	return r.rows, r.err
}

func imPolicyContext(platform, chatType, chatID string) context.Context {
	return types.WithIMConversation(context.Background(), types.IMConversation{
		Platform: platform, ChatType: chatType, ChatID: chatID,
	})
}

func TestIMSessionPolicyRestrictsDiscoveryAndExecution(t *testing.T) {
	reader := &sessionPolicyReader{rows: []*types.MCPToolSessionPolicy{{
		TenantID: 7, ServiceID: "svc", ToolName: "change_ticket",
		AllowedSessions: []types.MCPToolSessionRule{{Platform: "feishu", ChatType: "group", ChatID: "ops"}},
	}}}
	gate := NewGate(nil, &stubChecker{required: true}, nil).WithSessionPolicies(reader)
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		expected bool
	}{
		{"allowed group", imPolicyContext("feishu", "group", "ops"), true},
		{"other group", imPolicyContext("feishu", "group", "sales"), false},
		{"other platform", imPolicyContext("lark", "group", "ops"), false},
		{"direct message", imPolicyContext("feishu", "direct", "ops"), false},
		{"missing ID", imPolicyContext("feishu", "group", ""), false},
		{"missing IM identity", types.WithPrincipalAuthSource(context.Background(), types.AuthSourceIM), false},
		{"web", context.Background(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, err := gate.IsEnabled(tc.ctx, 7, "svc", "change_ticket")
			require.NoError(t, err)
			require.Equal(t, tc.expected, enabled)
			directory, err := gate.EnabledTools(tc.ctx, 7, "svc", []string{"change_ticket", "unrestricted"})
			require.NoError(t, err)
			require.Equal(t, tc.expected, directory["change_ticket"])
			require.True(t, directory["unrestricted"])
		})
	}
	// A matching allowlist cannot bypass the existing mandatory approval.
	action := gate.Action(imPolicyContext("feishu", "group", "ops"), 7, "svc", &types.MCPTool{Name: "change_ticket"})
	require.True(t, action.Always)
	require.Equal(t, policy.RiskWrite, action.Risk)
}

func TestIMSessionPolicyNeverReenablesDisabledTools(t *testing.T) {
	reader := &sessionPolicyReader{err: errors.New("must not read")}
	gate := NewGate(nil, &stubChecker{enabled: boolPtr(false)}, nil).WithSessionPolicies(reader)
	enabled, err := gate.IsEnabled(imPolicyContext("feishu", "group", "ops"), 7, "svc", "disabled")
	require.NoError(t, err)
	require.False(t, enabled)
	require.Zero(t, reader.calls)
}

func TestIMSessionPolicyFailureIsClosedAndNonIMDoesNotRead(t *testing.T) {
	reader := &sessionPolicyReader{err: errors.New("policy store unavailable")}
	gate := NewGate(nil, &stubChecker{}, nil).WithSessionPolicies(reader)
	enabled, err := gate.IsEnabled(imPolicyContext("feishu", "group", "ops"), 7, "svc", "tool")
	require.Error(t, err)
	require.False(t, enabled)
	_, err = gate.EnabledTools(imPolicyContext("feishu", "group", "ops"), 7, "svc", []string{"tool"})
	require.Error(t, err)
	reader.calls = 0
	for _, source := range []string{types.AuthSourceWeb, types.AuthSourceAPITenantKey, types.AuthSourceInternal, ""} {
		ctx := types.WithPrincipalAuthSource(context.Background(), source)
		enabled, err := gate.IsEnabled(ctx, 7, "svc", "tool")
		require.NoError(t, err)
		require.True(t, enabled)
		_, err = gate.EnabledTools(ctx, 7, "svc", []string{"tool"})
		require.NoError(t, err)
	}
	require.Zero(t, reader.calls)
}

func TestIMSessionPolicyIgnoresOtherTenantAndServiceRows(t *testing.T) {
	reader := &sessionPolicyReader{rows: []*types.MCPToolSessionPolicy{
		{TenantID: 8, ServiceID: "svc", ToolName: "tool"},
		{TenantID: 7, ServiceID: "other", ToolName: "tool"},
	}}
	gate := NewGate(nil, &stubChecker{}, nil).WithSessionPolicies(reader)
	enabled, err := gate.IsEnabled(imPolicyContext("feishu", "group", "ops"), 7, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestIMSessionPolicySurvivesDetachedContext(t *testing.T) {
	reader := &sessionPolicyReader{rows: []*types.MCPToolSessionPolicy{
		{TenantID: 7, ServiceID: "svc", ToolName: "tool"},
	}}
	gate := NewGate(nil, &stubChecker{}, nil).WithSessionPolicies(reader)
	ctx := logger.CloneContext(imPolicyContext("feishu", "group", "sales"))
	enabled, err := gate.IsEnabled(ctx, 7, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled, "detaching QA work must not turn an IM request into an unrestricted web request")
}
