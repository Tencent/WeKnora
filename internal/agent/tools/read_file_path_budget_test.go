package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestReadFileLongPathPagesDoNotLoseContent(t *testing.T) {
	for _, dir := range []string{"short/", strings.Repeat("nested/", 100), strings.Repeat("目录/", 220)} {
		t.Run(fmt.Sprintf("path-runes-%d", utf8.RuneCountInString(dir)), func(t *testing.T) {
			filePath := "/workspace/" + dir + "file.txt"
			var content strings.Builder
			for i := 0; i < 100; i++ {
				fmt.Fprintf(&content, "line %03d %s\n", i, strings.Repeat("x", 80))
			}
			source := &fakeSandboxFileSource{
				stat: &sandbox.RemoteStatEntry{
					Path: filePath, Type: sandbox.RemoteEntryFile, Size: int64(content.Len()),
				},
				data: []byte(content.String()),
			}
			registry := NewToolRegistry()
			registry.SetMaxToolOutputSize(2048)
			registry.RegisterTool(NewReadFileTool(source))
			offset := 1
			var restored strings.Builder
			for calls := 0; ; calls++ {
				require.Less(t, calls, 100, "continuation must make progress")
				args, err := json.Marshal(ReadFileInput{Path: filePath, Offset: offset})
				require.NoError(t, err)
				result, err := registry.ExecuteTool(sandboxFileTestContext(), ToolReadFile, args)
				require.NoError(t, err)
				require.True(t, result.Success, result.Error)
				require.NotContains(t, result.Output, "output truncated")
				require.LessOrEqual(t, utf8.RuneCountInString(result.Output), 2048)
				_, text, ok := strings.Cut(result.Output, "```\n")
				require.True(t, ok)
				text, _, ok = strings.Cut(text, "```\n")
				require.True(t, ok)
				require.Equal(t, len(text), result.Data["returned_bytes"])
				restored.WriteString(text)
				next, more := result.Data["next_offset"].(int)
				if !more {
					break
				}
				require.Greater(t, next, offset)
				offset = next
			}
			require.Equal(t, content.String(), restored.String())
		})
	}
}

func TestReadFileRejectsBudgetTooSmallForPath(t *testing.T) {
	filePath := "/workspace/" + strings.Repeat("nested/", 100) + "file.txt"
	result := renderFilePage(WithOutputBudget(t.Context(), 512), ReadFileInput{},
		[]byte("hello\n"), "session-1", filePath, "/workspace")
	require.False(t, result.Success)
	require.Contains(t, result.Error, "output budget")
	require.Empty(t, result.Output)
	require.NotContains(t, result.Data, "next_offset")
}
