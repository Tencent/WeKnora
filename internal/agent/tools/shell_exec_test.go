package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeShellExecutor struct {
	result  *sandbox.ExecuteResult
	err     error
	timeout time.Duration
}

func (f *fakeShellExecutor) ExecShellCommand(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	timeout time.Duration,
	_ map[string]string,
) (*sandbox.ExecuteResult, error) {
	f.timeout = timeout
	if f.err != nil {
		return nil, f.err
	}
	if f.result == nil {
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	return f.result, nil
}

func shellExecTestContext() context.Context {
	return WithToolExecContext(context.Background(), &ToolExecContext{SessionID: "session-1"})
}

func TestShellExecRejectsWorkDirOutsideWorkspace(t *testing.T) {
	executor := &fakeShellExecutor{}
	tool := NewShellExecTool(executor)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"pwd","work_dir":"/etc"}`,
	))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, `work_dir "/etc" is outside the allowed sandbox roots /workspace`)
	assert.Equal(t, time.Duration(0), executor.timeout)
}

func TestShellExecTimeoutHonorsAndCapsRequestedValue(t *testing.T) {
	executor := &fakeShellExecutor{}
	tool := NewShellExecTool(executor)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"sleep 1","timeout_sec":999}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, shellExecMaxTimeout, executor.timeout)
}

type fakeInstallShellExecutor struct {
	timeout time.Duration
}

func (f *fakeInstallShellExecutor) ExecShellCommandWithOptions(
	_ context.Context, _ string, _ string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	f.timeout = opts.Timeout
	return &sandbox.ExecuteResult{ExitCode: 0}, nil
}

func TestInstallShellExecDefaultsToTheTenMinuteBudget(t *testing.T) {
	inner := &fakeInstallShellExecutor{}
	tool := NewInstallShellExecTool(inner)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"pip install -r requirements.txt"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, shellExecMaxTimeout, inner.timeout,
		"install-mode shell_exec must not keep the ordinary 120s default")
}

func TestShellExecConfigurableOutputAndRegistryOverride(t *testing.T) {
	content := strings.Repeat("x", 24*1024)
	executor := &fakeShellExecutor{result: &sandbox.ExecuteResult{
		Stdout:   content,
		ExitCode: 0,
	}}
	registry := NewToolRegistry()
	registry.RegisterTool(NewShellExecTool(executor))

	result, err := registry.ExecuteTool(shellExecTestContext(), ToolShellExec, json.RawMessage(
		`{"command":"cat large.txt","max_output_bytes":32768}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Output, content)
	assert.NotContains(t, result.Output, "output truncated")
	assert.Equal(t, 32768, result.Data["max_output_bytes"])
	assert.Equal(t, "shell_exec", result.Data["display_type"])
}

func TestShellExecOutputLimitIsHardCapped(t *testing.T) {
	tool := NewShellExecTool(&fakeShellExecutor{})

	limit := tool.OutputLimitChars(json.RawMessage(`{"max_output_bytes":999999}`))

	assert.Equal(t, maxShellExecVisibleBytes, limit)
}

func TestShellExecSuppressesBinaryStreams(t *testing.T) {
	binary := "prefix\x00\x01payload"
	executor := &fakeShellExecutor{result: &sandbox.ExecuteResult{
		Stdout:   binary,
		Stderr:   "text error",
		ExitCode: 0,
	}}
	tool := NewShellExecTool(executor)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(`{"command":"cat image.bin"}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.NotContains(t, result.Output, binary)
	assert.Contains(t, result.Output, "Binary Output Suppressed")
	assert.Equal(t, "", result.Data["stdout"])
	assert.Equal(t, true, result.Data["stdout_binary"])
	assert.Equal(t, len(binary), result.Data["stdout_bytes"])
	assert.Equal(t, "text error", result.Data["stderr"])
}

func TestShellExecDescriptionSupportsGeneralExploration(t *testing.T) {
	description := NewShellExecTool(&fakeShellExecutor{}).Description()

	for _, command := range []string{"find", "file", "sed", "head", "tail", "cat", "grep", "awk"} {
		assert.Contains(t, description, command)
	}
	assert.Contains(t, description, "Use freely to explore")
	assert.Contains(t, description, "Binary output is never returned")
}

func TestShellExecBoundsStdoutStderrErrorAndTotal(t *testing.T) {
	executor := &fakeShellExecutor{result: &sandbox.ExecuteResult{
		Stdout: strings.Repeat("o", 100*1024),
		Stderr: strings.Repeat("s", 40*1024) + "TAIL_ERROR",
		Error:  strings.Repeat("e", 8*1024),
	}}

	result, err := NewShellExecTool(executor).Execute(
		shellExecTestContext(),
		json.RawMessage(`{"command":"noisy","max_output_bytes":999999,"max_stderr_bytes":999999}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.LessOrEqual(t, len(result.Output), maxShellExecVisibleBytes)
	assert.LessOrEqual(t, result.Data["stdout_returned_bytes"].(int), maxShellExecOutputBytes)
	assert.LessOrEqual(t, result.Data["stderr_returned_bytes"].(int), maxShellExecStderrBytes)
	assert.LessOrEqual(t, result.Data["error_returned_bytes"].(int), maxShellExecErrorBytes)
	assert.Equal(t, true, result.Data["stdout_truncated"])
	assert.Equal(t, true, result.Data["stderr_truncated"])
	assert.Equal(t, true, result.Data["error_truncated"])
	assert.Contains(t, result.Data["stderr"], "TAIL_ERROR")
}

func TestShellExecBoundsExecutorErrors(t *testing.T) {
	executor := &fakeShellExecutor{err: errors.New(strings.Repeat("network failure ", 1024))}

	result, err := NewShellExecTool(executor).Execute(
		shellExecTestContext(),
		json.RawMessage(`{"command":"echo hi"}`),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.LessOrEqual(t, len(result.Error), maxShellExecErrorBytes)
	assert.Contains(t, result.Error, "truncated")
}

func TestTruncateShellStreamIncludesMarkerWithinLimit(t *testing.T) {
	output, truncated := truncateShellStream(strings.Repeat("x", 10000), 100)

	require.True(t, truncated)
	assert.LessOrEqual(t, len(output), 100)
	assert.Contains(t, output, "truncated")
}
