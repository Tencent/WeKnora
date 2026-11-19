package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type queryLimitFileSource string

func (s queryLimitFileSource) Read(context.Context, string) ([]byte, error) {
	return []byte(s), nil
}

func TestReadFileQueryHonorsLimitAndContinuesAtNextMatch(t *testing.T) {
	for _, regex := range []bool{false, true} {
		t.Run(fmt.Sprintf("regex=%t", regex), func(t *testing.T) {
			reader := NewReadFileTool(nil).WithArtifacts(queryLimitFileSource(
				"needle one\nother\nneedle two\nother\nneedle three\n"))
			registry := NewToolRegistry()
			registry.RegisterTool(reader)
			input := ReadFileInput{Path: "resource://query-limit", Query: "needle", Regex: regex, Limit: 1}
			for i, line := range []int{1, 3, 5} {
				args, err := json.Marshal(input)
				require.NoError(t, err)
				result, err := registry.ExecuteTool(t.Context(), ToolReadFile, args)
				require.NoError(t, err)
				require.True(t, result.Success, result.Error)
				require.Equal(t, 1, result.Data["match_count"])
				require.Contains(t, result.Output, fmt.Sprintf("%d: needle", line))
				if i < 2 {
					require.Equal(t, true, result.Data["truncated"])
					require.Equal(t, line+2, result.Data["next_offset"])
					require.NotContains(t, result.Output, fmt.Sprintf("%d: needle", line+2))
					input.Offset = result.Data["next_offset"].(int)
				} else {
					require.Equal(t, false, result.Data["truncated"])
					require.NotContains(t, result.Data, "next_offset")
				}
			}
		})
	}
}

func TestReadFileQueryLimitRetainsDefaultAndHardCap(t *testing.T) {
	reader := NewReadFileTool(nil).WithArtifacts(queryLimitFileSource(strings.Repeat("needle\n", 60)))
	for _, tc := range []struct {
		limit int
		want  int
	}{
		{limit: 0, want: readFileMaxMatches},
		{limit: 2, want: 2},
		{limit: readFileMaxMatches, want: readFileMaxMatches},
		{limit: 100, want: readFileMaxMatches},
	} {
		t.Run(fmt.Sprintf("limit=%d", tc.limit), func(t *testing.T) {
			args, err := json.Marshal(ReadFileInput{
				Path: "resource://query-limit", Query: "needle", Limit: tc.limit,
			})
			require.NoError(t, err)
			result, err := reader.Execute(t.Context(), args)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			require.Equal(t, tc.want, result.Data["match_count"])
			require.Equal(t, tc.want+1, result.Data["next_offset"])
		})
	}
}
