package tools

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeHostGate struct {
	decision approval.Decision
	requests []approval.HostPendingRequest
}

func (f *fakeHostGate) RequestHostAndWait(
	_ context.Context, req approval.HostPendingRequest,
) (approval.Decision, error) {
	f.requests = append(f.requests, req)
	return f.decision, nil
}

func hostExecContext(t *testing.T) (context.Context, context.Context) {
	t.Helper()
	parent := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	meta := &ToolExecContext{
		SessionID: "s1", AssistantMessageID: "m1", RequestID: "r1", ToolCallID: "tc1",
		UserID: "u1", EventBus: event.NewEventBus(), ApprovalCtx: parent,
	}
	toolCtx, cancel := context.WithTimeout(WithToolExecContext(parent, meta), time.Second)
	t.Cleanup(cancel)
	return parent, toolCtx
}

func TestHostApprovalContextAsksThroughGate(t *testing.T) {
	gate := &fakeHostGate{decision: approval.Decision{Approved: true, Scope: "session", Access: "read"}}
	tool := NewShellExecTool(nil, nil).WithHostApproval(gate)
	_, toolCtx := hostExecContext(t)

	ctx := tool.hostApprovalContext(toolCtx, "rm a.txt", "")
	port, ok := sandbox.CommandApprovalFrom(ctx)
	require.True(t, ok)
	require.Equal(t, "rm a.txt", port.Command)
	require.NotNil(t, port.Approver)
	_, hasDeadline := ctx.Deadline()
	require.False(t, hasDeadline, "waiting for the user must not eat the tool timeout")

	d, err := port.Approver.ApproveCommand(ctx, sandbox.CommandApprovalRequest{
		SessionID: "s1", Command: "rm a.txt", Cwd: "/w", Reason: "delete", AllowSession: true,
		SessionRules: []string{"rm"},
	})
	require.NoError(t, err)
	require.Equal(t, sandbox.CommandApprovalDecision{Approved: true, Session: true, Access: "read"}, d)
	req := gate.requests[0]
	require.Equal(t, uint64(7), req.TenantID)
	require.Equal(t, "u1", req.UserID)
	require.Equal(t, "tc1", req.ToolCallID)
	require.Equal(t, "rm a.txt", req.Host.Command)
	require.Equal(t, "delete", req.Host.Reason)
	require.Equal(t, []string{"rm"}, req.Host.SessionRules)
}

func TestHostApprovalSessionScopeNeedsPermission(t *testing.T) {
	gate := &fakeHostGate{decision: approval.Decision{Approved: true, Scope: "session"}}
	tool := NewShellExecTool(nil, nil).WithHostApproval(gate)
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(toolCtx, "rm -rf ~", ""))

	d, err := port.Approver.ApproveCommand(context.Background(), sandbox.CommandApprovalRequest{
		Reason: "dangerous", AllowSession: false,
	})
	require.NoError(t, err)
	require.False(t, d.Session)
}

func TestHostApprovalTimeoutIsAnError(t *testing.T) {
	gate := &fakeHostGate{decision: approval.Decision{TimedOut: true, Reason: "approval timeout"}}
	tool := NewShellExecTool(nil, nil).WithHostApproval(gate)
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(toolCtx, "rm a", ""))

	_, err := port.Approver.ApproveCommand(context.Background(), sandbox.CommandApprovalRequest{Reason: "delete"})
	require.ErrorContains(t, err, "timed out")
}

func TestHostApprovalReviewIncludesStdinProgram(t *testing.T) {
	tool := NewShellExecTool(nil, nil).WithHostApproval(&fakeHostGate{})
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(toolCtx, "bash", "rm -rf out\n"))
	require.Contains(t, port.Command, "rm -rf out")
}

func TestHostApprovalWithoutGateStillCarriesCommand(t *testing.T) {
	tool := NewShellExecTool(nil, nil)
	_, toolCtx := hostExecContext(t)
	port, ok := sandbox.CommandApprovalFrom(tool.hostApprovalContext(toolCtx, "rm a", ""))
	require.True(t, ok)
	require.Nil(t, port.Approver)
	require.Equal(t, "rm a", port.Command)
}
