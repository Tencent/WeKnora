package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestReadFileQueryHonorsByteBudgetAcrossPages(t *testing.T) {
	for _, scheme := range []string{"resource://", "web://"} {
		for _, regex := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/regex=%t", scheme, regex), func(t *testing.T) {
				ref := scheme + "query-budget"
				source := &memoryWebPages{pages: map[string]string{ref: "hit界\nother\nhit界\nhit界\n"}}
				reader := NewReadFileTool(nil).WithArtifacts(source).WithWebPages(source)
				registry := NewToolRegistry()
				registry.RegisterTool(reader)
				input := ReadFileInput{Path: ref, Query: "hit", Regex: regex, MaxBytes: 10}
				// Each numbered match is exactly 10 UTF-8 bytes, but only 8 runes.
				for _, line := range []int{1, 3, 4} {
					args, err := json.Marshal(input)
					require.NoError(t, err)
					result, err := registry.ExecuteTool(t.Context(), ToolReadFile, args)
					require.NoError(t, err)
					require.True(t, result.Success, result.Error)
					_, body, ok := strings.Cut(result.Output, "```\n")
					require.True(t, ok)
					body, _, ok = strings.Cut(body, "```\n")
					require.True(t, ok)
					require.Equal(t, fmt.Sprintf("%d: hit界\n", line), body)
					require.Equal(t, 1, result.Data["match_count"])
					require.True(t, utf8.ValidString(body))
					if line < 4 {
						wantNext := 4
						if line == 1 {
							wantNext = 3
						}
						require.Equal(t, wantNext, result.Data["next_offset"])
						input.Offset = wantNext
					} else {
						require.Equal(t, false, result.Data["truncated"])
						require.NotContains(t, result.Data, "next_offset")
					}
				}
			})
		}
	}
}

func TestReadFileQueryRejectsUnreturnableFirstMatch(t *testing.T) {
	reader := NewReadFileTool(nil).WithArtifacts(queryLimitFileSource("hit界\n"))
	result, err := reader.Execute(t.Context(), json.RawMessage(
		`{"path":"resource://small-budget","query":"hit","max_bytes":9}`))
	require.NoError(t, err)
	require.False(t, result.Success, "a matching entry larger than max_bytes must not bypass the budget")
	require.Contains(t, result.Error, "max_bytes")
	require.NotContains(t, result.Output, "No line matches")
}

func TestReadFileQueryRejectsUnreturnableRuneBudget(t *testing.T) {
	reader := NewReadFileTool(nil).WithArtifacts(queryLimitFileSource("hit " + strings.Repeat("x", 500)))
	for _, budget := range []int{100, 600} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			result, err := reader.Execute(WithOutputBudget(t.Context(), budget), json.RawMessage(
				`{"path":"resource://small-budget","query":"hit"}`))
			require.NoError(t, err)
			require.False(t, result.Success, "the first match must not bypass the output budget")
			require.Contains(t, result.Error, "output budget")
		})
	}
}

func TestReadFileQueryRetainsSourceByteCaps(t *testing.T) {
	for _, tc := range []struct {
		scheme string
		cap    int
		want   int
	}{
		{scheme: "resource://", cap: 65536, want: 41},
		{scheme: "web://", cap: 51200, want: 32},
	} {
		t.Run(tc.scheme, func(t *testing.T) {
			ref := tc.scheme + "large-budget"
			line := "hit" + strings.Repeat("😀", 397)
			source := &memoryWebPages{pages: map[string]string{ref: strings.Repeat(line+"\n", 50)}}
			reader := NewReadFileTool(nil).WithArtifacts(source).WithWebPages(source)
			args, err := json.Marshal(ReadFileInput{Path: ref, Query: "hit", MaxBytes: 1024 * 1024})
			require.NoError(t, err)
			result, err := reader.Execute(WithOutputBudget(t.Context(), 200000), args)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			_, body, ok := strings.Cut(result.Output, "```\n")
			require.True(t, ok)
			body, _, ok = strings.Cut(body, "```\n")
			require.True(t, ok)
			require.LessOrEqual(t, len(body), tc.cap)
			require.Equal(t, tc.want, result.Data["match_count"])
			require.Equal(t, tc.want+1, result.Data["next_offset"])
			require.True(t, utf8.ValidString(body))
		})
	}
}
