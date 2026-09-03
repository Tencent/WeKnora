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

// applySandboxEdit exercises a one-entry batch, which is what most of these
// cases are about.
func applySandboxEdit(content, oldString, newString string, replaceAll bool) (string, int, error) {
	return applySandboxEdits(content, []SandboxEdit{
		{OldString: oldString, NewString: newString, ReplaceAll: replaceAll},
	})
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

// Every edit resolves against the original content, so a batch is
// order-independent: an entry cannot be shifted or swallowed by one that
// happens to be applied before it.
func TestApplySandboxEditsResolvesAgainstOriginal(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"

	updated, n, err := applySandboxEdits(content, []SandboxEdit{
		{OldString: "gamma", NewString: "GAMMA"},
		{OldString: "alpha", NewString: "ALPHA"},
	})

	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "ALPHA\nbeta\nGAMMA\n", updated)
}

// Two edits claiming the same bytes would corrupt the file silently, so the
// batch is refused with the indices that collide.
func TestApplySandboxEditsRejectsOverlap(t *testing.T) {
	content := "the quick brown fox\n"

	_, _, err := applySandboxEdits(content, []SandboxEdit{
		{OldString: "quick brown", NewString: "slow"},
		{OldString: "brown fox", NewString: "grey wolf"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlapping")
	assert.Contains(t, err.Error(), "edits[0]")
	assert.Contains(t, err.Error(), "edits[1]")
}

// A failed entry must not leave the file half-edited, and the error has to say
// which entry to fix.
func TestApplySandboxEditsFailsWholeBatchWithIndex(t *testing.T) {
	content := "alpha\nbeta\n"

	_, _, err := applySandboxEdits(content, []SandboxEdit{
		{OldString: "alpha", NewString: "ALPHA"},
		{OldString: "nowhere", NewString: "x"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "edits[1]")
	assert.Contains(t, err.Error(), "not found")
}

// replace_all lives on the entry, so one batch can mix a global rename with
// edits that must still be unique.
func TestApplySandboxEditsMixesReplaceAllWithUniqueEntries(t *testing.T) {
	content := "tmp = 1\nprint(tmp)\nuse(tmp)\nNAME = 'x'\n"

	updated, n, err := applySandboxEdits(content, []SandboxEdit{
		{OldString: "tmp", NewString: "total", ReplaceAll: true},
		{OldString: "NAME = 'x'", NewString: "NAME = 'y'"},
	})

	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "total = 1\nprint(total)\nuse(total)\nNAME = 'y'\n", updated)

	// An entry that matches several times without replace_all is still refused,
	// and the error points at the entry to fix.
	_, _, err = applySandboxEdits(content, []SandboxEdit{
		{OldString: "NAME = 'x'", NewString: "NAME = 'y'"},
		{OldString: "tmp", NewString: "total"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "edits[1]")
	assert.Contains(t, err.Error(), "replace_all")
}

// A replace_all expansion has to take part in overlap detection like any other
// span, or it could quietly consume text another entry is claiming.
func TestApplySandboxEditsDetectsOverlapAgainstReplaceAll(t *testing.T) {
	_, _, err := applySandboxEdits("foo bar\nfoo baz\n", []SandboxEdit{
		{OldString: "foo", NewString: "qux", ReplaceAll: true},
		{OldString: "foo baz", NewString: "nope"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlapping")
}

func TestApplySandboxEditsRequiresAtLeastOneEntry(t *testing.T) {
	_, _, err := applySandboxEdits("alpha\n", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "edits is required")
}

// Models emit array parameters as a bare object or as a JSON string often
// enough that rejecting those spends a round on a formatting slip the model
// cannot see.
func TestSandboxEditListAcceptsModelShapes(t *testing.T) {
	var asArray EditSandboxFileInput
	require.NoError(t, json.Unmarshal([]byte(
		`{"path":"/workspace/a.py","edits":[{"old_string":"a","new_string":"b"}]}`), &asArray))
	assert.Len(t, asArray.Edits, 1)

	var asObject EditSandboxFileInput
	require.NoError(t, json.Unmarshal([]byte(
		`{"path":"/workspace/a.py","edits":{"old_string":"a","new_string":"b"}}`), &asObject))
	require.Len(t, asObject.Edits, 1)
	assert.Equal(t, "a", asObject.Edits[0].OldString)

	var asString EditSandboxFileInput
	require.NoError(t, json.Unmarshal([]byte(
		`{"path":"/workspace/a.py","edits":"[{\"old_string\":\"a\",\"new_string\":\"b\"}]"}`), &asString))
	require.Len(t, asString.Edits, 1)
	assert.Equal(t, "b", asString.Edits[0].NewString)
}

// End to end: one call, several changes, one write.
func TestEditSandboxFileAppliesBatchInOneWrite(t *testing.T) {
	editor := &fakeSandboxFileEditor{
		files: map[string][]byte{
			"/workspace/run.py": []byte("SRC = '/tmp/in'\nDST = '/tmp/out'\nDEBUG = True\n"),
		},
	}

	result, err := NewEditSandboxFileTool(editor).Execute(sandboxFileTestContext(), json.RawMessage(
		`{"path":"/workspace/run.py","edits":[`+
			`{"old_string":"'/tmp/in'","new_string":"'/workspace/input'"},`+
			`{"old_string":"DEBUG = True","new_string":"DEBUG = False"}]}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 2, result.Data["replacements"])
	assert.Equal(t, 1, editor.writes)

	final := string(editor.files["/workspace/run.py"])
	assert.Contains(t, final, "SRC = '/workspace/input'")
	assert.Contains(t, final, "DEBUG = False")
	assert.Contains(t, final, "DST = '/tmp/out'")
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
	assert.Equal(t, 1, result.Data["added_lines"])
	assert.Equal(t, 1, result.Data["removed_lines"])
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
	edit := map[string]any{"old_string": oldString, "new_string": newString}
	if replaceAll {
		edit["replace_all"] = true
	}
	raw, err := json.Marshal(map[string]any{
		"path":  path,
		"edits": []any{edit},
	})
	if err != nil {
		panic(err)
	}
	return raw
}
