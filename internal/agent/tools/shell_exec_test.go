package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeShellExecutor struct {
	result  *sandbox.ExecuteResult
	err     error
	timeout time.Duration
	calls   int
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
	f.calls++
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
	tool := NewShellExecTool(executor, nil)

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
	tool := NewShellExecTool(executor, nil)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"sleep 1","timeout_sec":999}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, shellExecMaxTimeout, executor.timeout)
}

type fakeInstallShellExecutor struct {
	timeout time.Duration
	workDir string
	calls   int
}

func (f *fakeInstallShellExecutor) ExecShellCommandWithOptions(
	_ context.Context, _ string, _ string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	f.timeout = opts.Timeout
	f.workDir = opts.WorkDir
	f.calls++
	return &sandbox.ExecuteResult{ExitCode: 0}, nil
}

// installShellSkillDir is the directory an install owns, and the working
// directory its shell must default to.
const installShellSkillDir = sandbox.SkillsImageRoot + "/pdf-tools"

func TestInstallShellExecDescriptionDoesNotPointAtWriteSandboxFile(t *testing.T) {
	description := NewInstallShellExecTool(&fakeInstallShellExecutor{}, installShellSkillDir).Description()

	assert.Contains(t, description, ToolWriteSkillFile,
		"the writer for this tree is write_skill_file, not a heredoc")
	assert.Contains(t, description, installShellSkillDir)
	assert.NotContains(t, description, "Do NOT dump large files through")
	assert.NotContains(t, NewShellExecTool(&fakeShellExecutor{}, nil).Description(),
		"write_sandbox_file is not available")
}

// The model reached for `cd <skill-dir> && …` on command after command because
// the default landed it in /workspace — a directory an install is told not to
// touch, since it is wiped before the snapshot. Naming the skill directory as
// the default is what removes the prefix.
func TestInstallShellExecDefaultsToTheSkillDirectory(t *testing.T) {
	inner := &fakeInstallShellExecutor{}
	tool := NewInstallShellExecTool(inner, installShellSkillDir)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"ls -la scripts/"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, installShellSkillDir, inner.workDir)
	assert.Equal(t, installShellSkillDir, result.Data["work_dir"],
		"the transcript must show where the command actually ran")

	assert.Contains(t, tool.Description(), "Do NOT prefix",
		"the default alone did not stop the model; it has to be told")
}

// An explicit work_dir still wins: an install occasionally has to leave its own
// directory, and the allowed roots have not changed.
func TestInstallShellExecStillHonoursAnExplicitWorkDir(t *testing.T) {
	inner := &fakeInstallShellExecutor{}

	result, err := NewInstallShellExecTool(inner, installShellSkillDir).Execute(
		shellExecTestContext(),
		json.RawMessage(`{"command":"ls","work_dir":"/workspace"}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, "/workspace", inner.workDir)
}

// A run that somehow carries no valid skill directory must not invent one, and
// must not send commands to a path outside the allowed roots.
func TestInstallShellExecFallsBackToWorkspaceWithoutAValidSkillDir(t *testing.T) {
	for _, dir := range []string{"", sandbox.SkillsImageRoot, "/etc", "/opt/weknora/tenant/skills/a/b"} {
		inner := &fakeInstallShellExecutor{}

		result, err := NewInstallShellExecTool(inner, dir).Execute(
			shellExecTestContext(), json.RawMessage(`{"command":"ls"}`))

		require.NoError(t, err)
		require.True(t, result.Success, result.Error)
		assert.Equal(t, "/workspace", inner.workDir, "skillDir %q must not become a default", dir)
	}
}

func TestInstallShellExecDefaultsToTheTenMinuteBudget(t *testing.T) {
	inner := &fakeInstallShellExecutor{}
	tool := NewInstallShellExecTool(inner, installShellSkillDir)

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
	registry.RegisterTool(NewShellExecTool(executor, nil))

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
	tool := NewShellExecTool(&fakeShellExecutor{}, nil)

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
	tool := NewShellExecTool(executor, nil)

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

func TestShellExecDescriptionDefinesOneExecutionEntry(t *testing.T) {
	description := NewShellExecTool(&fakeShellExecutor{}, nil).Description()
	for _, fact := range []string{"/workspace", "skill_name", "virtualenv", "read-only", "non-root", "write_sandbox_file", "edit_sandbox_file", "not automatically saved"} {
		require.Contains(t, description, fact)
	}
	require.NotContains(t, description, "execute_skill_script")
	require.Less(t, len(description), 2500)
}

func TestShellExecBoundsStdoutStderrErrorAndTotal(t *testing.T) {
	executor := &fakeShellExecutor{result: &sandbox.ExecuteResult{
		Stdout: strings.Repeat("o", 100*1024),
		Stderr: strings.Repeat("s", 40*1024) + "TAIL_ERROR",
		Error:  strings.Repeat("e", 8*1024),
	}}

	result, err := NewShellExecTool(executor, nil).Execute(
		shellExecTestContext(),
		json.RawMessage(`{"command":"noisy","max_output_bytes":999999,"max_stderr_bytes":999999}`),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
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

	result, err := NewShellExecTool(executor, nil).Execute(
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

type recordedCapture struct {
	skillName string
	pairs     map[string]string
	calls     int
}

func (r *recordedCapture) capture(_ context.Context, skillName string, pairs map[string]string) {
	r.calls++
	r.skillName = skillName
	r.pairs = pairs
}

// stubEnvResolver stands in for the service-layer resolver: resolved is what
// the workspace or this caller already has stored, missing is what a required
// declaration still needs.
type stubEnvResolver struct {
	err      error
	resolved map[string]string
	missing  []string
}

func (r stubEnvResolver) ResolveEnv(
	_ context.Context, _ string,
) (map[string]string, []string, error) {
	return r.resolved, r.missing, r.err
}

func TestShellExecCapturesUsedEnvAfterSuccessfulCommand(t *testing.T) {
	recorder := &recordedCapture{}
	tool := NewShellExecTool(&fakeShellExecutor{}, nil).WithEnvCapture(recorder.capture).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"export USER_TOKEN=from-command; python x.py","skill_name":"pdf-tools","env":{"EXTRA_TOKEN":"from-tool"}}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, recorder.calls)
	require.Equal(t, "pdf-tools", recorder.skillName)
	require.Equal(t, "from-command", recorder.pairs["USER_TOKEN"])
	require.Equal(t, "from-tool", recorder.pairs["EXTRA_TOKEN"])
}

// Guessing the skill from a path would let any successful command that merely
// mentions a skill directory write into that skill's credentials.
func TestShellExecDoesNotCaptureWithoutAnExplicitSkillName(t *testing.T) {
	recorder := &recordedCapture{}
	tool := NewShellExecTool(&fakeShellExecutor{}, nil).WithEnvCapture(recorder.capture).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"export USER_TOKEN=from-command; cd /opt/weknora/tenant/skills/pdf-tools && python x.py"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Zero(t, recorder.calls)
}

// A stored value belongs to whoever entered it. Capture may fill a blank, so a
// name that resolved is never handed to the write path.
func TestShellExecDoesNotCaptureAlreadyResolvedNames(t *testing.T) {
	recorder := &recordedCapture{}
	resolver := stubEnvResolver{resolved: map[string]string{"USER_TOKEN": "stored"}}
	tool := NewShellExecTool(&fakeShellExecutor{}, resolver).WithEnvCapture(recorder.capture).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"python x.py","skill_name":"pdf-tools","env":{"USER_TOKEN":"model-made-this-up","NEW_TOKEN":"fresh"}}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, recorder.calls)
	require.NotContains(t, recorder.pairs, "USER_TOKEN")
	require.Equal(t, "fresh", recorder.pairs["NEW_TOKEN"])
}

// Refusing a required variable the call itself carries would strand every user
// without a settings page, since the value can only arrive through the chat.
func TestShellExecRunsWhenTheCallSuppliesTheMissingRequiredValue(t *testing.T) {
	recorder := &recordedCapture{}
	resolver := stubEnvResolver{missing: []string{"USER_TOKEN"}}
	tool := NewShellExecTool(&fakeShellExecutor{}, resolver).WithEnvCapture(recorder.capture).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"python x.py","skill_name":"pdf-tools","env":{"USER_TOKEN":"typed-in-chat"}}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "typed-in-chat", recorder.pairs["USER_TOKEN"],
		"the value that made the run possible is the one worth storing")
}

func TestShellExecStillRefusesAMissingRequiredValueNobodySupplied(t *testing.T) {
	resolver := stubEnvResolver{missing: []string{"USER_TOKEN"}}
	tool := NewShellExecTool(&fakeShellExecutor{}, resolver).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"python x.py","skill_name":"pdf-tools"}`,
	))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "USER_TOKEN")
}

func TestShellExecDoesNotCaptureWhenCommandFails(t *testing.T) {
	recorder := &recordedCapture{}
	tool := NewShellExecTool(&fakeShellExecutor{result: &sandbox.ExecuteResult{ExitCode: 1}}, nil).
		WithEnvCapture(recorder.capture).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"export USER_TOKEN=x; false","skill_name":"pdf-tools"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success, "non-zero exit is still a successful tool call")
	require.Zero(t, recorder.calls)
}

func TestShellExecDoesNotCaptureWhenExecutorErrors(t *testing.T) {
	recorder := &recordedCapture{}
	tool := NewShellExecTool(&fakeShellExecutor{err: errors.New("sandbox down")}, nil).
		WithEnvCapture(recorder.capture).WithSkillEnvironment(shellTestSkillEnvironment(t))

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"export USER_TOKEN=x; true","skill_name":"pdf-tools"}`,
	))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, recorder.calls)
}

func TestInstallShellExecDoesNotCapture(t *testing.T) {
	recorder := &recordedCapture{}
	tool := NewInstallShellExecTool(&fakeInstallShellExecutor{}, installShellSkillDir).WithEnvCapture(recorder.capture)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"export USER_TOKEN=x; pip install x"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Zero(t, recorder.calls)
}

func TestShellExecOversizeCommandPointsAtWriteSandboxFile(t *testing.T) {
	command := strings.Repeat("a", shellExecMaxCommandBytes+1)
	raw, err := json.Marshal(map[string]string{"command": command})
	require.NoError(t, err)

	result, err := NewShellExecTool(&fakeShellExecutor{}, nil).Execute(
		shellExecTestContext(), raw,
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "command too long")
	assert.Contains(t, result.Error, "write_sandbox_file")
}

func TestShellExecHintsWhenTreeCommandIsMissing(t *testing.T) {
	tool := NewShellExecTool(&fakeShellExecutor{result: &sandbox.ExecuteResult{
		ExitCode: 127,
		Stderr:   "/bin/bash: line 1: tree: command not found\n",
	}}, nil)

	result, err := tool.Execute(
		shellExecTestContext(),
		json.RawMessage(`{"command":"tree -L 2 /workspace/output"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 127, result.Data["exit_code"])
	assert.Contains(t, result.Output, "not in the default sandbox image")
	assert.Contains(t, result.Output, "Use find/ls")
	assert.Contains(t, result.Output, "Do not apt-get install")
}

func TestInferredMissingCommandFromBashStderr(t *testing.T) {
	assert.Equal(t, "file", inferredMissingCommand(
		"file /tmp/x",
		"/bin/bash: line 1: file: command not found\n",
	))
	assert.Equal(t, "tree", inferredMissingCommand("tree -L 2", "tree: command not found"))
}

func TestShellExecHintsWhenSystemPythonMissesASkillModule(t *testing.T) {
	script := `cd /workspace/output && python3 -c "
from docx import Document
doc = Document('brief.docx')
print(len(doc.paragraphs))
"`
	tool := NewShellExecTool(&fakeShellExecutor{result: &sandbox.ExecuteResult{
		ExitCode: 1,
		Stderr:   "ModuleNotFoundError: No module named 'docx'\n",
	}}, nil)

	raw, err := json.Marshal(map[string]string{"command": script})
	require.NoError(t, err)
	result, err := tool.Execute(shellExecTestContext(), raw)
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Output, "Do not pip install")
	assert.Contains(t, result.Output, "write_sandbox_file")
	assert.Contains(t, result.Output, "shell_exec")
	assert.Contains(t, result.Output, ".venv/bin/python -c")
}

func TestSkillNameFromShellCommandExtractsImageSkill(t *testing.T) {
	assert.Equal(t, "meeting-and-brief", skillNameFromShellCommand(
		`/opt/weknora/tenant/skills/meeting-and-brief/.venv/bin/python -c "print(1)"`,
	))
	assert.Empty(t, skillNameFromShellCommand(`python3 -c "print(1)"`))
}

func TestShellExecAllowsOverlayInstallThatMentionsTheSkillTree(t *testing.T) {
	// Previously an up-front command blacklist rejected this recovery path
	// because the line contained both `pip install` and the skills root.
	executor := &fakeShellExecutor{result: &sandbox.ExecuteResult{ExitCode: 0}}
	tool := NewShellExecTool(executor, nil)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"python3 -m pip install --target /workspace/.skill-packages/foo -r /opt/weknora/tenant/skills/foo/requirements.txt"}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 1, executor.calls)
}

func TestInstallShellExecStillPipsIntoTheSkillTree(t *testing.T) {
	inner := &fakeInstallShellExecutor{}
	tool := NewInstallShellExecTool(inner, installShellSkillDir)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"pip install python-docx","work_dir":"/opt/weknora/tenant/skills/律师助手"}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, 1, inner.calls)
}

func TestShellExecHintsWhenVenvHasNoPip(t *testing.T) {
	tool := NewShellExecTool(&fakeShellExecutor{result: &sandbox.ExecuteResult{
		ExitCode: 1,
		Stderr:   "/opt/weknora/tenant/skills/律师助手/.venv/bin/python: No module named pip\n",
	}}, nil)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"/opt/weknora/tenant/skills/律师助手/.venv/bin/python /opt/weknora/tenant/skills/律师助手/scripts/install_deps.py --word --yes"}`,
	))
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Contains(t, result.Output, "frozen")
	assert.Contains(t, result.Output, "/workspace/.skill-packages/律师助手")
	assert.NotContains(t, result.Output, "write_sandbox_file")
}

func shellTestSkillEnvironment(t *testing.T) *skills.Manager {
	t.Helper()
	manager := skills.NewManager(&skills.ManagerConfig{Enabled: true}, nil)
	manager.WithTenantSource(skills.NewTenantSkillSource([]*types.TenantSkillEntity{
		{Name: "pdf-tools", Status: types.SkillStatusReady, Enabled: true},
	}, nil))
	require.NoError(t, manager.Initialize(context.Background()))
	return manager
}

func TestShellExecNeverSilentlyFallsBackFromNamedSkill(t *testing.T) {
	executor := &fakeShellExecutor{}
	result, err := NewShellExecTool(executor, nil).Execute(shellExecTestContext(), json.RawMessage(`{"command":"python3 report.py","skill_name":"missing"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, executor.calls)
	require.Contains(t, result.Error, "no skill environment")
}
