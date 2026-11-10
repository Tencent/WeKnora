package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxEditRejectsOverlappingOccurrences(t *testing.T) {
	for _, tc := range []struct {
		name, content, old string
		matches            string
	}{
		{"repeated character", "aaa", "aa", "2 times"},
		{"repeated pattern", "ababa", "aba", "2 times"},
		{"three overlapping matches", "aaaa", "aa", "3 times"},
		{"unicode", "哈哈哈", "哈哈", "2 times"},
		{"multiline", "a\na\na", "a\na", "2 times"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated, n, err := applySandboxEdit(tc.content, tc.old, "replacement", false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.matches)
			assert.Empty(t, updated)
			assert.Zero(t, n)
		})
	}
}

func TestSandboxEditReplaceAllRetainsNonOverlappingSemantics(t *testing.T) {
	for _, tc := range []struct {
		content, old, want string
		count              int
	}{
		{"aaa", "aa", "Xa", 1},
		{"aaaaa", "aa", "XXa", 2},
		{"ababa", "aba", "Xba", 1},
		{"哈哈哈", "哈哈", "X哈", 1},
	} {
		t.Run(tc.content, func(t *testing.T) {
			updated, n, err := applySandboxEdit(tc.content, tc.old, "X", true)
			require.NoError(t, err)
			assert.Equal(t, tc.want, updated)
			assert.Equal(t, tc.count, n)
		})
	}
}

func TestSandboxEditAmbiguousBatchDoesNotWrite(t *testing.T) {
	const filePath = "/workspace/output/example.txt"
	const original = "header\nababa\n"
	editor := &fakeSandboxFileEditor{files: map[string][]byte{filePath: []byte(original)}}
	args, err := json.Marshal(EditSandboxFileInput{Path: filePath, Edits: sandboxEditList{
		{OldString: "header", NewString: "updated"},
		{OldString: "aba", NewString: "X"},
	}})
	require.NoError(t, err)
	result, err := NewEditSandboxFileTool(editor).Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "edits[1]")
	assert.Contains(t, result.Error, "2 times")
	assert.Zero(t, editor.writes)
	assert.Equal(t, original, string(editor.files[filePath]))
}
