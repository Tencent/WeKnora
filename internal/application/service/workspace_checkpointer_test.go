package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

type fakeShellRunner struct {
	calls   []string
	result  *sandbox.ExecuteResult
	err     error
	timeout time.Duration
	workDir string
}

func (f *fakeShellRunner) ExecShellCommand(
	_ context.Context, _ string, command, workDir string,
	timeout time.Duration, _ map[string]string,
) (*sandbox.ExecuteResult, error) {
	f.calls = append(f.calls, command)
	f.timeout = timeout
	f.workDir = workDir
	return f.result, f.err
}

func TestCheckpointReturnsShaFromLastStdoutLine(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0,
		Stdout:   "Initialized empty Git repository\n1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	got := cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1")

	require.NotNil(t, got)
	require.Equal(t, "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", got.CommitSHA)
	require.Equal(t, "sbx-1", got.SandboxID)
	require.False(t, got.CommittedAt.IsZero())
}

func TestCheckpointScriptShape(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: strings.Repeat("a", 40) + "\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-42")

	require.Len(t, runner.calls, 1)
	script := runner.calls[0]

	// safe.directory must precede the rev-parse probe: the probe itself is
	// what dubious-ownership blocks.
	require.Less(t,
		strings.Index(script, "safe.directory"),
		strings.Index(script, "rev-parse --git-dir"),
	)
	// Plain assignment, not --add: safe.directory is multi-valued and --add
	// would append a duplicate line to ~/.gitconfig on every single turn.
	require.NotContains(t, script, "--add safe.directory")
	require.Contains(t, script, "--allow-empty")
	require.Contains(t, script, "turn:msg-42")
	require.NotContains(t, script, ".gitignore")
	require.NotContains(t, script, "printf 'input/\\noutput/\\n'")
	require.Equal(t, workspaceCheckpointTimeout, runner.timeout)
	require.Equal(t, sandbox.SessionWorkspaceRoot, runner.workDir)
}

func TestCheckpointReturnsNilWhenExecFails(t *testing.T) {
	runner := &fakeShellRunner{err: errors.New("sandbox unreachable")}
	cp := NewWorkspaceCheckpointer(runner)

	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilOnNonZeroExit(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 128, Stderr: "fatal: not a git repository",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilWhenStdoutIsNotASha(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: "warning: something odd\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilWhenRunnerMissing(t *testing.T) {
	require.Nil(t, NewWorkspaceCheckpointer(nil).Checkpoint(
		context.Background(), "s1", "sbx-1", "msg-1"))

	var nilCheckpointer *WorkspaceCheckpointer
	require.Nil(t, nilCheckpointer.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilWithoutSandboxID(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: strings.Repeat("a", 40) + "\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	// No bound sandbox means there is nothing to check point, and a checkpoint
	// without a sandbox ID could never be validated at fork time.
	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "", "msg-1"))
	require.Empty(t, runner.calls)
}
