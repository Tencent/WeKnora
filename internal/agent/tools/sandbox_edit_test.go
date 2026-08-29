package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSandboxFileEditor struct {
	files    map[string][]byte
	statErr  error
	readErr  error
	writeErr error
	writes   int
}

func (f *fakeSandboxFileEditor) StatSessionFile(_ context.Context, _, filePath string) (*sandbox.RemoteStatEntry, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	data, ok := f.files[filePath]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", filePath)
	}
	return &sandbox.RemoteStatEntry{
		Path: filePath,
		Type: sandbox.RemoteEntryFile,
		Size: int64(len(data)),
	}, nil
}

func (f *fakeSandboxFileEditor) ReadSessionFile(_ context.Context, _, filePath string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	data, ok := f.files[filePath]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", filePath)
	}
	return append([]byte(nil), data...), nil
}

func (f *fakeSandboxFileEditor) WriteSessionWorkspaceFile(_ context.Context, _, filePath string, content []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes++
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[filePath] = append([]byte(nil), content...)
	return nil
}

func TestApplySandboxEditUniqueAndReplaceAll(t *testing.T) {
	content := "a = '/home/user/Desktop/deck.pptx'\nprint(a)\n"

	updated, n, err := applySandboxEdit(
		content,
		"/home/user/Desktop/deck.pptx",
		"/workspace/output/deck.pptx",
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Contains(t, updated, "/workspace/output/deck.pptx")
	assert.NotContains(t, updated, "/home/user/Desktop")

	_, _, err = applySandboxEdit("xx xx xx", "xx", "yy", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3 times")

	all, n, err := applySandboxEdit("xx xx xx", "xx", "yy", true)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, "yy yy yy", all)

	_, _, err = applySandboxEdit(content, "missing", "x", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	_, _, err = applySandboxEdit(content, "print(a)", "print(a)", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identical")

	_, _, err = applySandboxEdit(content, "", "x", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "old_string is required")
}

func TestEditSandboxFileReplacesUniqueSnippet(t *testing.T) {
	path := "/workspace/output/generate_wifi_ppt.py"
	original := "from pptx import Presentation\n" +
		"out = '/home/user/Desktop/Windows_Server_2008_WiFi连接指南.pptx'\n" +
		"prs.save(out)\n"
	editor := &fakeSandboxFileEditor{files: map[string][]byte{path: []byte(original)}}

	result, err := NewEditSandboxFileTool(editor).Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs(path,
			"/home/user/Desktop/Windows_Server_2008_WiFi连接指南.pptx",
			"/workspace/output/Windows_Server_2008_WiFi连接指南.pptx",
			false,
		),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 1, editor.writes)
	assert.Equal(t, 1, result.Data["replacements"])
	assert.Equal(t, path, result.Data["path"])
	assert.Contains(t, string(editor.files[path]), "/workspace/output/Windows_Server_2008_WiFi连接指南.pptx")
	assert.NotContains(t, string(editor.files[path]), "/home/user/Desktop")
	assert.NotContains(t, result.Output, original)
	_, hasContent := result.Data["content"]
	assert.False(t, hasContent)
}

func TestEditSandboxFileFlagsNestedPythonQuotes(t *testing.T) {
	path := "/workspace/output/generate_fortune_ppt.py"
	original := "slides = [(\"placeholder\", False)]\n"
	editor := &fakeSandboxFileEditor{files: map[string][]byte{path: []byte(original)}}

	result, err := NewEditSandboxFileTool(editor).Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs(path, "placeholder", "这不是一个\"大干快上\"的夜晚", false),
	)

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Equal(t, 1, editor.writes)
	assert.Contains(t, result.Error, "edit_sandbox_file")
	assert.Equal(t, true, result.Data["syntax_error"])
}

func TestEditSandboxFileReplaceAllAndAmbiguous(t *testing.T) {
	path := "/workspace/scratch.py"
	original := "print('todo')\nprint('todo')\n"
	editor := &fakeSandboxFileEditor{files: map[string][]byte{path: []byte(original)}}
	tool := NewEditSandboxFileTool(editor)

	ambiguous, err := tool.Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs(path, "todo", "done", false),
	)
	require.NoError(t, err)
	require.False(t, ambiguous.Success)
	assert.Contains(t, ambiguous.Error, "2 times")
	assert.Zero(t, editor.writes)

	all, err := tool.Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs(path, "todo", "done", true),
	)
	require.NoError(t, err)
	require.True(t, all.Success, all.Error)
	assert.Equal(t, 2, all.Data["replacements"])
	assert.Equal(t, "print('done')\nprint('done')\n", string(editor.files[path]))
}

func TestEditSandboxFileRefusesInputAndMissing(t *testing.T) {
	editor := &fakeSandboxFileEditor{files: map[string][]byte{
		"/workspace/input/secret.txt": []byte("nope"),
	}}
	tool := NewEditSandboxFileTool(editor)

	input, err := tool.Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs("/workspace/input/secret.txt", "nope", "ok", false),
	)
	require.NoError(t, err)
	require.False(t, input.Success)
	assert.Contains(t, input.Error, "outside that scope")
	assert.Zero(t, editor.writes)

	missing, err := tool.Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs("/workspace/output/missing.py", "a", "b", false),
	)
	require.NoError(t, err)
	require.False(t, missing.Success)
	assert.Contains(t, missing.Error, "failed to stat")
}

func TestEditSandboxFileRefusesBinaryAndOversize(t *testing.T) {
	tool := NewEditSandboxFileTool(&fakeSandboxFileEditor{files: map[string][]byte{
		"/workspace/output/x.bin":  []byte("pre\x00post"),
		"/workspace/output/big.py": []byte(strings.Repeat("a", maxWriteSandboxBytes+1)),
	}})

	binary, err := tool.Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs("/workspace/output/x.bin", "pre", "POST", false),
	)
	require.NoError(t, err)
	require.False(t, binary.Success)
	assert.Contains(t, binary.Error, "binary")

	oversize, err := tool.Execute(
		sandboxFileTestContext(),
		mustEditSandboxArgs("/workspace/output/big.py", "a", "b", true),
	)
	require.NoError(t, err)
	require.False(t, oversize.Success)
	assert.Contains(t, oversize.Error, "too large")
}

func TestEditSandboxFileRegistryHintsWhenPathMissing(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(NewEditSandboxFileTool(&fakeSandboxFileEditor{
		files: map[string][]byte{"/workspace/a.py": []byte("x")},
	}))

	result, err := registry.ExecuteTool(
		sandboxFileTestContext(),
		ToolEditSandboxFile,
		json.RawMessage(`{"old_string":"x","new_string":"y"}`),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "path")
	assert.Contains(t, result.Error, "put `path` first")
}

func mustEditSandboxArgs(path, oldString, newString string, replaceAll bool) json.RawMessage {
	payload := map[string]any{
		"path":       path,
		"old_string": oldString,
		"new_string": newString,
	}
	if replaceAll {
		payload["replace_all"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return raw
}
