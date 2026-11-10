package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rangeText returns deterministic text of exactly n runes so fixtures satisfy
// the splitter invariant runeLen(Content) == EndAt-StartAt.
func rangeText(seed rune, n int) string {
	return strings.Repeat(string(seed), n)
}

// trustedResult builds a SearchResult whose coordinates are trustworthy
// (unedited, valid range, splitter-consistent length).
func trustedResult(id string, index, start, end int, content string) *types.SearchResult {
	return &types.SearchResult{
		ID: id, ChunkIndex: index, StartAt: start, EndAt: end, Content: content, Score: 1.0,
	}
}

// mergeGrouped runs the merge stage as the pipeline does: group by knowledge
// and chunk type, then merge sequential bodies. Observable behavior only.
func mergeGrouped(chunks []*types.SearchResult) []*types.SearchResult {
	return (&PluginMerge{}).groupAndMergeCurrentContent(context.Background(), chunks)
}

// All fixtures below share KnowledgeID "doc", ChunkType "text" and Score 1.0,
// so results form a single group and the deterministic post-merge sort keeps
// their relative order by ChunkIndex.
func docChunks(chunks ...*types.SearchResult) []*types.SearchResult {
	for _, c := range chunks {
		c.KnowledgeID = "doc"
		c.ChunkType = "text"
	}
	return chunks
}

func TestGroupAndMerge_TrustedOverlapTrimsAndExtendsRange(t *testing.T) {
	// 20-rune real overlap between two trusted chunks: the merged body must
	// contain the overlap exactly once and extend the result range.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 80)+rangeText('b', 20))
	second := trustedResult("c2", 2, 80, 200, rangeText('b', 20)+rangeText('c', 100))

	results := mergeGrouped(docChunks(first, second))

	require.Len(t, results, 1)
	assert.Equal(t, rangeText('a', 80)+rangeText('b', 20)+rangeText('c', 100), results[0].Content)
	assert.Equal(t, 200, results[0].EndAt, "merged range must extend to the second chunk")
	assert.ElementsMatch(t, []string{"c2"}, results[0].SubChunkID)
}

func TestGroupAndMerge_TrustedOverlapBeyondTextSearchWindow(t *testing.T) {
	// A real overlap wider than the text matcher's 400-rune window: the position
	// path knows the exact amount, so the overlap must not survive twice.
	overlap := rangeText('o', 500)
	first := trustedResult("c1", 1, 0, 1000, rangeText('a', 500)+overlap)
	second := trustedResult("c2", 2, 500, 1500, overlap+rangeText('b', 500))

	results := mergeGrouped(docChunks(first, second))

	require.Len(t, results, 1)
	assert.Equal(t, rangeText('a', 500)+overlap+rangeText('b', 500), results[0].Content)
	assert.Equal(t, 1500, results[0].EndAt)
}

func TestGroupAndMerge_TrustedAdjacentRepeatedTextKeepsEveryRow(t *testing.T) {
	// Adjacent trusted chunks (positional overlap 0) made of one repeating row:
	// searching for the longest suffix match would mistake the repetition for an
	// overlap and drop rows, so the exact-overlap path must concatenate verbatim.
	row := "| cell | cell |\n"
	firstBody := rangeText('x', 100) + strings.Repeat(row, 2)
	secondBody := strings.Repeat(row, 3) + rangeText('y', 60)
	first := trustedResult("c1", 1, 0, runeLen(firstBody), firstBody)
	second := trustedResult("c2", 2, runeLen(firstBody), runeLen(firstBody)+runeLen(secondBody), secondBody)

	results := mergeGrouped(docChunks(first, second))

	require.Len(t, results, 1)
	assert.Equal(t, firstBody+secondBody, results[0].Content)
	assert.Equal(t, 5, strings.Count(results[0].Content, row), "no repeated row may be dropped")
}

func TestGroupAndMerge_TrustedOverlapTextMismatchFallsBackToTextMatch(t *testing.T) {
	// Length invariant holds but the bodies disagree inside the overlap window
	// (HTML entities, synthetic headers): the exact path must refuse and the text
	// fallback must keep both bodies rather than trim on coordinates.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 80)+rangeText('b', 20))
	second := trustedResult("c2", 2, 80, 200, rangeText('c', 20)+rangeText('d', 100))

	results := mergeGrouped(docChunks(first, second))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, rangeText('b', 20))
	assert.Contains(t, results[0].Content, rangeText('c', 20))
	assert.Contains(t, results[0].Content, rangeText('d', 100))
}

func TestGroupAndMerge_TrustedAdjacentJoinsSeamlessly(t *testing.T) {
	// Adjacent trusted chunks are contiguous in the original document: the
	// merged body must be the exact concatenation, with no separator inserted.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	second := trustedResult("c2", 2, 100, 200, rangeText('b', 100))

	results := mergeGrouped(docChunks(first, second))

	require.Len(t, results, 1)
	assert.Equal(t, rangeText('a', 100)+rangeText('b', 100), results[0].Content)
	assert.Equal(t, 200, results[0].EndAt)
}

func TestGroupAndMerge_TrustedGapStartsNewGroup(t *testing.T) {
	// A real position gap means content in between is missing: chunks must
	// stay separate instead of being joined with a fabricated separator.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	second := trustedResult("c2", 2, 150, 250, rangeText('b', 100))

	results := mergeGrouped(docChunks(first, second))

	require.Len(t, results, 2, "position gap must not merge")
}

func TestGroupAndMerge_RewrittenResultDoesNotTrustCoordinates(t *testing.T) {
	// A result whose content was replaced by the pipeline (parent or neighbor
	// expansion) must never enter the position path, even when the replacement
	// happens to satisfy the length invariant and share boundary text: its
	// stale coordinates must not extend the merged group's range.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	rewritten := trustedResult("c2", 2, 80, 200, rangeText('a', 20)+rangeText('b', 100))
	rewritten.ContentRewritten = true

	results := mergeGrouped(docChunks(first, rewritten))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, rangeText('a', 100))
	assert.Contains(t, results[0].Content, rangeText('b', 100))
	assert.Equal(t, 100, results[0].EndAt, "rewritten result must not extend the range via stale coordinates")
}

func TestGroupAndMerge_TrustedContainedSubsumes(t *testing.T) {
	// Range-contained and text-verified: one result whose content is the
	// outer body; the inner chunk is recorded, not duplicated.
	outer := trustedResult("outer", 1, 0, 200, rangeText('a', 200))
	inner := trustedResult("inner", 2, 50, 100, rangeText('a', 50))

	results := mergeGrouped(docChunks(outer, inner))

	require.Len(t, results, 1)
	assert.Equal(t, rangeText('a', 200), results[0].Content)
	assert.ElementsMatch(t, []string{"inner"}, results[0].SubChunkID)
}

func TestGroupAndMerge_TrustedContainedDistinctKeepsBoth(t *testing.T) {
	// Range-contained but textually distinct: both bodies must survive; the
	// inner one must never be dropped on coordinates alone.
	outer := trustedResult("outer", 1, 0, 100, rangeText('a', 100))
	inner := trustedResult("inner", 2, 50, 60, rangeText('b', 10))

	results := mergeGrouped(docChunks(outer, inner))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, rangeText('a', 100))
	assert.Contains(t, results[0].Content, rangeText('b', 10))
	assert.ElementsMatch(t, []string{"inner"}, results[0].SubChunkID)
}

func TestGroupAndMerge_EditedPairKeepsBoth(t *testing.T) {
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	edited := &types.SearchResult{
		ID: "c2", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 2, StartAt: 80, EndAt: 200,
		Content: "rewritten body", ContentRevision: 1, Score: 1.0,
	}

	results := mergeGrouped(docChunks(first, edited))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, "rewritten body")
	assert.Contains(t, results[0].Content, rangeText('a', 100))
}

func TestGroupAndMerge_SameLengthRewriteKeepsBoth(t *testing.T) {
	// An edit that happens to keep the splitter length invariant must still
	// never cause the rewritten body to be swallowed on coordinates.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	edited := &types.SearchResult{
		ID: "c2", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 2, StartAt: 80, EndAt: 200,
		Content: rangeText('b', 120), ContentRevision: 1, Score: 1.0,
	}

	results := mergeGrouped(docChunks(first, edited))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, rangeText('a', 100))
	assert.Contains(t, results[0].Content, rangeText('b', 120))
}

func TestGroupAndMerge_InvariantBreakKeepsBoth(t *testing.T) {
	// ContentRevision 0 but length inconsistent with the range (e.g. zero-width
	// table headers, HTML entities): both bodies must survive.
	first := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	stale := &types.SearchResult{
		ID: "c2", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 2, StartAt: 80, EndAt: 200,
		Content: "short", Score: 1.0,
	}

	results := mergeGrouped(docChunks(first, stale))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, "short")
	assert.Contains(t, results[0].Content, rangeText('a', 100))
}

func TestGroupAndMerge_StartInversionKeepsBoth(t *testing.T) {
	// A chunk whose StartAt precedes the group's but has a higher ChunkIndex
	// contradicts document order: both bodies must survive.
	first := trustedResult("c1", 1, 100, 200, rangeText('a', 100))
	inverted := trustedResult("c2", 2, 50, 150, rangeText('b', 100))

	results := mergeGrouped(docChunks(first, inverted))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, rangeText('a', 100))
	assert.Contains(t, results[0].Content, rangeText('b', 100))
}

func TestGroupAndMerge_EditedNonSequentialSeparates(t *testing.T) {
	// Edited chunks that are neither text-contained nor index-sequential stay
	// separate results.
	first := &types.SearchResult{
		ID: "c1", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 1, StartAt: 0, EndAt: 50,
		Content: rangeText('a', 50), ContentRevision: 1, Score: 1.0,
	}
	third := &types.SearchResult{
		ID: "c3", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 3, StartAt: 200, EndAt: 250,
		Content: rangeText('b', 50), ContentRevision: 1, Score: 1.0,
	}

	results := mergeGrouped(docChunks(first, third))

	require.Len(t, results, 2, "non-sequential edited chunks must not merge")
}

func TestGroupAndMerge_MidGroupDriftKeepsAllBodies(t *testing.T) {
	// When an overlap cannot be trimmed the merged body no longer matches its
	// range; subsequent pairs must still merge safely without losing content.
	c1 := trustedResult("c1", 1, 0, 100, rangeText('a', 100))
	c2 := trustedResult("c2", 2, 90, 200, rangeText('c', 110))
	c3 := trustedResult("c3", 3, 200, 300, rangeText('e', 100))

	results := mergeGrouped(docChunks(c1, c2, c3))

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, rangeText('a', 100))
	assert.Contains(t, results[0].Content, rangeText('c', 110))
	assert.Contains(t, results[0].Content, rangeText('e', 100))
	assert.ElementsMatch(t, []string{"c2", "c3"}, results[0].SubChunkID)
}
