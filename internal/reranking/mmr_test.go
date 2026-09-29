package reranking

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// selectMMRNaive recomputes the redundancy of every candidate against every
// already-selected result on each round. It is the reference oracle for the
// incremental SelectMMR.
func selectMMRNaive(ctx context.Context, results []*types.SearchResult, k int, lambda float64) []int {
	if k <= 0 || len(results) == 0 {
		return nil
	}
	var selected []int
	taken := make(map[int]bool)
	for len(selected) < k && len(selected) < len(results) {
		best, bestScore := -1, math.Inf(-1)
		for i, r := range results {
			if taken[i] {
				continue
			}
			redundancy := 0.0
			for _, s := range selected {
				redundancy = math.Max(redundancy, searchutil.Jaccard(
					searchutil.TokenizeSimple(EnrichedPassage(ctx, r)),
					searchutil.TokenizeSimple(EnrichedPassage(ctx, results[s])),
				))
			}
			if mmr := lambda*r.Score - (1.0-lambda)*redundancy; mmr > bestScore {
				best, bestScore = i, mmr
			}
		}
		selected = append(selected, best)
		taken[best] = true
	}
	return selected
}

// mmrTestCorpus builds a deterministic candidate set whose passages share
// vocabulary in overlapping bands, so redundancy actually drives selection
// instead of the score alone.
func mmrTestCorpus(n int) []*types.SearchResult {
	vocab := []string{
		"insurance", "policy", "claim", "premium", "deductible", "liability",
		"coverage", "endorsement", "underwriting", "reinsurance", "subrogation",
		"indemnity", "exclusion", "rider", "annuity",
	}
	// Simple LCG so the corpus is identical on every run and every platform.
	state := uint64(42)
	next := func(mod int) int {
		state = state*6364136223846793005 + 1442695040888963407
		return int((state >> 33) % uint64(mod))
	}
	results := make([]*types.SearchResult, 0, n)
	for i := 0; i < n; i++ {
		words := make([]byte, 0, 128)
		for j := 0; j < 12; j++ {
			words = append(words, vocab[next(len(vocab))]...)
			words = append(words, ' ')
		}
		results = append(results, &types.SearchResult{
			ID:      fmt.Sprintf("chunk-%03d", i),
			Content: fmt.Sprintf("chunk %d %s", i, string(words)),
			// Scores intentionally collide so tie-breaking is exercised too.
			Score: float64(next(20)) / 20.0,
		})
	}
	return results
}

func TestSelectMMR_matchesNaiveSelection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	results := mmrTestCorpus(40)
	for _, tc := range []struct {
		k      int
		lambda float64
	}{
		{k: 1, lambda: 0.7},
		{k: 5, lambda: 0.7},
		{k: 12, lambda: 0.3},
		{k: 40, lambda: 0.9},
		{k: 60, lambda: 0.5}, // k larger than the candidate count
	} {
		t.Run(fmt.Sprintf("k=%d/lambda=%.1f", tc.k, tc.lambda), func(t *testing.T) {
			want := selectMMRNaive(ctx, results, tc.k, tc.lambda)
			got := SelectMMR(ctx, results, tc.k, tc.lambda)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("SelectMMR = %v, naive = %v", got, want)
			}
		})
	}
}

func TestSelectMMR_emptyAndNonPositiveK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := SelectMMR(ctx, mmrTestCorpus(3), 0, DefaultMMRLambda); got != nil {
		t.Fatalf("expected nil for k=0, got %v", got)
	}
	if got := SelectMMR(ctx, nil, 5, DefaultMMRLambda); got != nil {
		t.Fatalf("expected nil for empty candidates, got %v", got)
	}
}

func BenchmarkSelectMMR(b *testing.B) {
	ctx := context.Background()
	results := mmrTestCorpus(250)
	for i := 0; i < b.N; i++ {
		SelectMMR(ctx, results, 250, DefaultMMRLambda)
	}
}
