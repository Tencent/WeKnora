package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSandboxFileSink struct {
	path    string
	content []byte
	err     error
	calls   int
}

func (f *fakeSandboxFileSink) WriteSessionWorkspaceFile(_ context.Context, _, filePath string, content []byte) error {
	f.calls++
	f.path = filePath
	f.content = append([]byte(nil), content...)
	return f.err
}

func TestWriteSandboxFileWritesTextUnderOutput(t *testing.T) {
	script := "from pptx import Presentation\n" + strings.Repeat("# slide content for a long deck\n", 400)
	require.Greater(t, len(script), 8*1024)

	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/generate_ppt.py", script),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 1, sink.calls)
	assert.Equal(t, "/workspace/output/generate_ppt.py", sink.path)
	assert.Equal(t, script, string(sink.content))
	assert.Equal(t, "/workspace/output/generate_ppt.py", result.Data["path"])
	assert.Equal(t, len(script), result.Data["size"])
	assert.NotContains(t, result.Output, script)
	assert.Contains(t, result.Output, "execute_skill_script")
	assert.Contains(t, result.Output, "/workspace/output/generate_ppt.py")
	_, hasContent := result.Data["content"]
	assert.False(t, hasContent)
}

func TestWriteSandboxFileFlagsNestedPythonQuotes(t *testing.T) {
	script := "slides = [(\"这不是一个\"大干快上\"的夜晚\", False)]\n"
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/generate_fortune_ppt.py", script),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Equal(t, 1, sink.calls, "the file is still written so edit_sandbox_file can fix it")
	assert.Contains(t, result.Error, "line 1")
	assert.Contains(t, result.Error, "edit_sandbox_file")
	assert.Equal(t, true, result.Data["syntax_error"])
}

func TestWriteSandboxFileAllowsWorkspaceScratch(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/scratch.py", "print(1)\n"),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, sandbox.SessionWorkspaceRoot, result.Data["root"])
	assert.Equal(t, "/workspace/scratch.py", sink.path)
}

func TestWriteSandboxFileRefusesSessionInput(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	result, err := NewWriteSandboxFileTool(sink).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/input/secret.txt", "nope"),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Zero(t, sink.calls)
	assert.Contains(t, result.Error, "outside that scope")
	assert.Contains(t, result.Error, sandbox.SessionWorkspaceRoot)
}

func TestWriteSandboxFileRefusesDirectoryPaths(t *testing.T) {
	sink := &fakeSandboxFileSink{}
	for _, p := range []string{"/workspace", "/workspace/output", "/workspace/input"} {
		result, err := NewWriteSandboxFileTool(sink).Execute(
			sandboxFileTestContext(),
			mustWriteSandboxArgs(p, "nope"),
		)
		require.NoError(t, err, p)
		require.False(t, result.Success, p)
	}
	assert.Zero(t, sink.calls)
}

func TestWriteSandboxFileRefusesBinaryAndOversize(t *testing.T) {
	sink := &fakeSandboxFileSink{}

	binary, err := NewWriteSandboxFileTool(sink).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/x.bin", "pre\x00post"),
	)
	require.NoError(t, err)
	require.False(t, binary.Success)
	assert.Contains(t, binary.Error, "binary")

	oversize, err := NewWriteSandboxFileTool(sink).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("/workspace/output/big.py", strings.Repeat("a", maxWriteSandboxBytes+1)),
	)
	require.NoError(t, err)
	require.False(t, oversize.Success)
	assert.Contains(t, oversize.Error, "too large")
	assert.Zero(t, sink.calls)
}

func TestWriteSandboxFileRegistryHintsWhenPathMissing(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(NewWriteSandboxFileTool(&fakeSandboxFileSink{}))

	result, err := registry.ExecuteTool(
		sandboxFileTestContext(),
		ToolWriteSandboxFile,
		json.RawMessage(`{"content":"print(1)\n"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "path")
	assert.Contains(t, result.Error, "put `path` first")
}

// The sandbox and skill tools are registered from the capability itself
// (registerSandboxFileTools / registerSandboxShellIfAllowed /
// initializeSkillsManager), never from the agent's tool allowlist. Offering
// them as checkboxes told the operator they could withhold a tool that would
// be registered anyway.
func TestSandboxCapabilityToolsAreNotToolListCheckboxes(t *testing.T) {
	for _, name := range []string{
		ToolListSandboxFiles, ToolReadSandboxFile,
		ToolWriteSandboxFile, ToolEditSandboxFile, ToolShellExec,
		ToolReadSkill, ToolExecuteSkillScript,
	} {
		require.NotContains(t, DefaultAllowedTools(), name)
		for _, definition := range AvailableToolDefinitions() {
			require.NotEqual(t, name, definition.Name,
				"%s follows the sandbox switch, so a tool checkbox for it would do nothing", name)
		}
	}
}

func mustWriteSandboxArgs(path, content string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		panic(err)
	}
	return raw
}
