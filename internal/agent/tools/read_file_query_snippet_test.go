package tools

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestReadFileQueryShowsMatchInLongLine(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, query, match string
		regex                      bool
	}{
		{name: "literal ASCII", prefix: strings.Repeat("x", 500), query: "needle", match: "NEEDLE"},
		{name: "Unicode prefix", prefix: strings.Repeat("章节", 300), query: "needle", match: "NEEDLE"},
		{name: "regex", prefix: strings.Repeat("x", 500), query: `ERR-[0-9]+`, match: "ERR-123", regex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := NewReadFileTool(nil).WithArtifacts(queryLimitFileSource(tc.prefix + tc.match + " tail\n"))
			args, err := json.Marshal(ReadFileInput{Path: "resource://example", Query: tc.query, Regex: tc.regex})
			require.NoError(t, err)
			result, err := reader.Execute(t.Context(), args)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			require.Equal(t, 1, result.Data["match_count"])
			require.Contains(t, result.Output, tc.match)
		})
	}
}

func TestReadFileMatchSnippetBoundsAndShortLines(t *testing.T) {
	matcher := regexp.MustCompile(`(?i)needle`)
	for _, tc := range []struct {
		name, line string
	}{
		{name: "short", line: "a NEEDLE b"},
		{name: "start", line: "NEEDLE" + strings.Repeat("a", 500)},
		{name: "middle", line: strings.Repeat("章", 500) + "NEEDLE" + strings.Repeat("节", 500)},
		{name: "end", line: strings.Repeat("章", 500) + "NEEDLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readFileMatchSnippet(tc.line, []*regexp.Regexp{matcher})
			require.True(t, utf8.ValidString(got))
			require.Contains(t, got, "NEEDLE")
			require.LessOrEqual(t, utf8.RuneCountInString(got), readFileMatchLineRunes+2)
			if tc.name == "short" {
				require.Equal(t, tc.line, got)
			}
		})
	}
}
