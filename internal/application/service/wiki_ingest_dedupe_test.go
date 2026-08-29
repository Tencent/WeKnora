package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A hub page touched by many documents accumulates the same paragraph across
// successive modify rounds. Headings and short blocks legitimately repeat and
// must survive; only long prose duplicates collapse.
func TestDedupeRepeatedBlocksCollapsesRepeatedProse(t *testing.T) {
	longBlock := strings.Repeat("这是需要确定性去重的完整长段落。", 5)
	input := strings.Join([]string{
		"# 重复标题",
		longBlock,
		"无",
		"# 重复标题",
		longBlock,
		"无",
		longBlock,
	}, "\n\n")

	got, removed := dedupeRepeatedBlocks(input)

	require.Equal(t, 2, removed)
	require.Equal(t, 1, strings.Count(got, longBlock),
		"repeated prose block must be collapsed to a single copy")
	require.Equal(t, 2, strings.Count(got, "# 重复标题"),
		"heading blocks must not be deduped")
	require.Equal(t, 2, strings.Count(got, "\n\n无"),
		"blocks below the length threshold must not be deduped")
}

// Matching is on whitespace-normalized text: the model frequently re-emits the
// same paragraph with different wrapping or indentation, and that is still a
// duplicate.
func TestDedupeRepeatedBlocksIgnoresWhitespaceDifferences(t *testing.T) {
	longBlock := strings.Repeat("Deterministic dedupe keeps one copy of this sentence. ", 3)
	rewrapped := "  " + strings.Join(strings.Fields(longBlock), "   ") + "  "
	input := strings.Join([]string{longBlock, rewrapped}, "\n\n")

	got, removed := dedupeRepeatedBlocks(input)

	require.Equal(t, 1, removed)
	require.NotContains(t, got, rewrapped)
}

// The overwhelming majority of pages have no duplicates. On those the function
// must not touch a single byte — no newline-style normalization, no trailing
// whitespace stripping — so it can never churn content on its own.
func TestDedupeRepeatedBlocksNoopIsByteExact(t *testing.T) {
	input := "# 标题\r\n\r\n第一段没有重复。  \r\n\r\n- 无\r\n\r\n结尾\r\n"

	got, removed := dedupeRepeatedBlocks(input)

	require.Zero(t, removed)
	require.Equal(t, []byte(input), []byte(got))
}

// Content with no blank-line separator is a single block and cannot contain a
// repeat by this definition.
func TestDedupeRepeatedBlocksSingleBlockIsUntouched(t *testing.T) {
	input := strings.Repeat("one long unbroken paragraph. ", 10)

	got, removed := dedupeRepeatedBlocks(input)

	require.Zero(t, removed)
	require.Equal(t, input, got)
}

// Repeats shorter than the threshold are usually real structure (a rule, a
// repeated table header, a short list item under two sections). Dropping them
// would corrupt the page.
func TestDedupeRepeatedBlocksKeepsShortRepeats(t *testing.T) {
	input := strings.Join([]string{"---", "Alpha section.", "---", "Beta section.", "---"}, "\n\n")

	got, removed := dedupeRepeatedBlocks(input)

	require.Zero(t, removed)
	require.Equal(t, input, got)
}

func TestIsMarkdownHeadingBlock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		want  bool
	}{
		{name: "h1", block: "# Title", want: true},
		{name: "h6", block: "###### Title", want: true},
		{name: "tab after hashes", block: "#\tTitle", want: true},
		{name: "surrounding whitespace", block: "  ## Title  ", want: true},
		{name: "seven hashes is not a heading", block: "####### Title", want: false},
		{name: "no space after hashes", block: "#Title", want: false},
		{name: "hashes only", block: "###", want: false},
		{name: "multiline is not a bare heading", block: "# Title\nbody", want: false},
		{name: "plain prose", block: "Not a heading", want: false},
		{name: "empty", block: "   ", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isMarkdownHeadingBlock(tc.block))
		})
	}
}
