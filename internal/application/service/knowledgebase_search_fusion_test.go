package service

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFuseOrDeduplicate_KeywordOnlyRescalesUnboundedBM25(t *testing.T) {
	t.Parallel()

	got := fuseOrDeduplicate(context.Background(), nil, [][]*types.IndexWithScore{{
		{ChunkID: "strong", Score: 16.1239},
		{ChunkID: "mid", Score: 8.06195},
		{ChunkID: "weak", Score: 4.030975},
	}}, nil)

	require.Len(t, got, 3)
	require.Equal(t, "strong", got[0].ChunkID)
	require.InDelta(t, 1.0, got[0].Score, 1e-9)
	require.InDelta(t, 0.5, got[1].Score, 1e-9)
	require.InDelta(t, 0.25, got[2].Score, 1e-9)

	// 0.3*base stays below the composite clamp, so model score can still discriminate.
	require.Less(t, 0.3*got[0].Score, 1.0)
	require.Less(t, 0.3*got[1].Score, 0.3*got[0].Score)
}

func TestFuseOrDeduplicate_KeywordOnlyLeavesUnitIntervalScores(t *testing.T) {
	t.Parallel()

	flat := fuseOrDeduplicate(context.Background(), nil, [][]*types.IndexWithScore{{
		{ChunkID: "a", Score: 1.0},
		{ChunkID: "b", Score: 1.0},
	}}, nil)
	require.Len(t, flat, 2)
	require.InDelta(t, 1.0, flat[0].Score, 1e-9)
	require.InDelta(t, 1.0, flat[1].Score, 1e-9)

	bounded := fuseOrDeduplicate(context.Background(), nil, [][]*types.IndexWithScore{{
		{ChunkID: "high", Score: 0.8},
		{ChunkID: "low", Score: 0.4},
	}}, nil)
	require.Equal(t, "high", bounded[0].ChunkID)
	require.InDelta(t, 0.8, bounded[0].Score, 1e-9)
	require.InDelta(t, 0.4, bounded[1].Score, 1e-9)
}

func TestFuseOrDeduplicate_VectorOnlyKeepsEmbeddingScores(t *testing.T) {
	t.Parallel()

	got := fuseOrDeduplicate(context.Background(), [][]*types.IndexWithScore{{
		{ChunkID: "near", Score: 0.91},
		{ChunkID: "far", Score: 0.22},
	}}, nil, nil)

	require.Equal(t, "near", got[0].ChunkID)
	require.InDelta(t, 0.91, got[0].Score, 1e-9)
	require.InDelta(t, 0.22, got[1].Score, 1e-9)
}

func TestFuseOrDeduplicate_HybridUsesRRFNotRawBM25(t *testing.T) {
	t.Parallel()

	got := fuseOrDeduplicate(context.Background(),
		[][]*types.IndexWithScore{{{ChunkID: "vec", Score: 0.9}}},
		[][]*types.IndexWithScore{{{ChunkID: "kw", Score: 16.1239}}},
		nil,
	)

	require.Len(t, got, 2)
	for _, hit := range got {
		require.Greater(t, hit.Score, 0.0)
		require.Less(t, hit.Score, 1.0)
		require.NotEqual(t, 16.1239, hit.Score)
	}
	// Normalized RRF: rank 1 for one retriever only scores that retriever's
	// share of the total weight (defaults 0.7 / 0.3).
	require.Equal(t, "vec", got[0].ChunkID)
	require.InDelta(t, 0.7, got[0].Score, 1e-9)
	require.InDelta(t, 0.3, got[1].Score, 1e-9)
}

func TestFuseOrDeduplicate_HybridTopOfBothScoresOne(t *testing.T) {
	t.Parallel()

	got := fuseOrDeduplicate(context.Background(),
		[][]*types.IndexWithScore{{{ChunkID: "both", Score: 0.9}, {ChunkID: "vec", Score: 0.8}}},
		[][]*types.IndexWithScore{{{ChunkID: "both", Score: 12}}},
		nil,
	)
	require.Equal(t, "both", got[0].ChunkID)
	require.InDelta(t, 1.0, got[0].Score, 1e-9)
}

// Lists are ranked on their own. The FAQ list arrives second (goroutine
// completion order), but its best hit is still vector rank 1 — not rank 4
// behind every document hit.
func TestFuseOrDeduplicate_HybridRanksEachListOnItsOwn(t *testing.T) {
	t.Parallel()

	docList := []*types.IndexWithScore{
		{ChunkID: "doc-1", Score: 0.81}, {ChunkID: "doc-2", Score: 0.8}, {ChunkID: "doc-3", Score: 0.79},
	}
	// Engines return lists sorted, but nothing guarantees it; ranks follow score.
	faqList := []*types.IndexWithScore{{ChunkID: "faq-2", Score: 0.7}, {ChunkID: "faq-1", Score: 0.93}}
	keywordList := []*types.IndexWithScore{{ChunkID: "faq-1", Score: 7}, {ChunkID: "doc-3", Score: 3}}

	got := fuseOrDeduplicate(context.Background(),
		[][]*types.IndexWithScore{docList, faqList},
		[][]*types.IndexWithScore{keywordList},
		nil,
	)
	require.Equal(t, "faq-1", got[0].ChunkID, "vector rank 1 in its list plus keyword rank 1")
	require.InDelta(t, 1.0, got[0].Score, 1e-9)

	scores := map[string]float64{}
	for _, hit := range got {
		scores[hit.ChunkID] = hit.Score
	}
	// doc-1 and faq-2 hold rank 1 and 2 of their own lists.
	require.InDelta(t, 0.7, scores["doc-1"], 1e-9)
	require.InDelta(t, 0.7*61.0/62.0, scores["faq-2"], 1e-9)
}

func TestFuseOrDeduplicate_KeywordOnlyScalesEachList(t *testing.T) {
	t.Parallel()

	got := fuseOrDeduplicate(context.Background(), nil, [][]*types.IndexWithScore{
		{{ChunkID: "pg-top", Score: 20}, {ChunkID: "pg-low", Score: 10}},
		{{ChunkID: "es-top", Score: 4}},
	}, nil)
	scores := map[string]float64{}
	for _, hit := range got {
		scores[hit.ChunkID] = hit.Score
	}
	require.InDelta(t, 1.0, scores["pg-top"], 1e-9)
	require.InDelta(t, 0.5, scores["pg-low"], 1e-9)
	require.InDelta(t, 1.0, scores["es-top"], 1e-9, "each engine's BM25 is scaled by its own best score")
}

func TestRescaleUnboundedScores_IgnoresNonFiniteWhenFindingMax(t *testing.T) {
	t.Parallel()

	hits := []*types.IndexWithScore{
		{ChunkID: "nan", Score: math.NaN()},
		{ChunkID: "top", Score: 10},
		{ChunkID: "low", Score: 5},
		nil,
	}
	rescaleUnboundedScores(hits)
	require.Equal(t, 0.0, hits[0].Score)
	require.InDelta(t, 1.0, hits[1].Score, 1e-9)
	require.InDelta(t, 0.5, hits[2].Score, 1e-9)
}

// The retrievers run concurrently and their lists are appended in goroutine
// completion order; the order of the hits inside a list is whatever the engine
// returned. Neither may influence a chunk's rank: ranks are per-list and read
// after sorting by score (#3796, fixed by #3687).
func TestBestRanks_IsOrderInvariant(t *testing.T) {
	t.Parallel()

	rankLists := func(reversed bool) [][]*types.IndexWithScore {
		docList := []*types.IndexWithScore{
			{ChunkID: "doc-1", Score: 0.81},
			{ChunkID: "doc-2", Score: 0.80},
			{ChunkID: "doc-3", Score: 0.79},
		}
		faqList := []*types.IndexWithScore{
			{ChunkID: "faq-1", Score: 0.93},
			{ChunkID: "faq-2", Score: 0.70},
		}
		keywordList := []*types.IndexWithScore{
			{ChunkID: "faq-1", Score: 7},
			{ChunkID: "doc-3", Score: 3},
		}
		if reversed {
			slices.Reverse(docList)
			slices.Reverse(faqList)
			slices.Reverse(keywordList)
			return [][]*types.IndexWithScore{keywordList, faqList, docList}
		}
		return [][]*types.IndexWithScore{docList, faqList, keywordList}
	}

	// faq-1 tops both of its lists. doc-3 holds rank 2 in the keyword list and
	// rank 3 in the document list, so its best rank is 2.
	want := map[string]int{"doc-1": 1, "doc-2": 2, "doc-3": 2, "faq-1": 1, "faq-2": 2}
	require.Equal(t, want, bestRanks(rankLists(false)))
	require.Equal(t, want, bestRanks(rankLists(true)),
		"shuffling the lists must not change the ranks")
}

// The same property end to end: the list that finishes last must not push its
// best hit behind every hit of the list that finished first.
func TestFuseOrDeduplicate_HybridIgnoresArrivalOrder(t *testing.T) {
	t.Parallel()

	fuse := func(reversed bool) map[string]float64 {
		t.Helper()
		docList := []*types.IndexWithScore{
			{ChunkID: "doc-1", Score: 0.81},
			{ChunkID: "doc-2", Score: 0.80},
		}
		faqList := []*types.IndexWithScore{
			{ChunkID: "faq-1", Score: 0.93},
			{ChunkID: "faq-2", Score: 0.70},
		}
		keywordList := []*types.IndexWithScore{{ChunkID: "faq-1", Score: 7}}

		vectorLists := [][]*types.IndexWithScore{docList, faqList}
		if reversed {
			slices.Reverse(docList)
			slices.Reverse(faqList)
			// The FAQ list is the one that arrives first here.
			vectorLists = [][]*types.IndexWithScore{faqList, docList}
		}
		scores := map[string]float64{}
		for _, hit := range fuseOrDeduplicate(context.Background(), vectorLists,
			[][]*types.IndexWithScore{keywordList}, nil) {
			scores[hit.ChunkID] = hit.Score
		}
		return scores
	}

	// fuseOrDeduplicate writes its RRF scores into the input hits, so each call
	// builds its own fixtures.
	require.Equal(t, fuse(false), fuse(true))
}
