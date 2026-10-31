package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type fakeSubmitter struct {
	requests []ShellJobSubmitRequest
	refs     []interfaces.SessionRef
	response ShellJobSubmission
	err      error
	ended    int
}

func (f *fakeSubmitter) SubmitShellJob(_ context.Context, ref interfaces.SessionRef,
	req ShellJobSubmitRequest,
) (ShellJobSubmission, error) {
	f.requests = append(f.requests, req)
	f.refs = append(f.refs, ref)
	return f.response, f.err
}
func (f *fakeSubmitter) EndShellJobRun(interfaces.SessionRef, string) { f.ended++ }

type holdingSubmitter struct {
	fakeSubmitter
	held func(context.Context)
}

func (h *holdingSubmitter) HoldSkillEnvCapture(
	_ context.Context, _ interfaces.SessionRef, _ string, run func(context.Context),
) {
	h.held = run
}

func asyncTestCtx() context.Context {
	// The turn's session tenant is SandboxTenantIDFromContext, not a tool-context helper.
	ctx := types.WithSandboxTenantID(context.Background(), 7)
	return WithToolExecContext(ctx, &ToolExecContext{
		SessionID: "session-1", AssistantMessageID: "m1", ToolCallID: "tc1",
	})
}

func runAsyncTool(t *testing.T, submitter ShellJobSubmitter, args string) (*types.ToolResult, error) {
	t.Helper()
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil)
	return tool.Execute(asyncTestCtx(), json.RawMessage(args))
}

func TestShellExecAsyncRejectsBlacklistedAndMissingCommand(t *testing.T) {
	submitter := &fakeSubmitter{}
	for _, args := range []string{
		`{"command":"sleep 5 &"}`,
		`{"command":"nohup make build"}`,
		`{"command":"  "}`,
	} {
		result, err := runAsyncTool(t, submitter, args)
		require.NoError(t, err)
		require.False(t, result.Success, args)
		require.Empty(t, submitter.requests, args)
	}
}

func TestShellExecAsyncClampsYieldAndPassesContext(t *testing.T) {
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result:     interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateRunning},
		Background: true,
	}}
	result, err := runAsyncTool(t, submitter,
		`{"command":"make build","description":"build","yield_time_ms":999999,"skill_name":""}`)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Len(t, submitter.requests, 1)
	require.Equal(t, 30*time.Second, submitter.requests[0].YieldTime, "夹到 30000ms 上限")
	require.Equal(t, "m1", submitter.requests[0].ParentMessageID)
	require.Equal(t, "tc1", submitter.requests[0].ToolCallID)
	require.Equal(t, "build", submitter.requests[0].Description)
	require.Equal(t,
		commandInWorkDir("/workspace", sandbox.WithSessionDisplay("make build")),
		submitter.requests[0].Command)
	require.Equal(t, "make build", submitter.requests[0].DisplayCommand)
	require.Equal(t, "/workspace", submitter.requests[0].WorkDir)
	require.Equal(t, interfaces.SessionRef{TenantID: 7, SessionID: "session-1"}, submitter.refs[0])
}

func TestShellExecAsyncMarksStartedOnRunningHandle(t *testing.T) {
	marks := &recordingMarks{}
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result:     interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateRunning},
		Background: true,
	}}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "session-1"}
	tool.BindBackgroundRun(ref, "m1", marks)
	_, err := tool.Execute(asyncTestCtx(), json.RawMessage(`{"command":"make build"}`))
	require.NoError(t, err)
	require.Equal(t, []string{"sj_0123"}, marks.started)
	require.NoError(t, tool.Cleanup(context.Background()))
	require.Equal(t, 1, submitter.ended, "Cleanup 通知 run 结束（cursor 一小时后过期）")
}

func TestShellExecAsyncShortResultCarriesPayload(t *testing.T) {
	exit := 0
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result: interfaces.WorkResult{
			ID: "sj_0123", State: interfaces.WorkStateSucceeded,
			Terminal: true, Payload: map[string]any{"exit_code": exit, "stdout": "ok"},
		},
		Background: false,
	}}
	result, err := runAsyncTool(t, submitter, `{"command":"true"}`)
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data["shell_job"].(map[string]any)
	require.Equal(t, "sj_0123", data["work_id"])
	require.Equal(t, "succeeded", data["state"])
	require.Equal(t, "ok", data["stdout"])
}

func TestShellExecAsyncDirectResultDoesNotMarkStarted(t *testing.T) {
	marks := &recordingMarks{}
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result: interfaces.WorkResult{
			ID: "sj_0123", State: interfaces.WorkStateSucceeded,
			Terminal: true, Payload: map[string]any{"stdout": "ok"},
		},
		Background: false,
	}}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "session-1"}
	tool.BindBackgroundRun(ref, "m1", marks)
	ctx := types.WithSandboxTenantID(context.Background(), 99)
	ctx = WithToolExecContext(ctx, &ToolExecContext{
		SessionID: "other", AssistantMessageID: "m-other", ToolCallID: "tc-other",
	})
	_, err := tool.Execute(ctx, json.RawMessage(`{"command":"true"}`))
	require.NoError(t, err)
	require.Empty(t, marks.started)
	require.Equal(t, ref, submitter.refs[0])
	require.Equal(t, "m1", submitter.requests[0].RunID)
}

func TestShellExecAsyncWrapsStdinBeforeSubmit(t *testing.T) {
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result: interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateSucceeded, Terminal: true},
	}}
	stdin := "hello 'world'\n"
	args, err := json.Marshal(ShellExecAsyncInput{Command: "cat", Stdin: stdin})
	require.NoError(t, err)
	result, err := runAsyncTool(t, submitter, string(args))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Len(t, submitter.requests, 1)
	encoded := base64.StdEncoding.EncodeToString([]byte(stdin))
	inner := "printf %s " + sandbox.ShellQuote(encoded) +
		" | base64 -d | /bin/bash --noprofile --norc -c " + sandbox.ShellQuote("cat")
	require.Equal(t, commandInWorkDir(sandbox.SessionWorkspaceRoot, sandbox.WithSessionDisplay(inner)),
		submitter.requests[0].Command)
	require.Equal(t, "cat", submitter.requests[0].DisplayCommand)
	require.Equal(t, sandbox.SessionWorkspaceRoot, submitter.requests[0].WorkDir)
}

func TestShellExecAsyncRejectsOversizeAndExecutableStdin(t *testing.T) {
	submitter := &fakeSubmitter{}
	oversize, err := json.Marshal(ShellExecAsyncInput{Command: "cat", Stdin: strings.Repeat("x", 65537)})
	require.NoError(t, err)
	result, err := runAsyncTool(t, submitter, string(oversize))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "65536")
	require.Empty(t, submitter.requests)

	bomb, err := json.Marshal(ShellExecAsyncInput{Command: "bash -s", Stdin: ":(){ :|:& };:"})
	require.NoError(t, err)
	result, err = runAsyncTool(t, submitter, string(bomb))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "fork_bomb")
	require.Empty(t, submitter.requests)
}

func TestShellExecAsyncReportsActivityLimit(t *testing.T) {
	submitter := &fakeSubmitter{err: interfaces.ErrActivityLimit}
	result, err := runAsyncTool(t, submitter, `{"command":"make build"}`)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "ERR_WORK_LIMIT")
}

func TestShellExecAsyncRejectsSkillWithoutEnvironment(t *testing.T) {
	submitter := &fakeSubmitter{}
	result, err := runAsyncTool(t, submitter, `{"command":"python3 report.py","skill_name":"pdf"}`)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "skill environment")
	require.Empty(t, submitter.requests)
}

func TestShellExecAsyncRequiresSessionTenant(t *testing.T) {
	submitter := &fakeSubmitter{}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil)
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID: "session-1", AssistantMessageID: "m1", ToolCallID: "tc1",
	})
	result, err := tool.Execute(ctx, json.RawMessage(`{"command":"true"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "tenant")
	require.Empty(t, submitter.requests)
}

func TestShellExecAsyncCapturesSkillEnvWhenTheJobFinishesInTheWindow(t *testing.T) {
	recorder := &recordedCapture{}
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result: interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateSucceeded},
	}}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil).
		WithSkillEnvironment(shellTestSkillEnvironment(t)).
		WithEnvCapture(recorder.capture)
	result, err := tool.Execute(asyncTestCtx(), json.RawMessage(
		`{"command":"export USER_TOKEN=from-command; python x.py",`+
			`"skill_name":"pdf-tools","env":{"EXTRA_TOKEN":"from-tool"}}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, recorder.calls)
	require.Equal(t, "pdf-tools", recorder.skillName)
	require.Equal(t, "from-command", recorder.pairs["USER_TOKEN"])
	require.Equal(t, "from-tool", recorder.pairs["EXTRA_TOKEN"])
}

func TestShellExecAsyncHoldsSkillEnvUntilTheBackgroundJobSucceeds(t *testing.T) {
	recorder := &recordedCapture{}
	submitter := &holdingSubmitter{fakeSubmitter: fakeSubmitter{response: ShellJobSubmission{
		Result:     interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateRunning},
		Background: true,
	}}}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil).
		WithSkillEnvironment(shellTestSkillEnvironment(t)).
		WithEnvCapture(recorder.capture)
	result, err := tool.Execute(asyncTestCtx(), json.RawMessage(
		`{"command":"export USER_TOKEN=from-command; python x.py","skill_name":"pdf-tools"}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Zero(t, recorder.calls)
	require.NotNil(t, submitter.held)
	submitter.held(context.Background())
	require.Equal(t, 1, recorder.calls)
	require.Equal(t, "from-command", recorder.pairs["USER_TOKEN"])
}

func TestShellExecAsyncDoesNotCaptureAFailedInWindowJob(t *testing.T) {
	recorder := &recordedCapture{}
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result: interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateFailed},
	}}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil).
		WithSkillEnvironment(shellTestSkillEnvironment(t)).
		WithEnvCapture(recorder.capture)
	result, err := tool.Execute(asyncTestCtx(), json.RawMessage(
		`{"command":"export USER_TOKEN=from-command; python x.py","skill_name":"pdf-tools"}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Zero(t, recorder.calls)
}

func TestShellExecAsyncDoesNotRewriteResolvedSkillEnv(t *testing.T) {
	recorder := &recordedCapture{}
	submitter := &fakeSubmitter{response: ShellJobSubmission{
		Result: interfaces.WorkResult{ID: "sj_0123", State: interfaces.WorkStateSucceeded},
	}}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, stubEnvResolver{
		resolved: map[string]string{"USER_TOKEN": "already-stored"},
	}).
		WithSkillEnvironment(shellTestSkillEnvironment(t)).
		WithEnvCapture(recorder.capture)
	result, err := tool.Execute(asyncTestCtx(), json.RawMessage(
		`{"command":"export USER_TOKEN=from-command; python x.py",`+
			`"skill_name":"pdf-tools","env":{"EXTRA_TOKEN":"from-tool"}}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, recorder.calls)
	require.NotContains(t, recorder.pairs, "USER_TOKEN")
	require.Equal(t, "from-tool", recorder.pairs["EXTRA_TOKEN"])
}

func TestShellExecDescriptionPointsAtAsync(t *testing.T) {
	needle := "use `shell_exec_async` instead: short commands still return their result directly."
	require.Contains(t, shellExecDescription(sandbox.RemoteWorkspaceLayout()), needle)
	require.Contains(t, shellExecDescription(sandbox.WorkspaceLayout{
		Origin: sandbox.WorkspaceOriginHost, Root: "/Users/dev/proj",
	}), needle)
}

func TestShellExecAsyncCleanupRunsFromRegistry(t *testing.T) {
	submitter := &fakeSubmitter{}
	tool := NewShellExecAsyncTool(submitter, &fakeShellExecutor{}, nil)
	tool.BindBackgroundRun(interfaces.SessionRef{TenantID: 7, SessionID: "session-1"}, "m1", &recordingMarks{})
	registry := NewToolRegistry()
	registry.RegisterTool(tool)
	registry.Cleanup(context.Background())
	require.Equal(t, 1, submitter.ended)
}
