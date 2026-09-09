package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

// A controlled test executor exercises stdin's shell quoting with real Bash.
type stdinTestExecutor struct{}

func (stdinTestExecutor) ExecShellCommand(ctx context.Context, _, command, _ string, _ time.Duration, _ map[string]string) (*sandbox.ExecuteResult, error) {
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return &sandbox.ExecuteResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: cmd.ProcessState.ExitCode()}, err
}

func TestShellStdinPreservesLiteralDataForTheEntireCommand(t *testing.T) {
	stdin := "first 'quoted' line\n\"$(printf must-not-expand)\"; $HOME\\\n中文\n\n"
	input := ShellExecInput{Command: `IFS= read -r first; printf '<%s>\n' "$first"; cat`, Stdin: stdin}
	args, err := json.Marshal(input)
	require.NoError(t, err)
	result, err := NewShellExecTool(stdinTestExecutor{}, nil).Execute(shellExecTestContext(), args)
	require.NoError(t, err)
	require.True(t, result.Success, "%+v", result)
	require.Equal(t, "<first 'quoted' line>\n"+strings.SplitN(stdin, "\n", 2)[1], result.Data["stdout"])
}

func TestShellRejectsOversizeInputAndResolverFailuresBeforeExecution(t *testing.T) {
	executor := &fakeShellExecutor{}
	args, err := json.Marshal(ShellExecInput{Command: "cat", Stdin: strings.Repeat("x", 65537)})
	require.NoError(t, err)
	result, err := NewShellExecTool(executor, nil).Execute(shellExecTestContext(), args)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, executor.calls)
	resolver := stubEnvResolver{err: errors.New("credentials unavailable")}
	result, err = NewShellExecTool(executor, resolver).WithSkillEnvironment(shellTestSkillEnvironment(t)).Execute(shellExecTestContext(), json.RawMessage(`{"skill_name":"pdf-tools","command":"true"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "credentials unavailable")
	require.Zero(t, executor.calls)
}

func TestShellRejectsStdinWhenTheCommandWouldExecuteIt(t *testing.T) {
	executor := &fakeShellExecutor{}
	tool := NewShellExecTool(executor, nil)
	oversize, err := json.Marshal(ShellExecInput{Command: "python3", Stdin: strings.Repeat("x", shellExecMaxCommandBytes+1)})
	require.NoError(t, err)
	result, err := tool.Execute(shellExecTestContext(), oversize)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "stdin program too long")
	require.Zero(t, executor.calls)

	bomb, err := json.Marshal(ShellExecInput{Command: "bash -s", Stdin: ":(){ :|:& };:"})
	require.NoError(t, err)
	result, err = tool.Execute(shellExecTestContext(), bomb)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "fork_bomb")
	require.Zero(t, executor.calls)

	root, err := json.Marshal(ShellExecInput{Command: "/bin/python3 -", Stdin: "rm -rf /\n"})
	require.NoError(t, err)
	result, err = tool.Execute(shellExecTestContext(), root)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "rm_root")
	require.Zero(t, executor.calls)

	script, err := json.Marshal(ShellExecInput{Command: "python3 script.py", Stdin: "rm -rf /\n"})
	require.NoError(t, err)
	result, err = tool.Execute(shellExecTestContext(), script)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, executor.calls)

	data, err := json.Marshal(ShellExecInput{Command: "cat", Stdin: ":(){ :|:& };:\nrm -rf /\n"})
	require.NoError(t, err)
	result, err = tool.Execute(shellExecTestContext(), data)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 2, executor.calls)
}
