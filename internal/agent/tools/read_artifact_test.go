package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadArtifactPagesLongLinesWithoutSandboxOrContentLoss(t *testing.T) {
	ref := "resource://" + strings.Repeat("a", 22)
	body := strings.Repeat("章节正文", 4000)
	source := &memoryWebPages{pages: map[string]string{ref: body}}
	reader := NewReadFileTool(nil).WithArtifacts(source)
	input := ReadFileInput{Path: ref, MaxBytes: 2048}
	var previous int
	var collected strings.Builder
	for attempts := 0; ; attempts++ {
		require.Less(t, attempts, 100)
		args, err := json.Marshal(input)
		require.NoError(t, err)
		result, err := reader.Execute(t.Context(), args)
		require.NoError(t, err)
		require.True(t, result.Success, result.Error)
		_, text, ok := strings.Cut(result.Output, "\n```\n")
		require.True(t, ok)
		text, _, ok = strings.Cut(text, "\n```\n")
		require.True(t, ok)
		collected.WriteString(text)
		if result.Data["truncated"] == false {
			break
		}
		input.Offset = result.Data["next_offset"].(int)
		input.LineOffset = result.Data["next_line_offset"].(int)
		require.Greater(t, input.LineOffset, previous)
		previous = input.LineOffset
	}
	require.Positive(t, previous)
	require.Equal(t, body, collected.String())
	input.LineOffset, input.Offset, input.Query = 0, 0, "章节"
	args, err := json.Marshal(input)
	require.NoError(t, err)
	found, err := reader.Execute(t.Context(), args)
	require.NoError(t, err)
	require.True(t, found.Success, found.Error)
	missing, err := NewReadFileTool(nil).Execute(t.Context(), args)
	require.NoError(t, err)
	require.False(t, missing.Success)
	require.Contains(t, missing.Error, "generated file access")
}
