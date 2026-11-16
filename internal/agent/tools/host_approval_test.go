package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/hostreview"
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
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: gate})
	_, toolCtx := hostExecContext(t)

	ctx := tool.hostApprovalContext(toolCtx, ShellExecInput{Command: "rm a.txt", Stdin: ""})
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

func TestHostApprovalContextKeepsTheOutputBudget(t *testing.T) {
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: &fakeHostGate{}})
	_, toolCtx := hostExecContext(t)

	ctx := tool.hostApprovalContext(WithOutputBudget(toolCtx, 1234), ShellExecInput{Command: "rm a.txt"})
	require.Equal(t, 1234, OutputBudget(ctx))
}

func TestHostApprovalSessionScopeNeedsPermission(t *testing.T) {
	gate := &fakeHostGate{decision: approval.Decision{Approved: true, Scope: "session"}}
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: gate})
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(
		toolCtx, ShellExecInput{Command: "rm -rf ~", Stdin: ""},
	))

	d, err := port.Approver.ApproveCommand(context.Background(), sandbox.CommandApprovalRequest{
		Reason: "dangerous", AllowSession: false,
	})
	require.NoError(t, err)
	require.False(t, d.Session)
}

func TestHostApprovalTimeoutIsAnError(t *testing.T) {
	gate := &fakeHostGate{decision: approval.Decision{TimedOut: true, Reason: "approval timeout"}}
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: gate})
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(
		toolCtx, ShellExecInput{Command: "rm a", Stdin: ""},
	))

	_, err := port.Approver.ApproveCommand(context.Background(), sandbox.CommandApprovalRequest{Reason: "delete"})
	require.ErrorContains(t, err, "timed out")
}

func TestHostApprovalReviewIncludesStdinProgram(t *testing.T) {
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: &fakeHostGate{}})
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(
		toolCtx, ShellExecInput{Command: "bash", Stdin: "rm -rf out\n"},
	))
	require.Equal(t, "bash", port.Command)
	require.Equal(t, "rm -rf out\n", port.Stdin)
}

func TestHostApprovalReviewIncludesStdinBehindAWrapper(t *testing.T) {
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: &fakeHostGate{}})
	_, toolCtx := hostExecContext(t)
	script := "rm -rf ~/Desktop/secret\n"
	for _, command := range []string{"true && bash", "env bash", "bash -c bash"} {
		port, ok := sandbox.CommandApprovalFrom(tool.hostApprovalContext(
			toolCtx, ShellExecInput{Command: command, Stdin: script},
		))
		require.True(t, ok)
		require.Equal(t, command, port.Command)
		require.Equalf(t, script, port.Stdin, "stdin of %q must be reviewed", command)
	}
}

func TestHostApprovalReviewCarriesDataOnStdinToo(t *testing.T) {
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Gate: &fakeHostGate{}})
	_, toolCtx := hostExecContext(t)
	port, _ := sandbox.CommandApprovalFrom(tool.hostApprovalContext(
		toolCtx, ShellExecInput{Command: "./tool", Stdin: "payload"},
	))
	require.Equal(t, "payload", port.Stdin)
	require.False(t, port.StdinIsProgram)

	port, _ = sandbox.CommandApprovalFrom(tool.hostApprovalContext(
		toolCtx, ShellExecInput{Command: "bash", Stdin: "ls"},
	))
	require.True(t, port.StdinIsProgram)
}

func TestHostPayloadShowsWhatTheCommandRunsWith(t *testing.T) {
	host := hostPayload(sandbox.CommandApprovalRequest{
		Command: "rm a", SkillName: "pdf",
		Env:     map[string]string{"BASH_ENV": "./x.sh", "GIT_SSH_COMMAND": "./y", "API_TOKEN": "s3cret"},
		Stdin:   "payload",
		Scripts: []sandbox.ScriptFact{{Path: "/w/x.py", Content: "print(1)"}},
	}, "user")
	require.Equal(t, "rm a", host.Command, "data on stdin is shown apart from the command")
	require.Equal(t, "payload", host.Stdin)
	require.Equal(t, "pdf", host.SkillName)
	require.Equal(t, "./x.sh", host.Env["BASH_ENV"])
	require.Equal(t, "./y", host.Env["GIT_SSH_COMMAND"])
	require.Equal(t, "***", host.Env["API_TOKEN"])
	require.Equal(t, []event.HostScript{{Path: "/w/x.py", Content: "print(1)"}}, host.Scripts)

	program := hostPayload(sandbox.CommandApprovalRequest{Command: "bash", Stdin: "ls", StdinIsProgram: true}, "user")
	require.Equal(t, "bash\nls", program.Command)
	require.Empty(t, program.Stdin)
}

func TestHostApprovalWithoutGateStillCarriesCommand(t *testing.T) {
	tool := NewShellExecTool(nil, nil)
	_, toolCtx := hostExecContext(t)
	port, ok := sandbox.CommandApprovalFrom(tool.hostApprovalContext(
		toolCtx, ShellExecInput{Command: "rm a", Stdin: ""},
	))
	require.True(t, ok)
	require.Nil(t, port.Approver)
	require.Equal(t, "rm a", port.Command)
}

type fakeHostReviewer struct {
	verdict  hostreview.Verdict
	turns    []hostreview.Turn
	requests []sandbox.CommandApprovalRequest
}

func (f *fakeHostReviewer) Review(
	_ context.Context, turn hostreview.Turn, req sandbox.CommandApprovalRequest,
) hostreview.Verdict {
	f.turns = append(f.turns, turn)
	f.requests = append(f.requests, req)
	return f.verdict
}

func TestModelApproverReviewsAndRecordsTheDecision(t *testing.T) {
	reviewer := &fakeHostReviewer{verdict: hostreview.Verdict{Approved: true, Reason: "asked for", RiskLevel: "low"}}
	tool := NewShellExecTool(nil, nil).WithHostApprovals(HostApprovals{Reviewer: reviewer})
	_, toolCtx := hostExecContext(t)
	meta, _ := ToolExecFromContext(toolCtx)
	var required []event.ToolApprovalRequiredData
	var resolved []event.ToolApprovalResolvedData
	meta.EventBus.On(event.EventToolApprovalRequired, func(_ context.Context, e event.Event) error {
		required = append(required, e.Data.(event.ToolApprovalRequiredData))
		return nil
	})
	meta.EventBus.On(event.EventToolApprovalResolved, func(_ context.Context, e event.Event) error {
		resolved = append(resolved, e.Data.(event.ToolApprovalResolvedData))
		return nil
	})

	ctx := tool.hostApprovalContext(toolCtx, ShellExecInput{
		Command: "rm x", Env: map[string]string{"A": "1"}, SkillName: "pdf",
	})
	port, ok := sandbox.CommandApprovalFrom(ctx)
	require.True(t, ok)
	require.Equal(t, map[string]string{"A": "1"}, port.Env)
	require.Equal(t, "pdf", port.SkillName)
	d, err := port.Reviewer.ApproveCommand(ctx, sandbox.CommandApprovalRequest{
		SessionID: "s1", Reason: "delete", AllowSession: true,
	})
	require.NoError(t, err)
	require.True(t, d.Approved)
	require.False(t, d.Session, "a model answer is never remembered")
	require.Equal(t, "low", d.RiskLevel)
	require.Equal(t, []hostreview.Turn{{SessionID: "s1", RequestID: "r1"}}, reviewer.turns)

	require.Len(t, required, 1)
	require.Equal(t, "model", required[0].Host.Reviewer)
	require.Len(t, resolved, 1)
	require.True(t, resolved[0].Approved)
	require.Equal(t, "low", resolved[0].RiskLevel)
	require.Equal(t, required[0].PendingID, resolved[0].PendingID)
}

func TestModelApproverFillsRequestIDFromContext(t *testing.T) {
	reviewer := &fakeHostReviewer{verdict: hostreview.Verdict{Approved: true}}
	parent := context.WithValue(context.Background(), types.RequestIDContextKey, "turn-9")
	parent = context.WithValue(parent, types.TenantIDContextKey, uint64(7))
	meta := &ToolExecContext{
		SessionID: "s1", AssistantMessageID: "m1", ToolCallID: "tc1",
		UserID: "u1", EventBus: event.NewEventBus(), ApprovalCtx: parent,
	}
	ctx := HostApprovals{Reviewer: reviewer}.attach(
		WithToolExecContext(parent, meta), sandbox.CommandApproval{Tool: ToolShellExec},
	)
	port, ok := sandbox.CommandApprovalFrom(ctx)
	require.True(t, ok)
	_, err := port.Reviewer.ApproveCommand(ctx, sandbox.CommandApprovalRequest{SessionID: "s1", Reason: "delete"})
	require.NoError(t, err)
	require.Equal(t, []hostreview.Turn{{SessionID: "s1", RequestID: "turn-9"}}, reviewer.turns)
}

func TestUserApproverNamesTheToolAndPreviewsTheWrite(t *testing.T) {
	gate := &fakeHostGate{decision: approval.Decision{Approved: true}}
	approvals := HostApprovals{Gate: gate}
	_, toolCtx := hostExecContext(t)
	ctx := approvals.attach(toolCtx, sandbox.CommandApproval{Tool: ToolWriteSandboxFile})
	port, _ := sandbox.CommandApprovalFrom(ctx)

	content := strings.Repeat("a", hostPreviewLimit+10)
	_, err := port.Approver.ApproveCommand(ctx, sandbox.CommandApprovalRequest{
		Tool:         ToolWriteSandboxFile,
		Reason:       "file_write",
		FilePath:     "/x/a.md",
		WriteMode:    "overwrite",
		WriteContent: content,
	})
	require.NoError(t, err)
	host := gate.requests[0].Host
	require.Equal(t, ToolWriteSandboxFile, gate.requests[0].Tool)
	require.Equal(t, "user", host.Reviewer)
	require.Equal(t, "/x/a.md", host.FilePath)
	require.Equal(t, len(content), host.WriteBytes)
	require.Len(t, host.WritePreview, hostPreviewLimit)

	_, err = port.Approver.ApproveCommand(ctx, sandbox.CommandApprovalRequest{
		Tool: ToolEditSandboxFile, Reason: "file_write", FilePath: "/x/a.md",
		WriteMode: "edit", EditDiff: "--- a\n+++ b\n", WriteBytes: 8,
	})
	require.NoError(t, err)
	require.Equal(t, 8, gate.requests[1].Host.WriteBytes)
	require.Equal(t, "--- a\n+++ b\n", gate.requests[1].Host.WritePreview)
}
