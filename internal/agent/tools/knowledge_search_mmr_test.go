package tools

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// applyMMRNaive is the pre-optimization form of applyMMR: it recomputes the
// redundancy of every candidate against every already-selected result on each
// round, re-tokenizing the selected passages every time. It is kept here purely
// as the reference oracle for the incremental implementation.
func (t *KnowledgeSearchTool) applyMMRNaive(
	ctx context.Context,
	results []*searchResultWithMeta,
	k int,
	lambda float64,
) []*searchResultWithMeta {
	if k <= 0 || len(results) == 0 {
		return nil
	}

	selected := make([]*searchResultWithMeta, 0, k)
	candidates := make([]*searchResultWithMeta, len(results))
	copy(candidates, results)

	tokenSets := make([]map[string]struct{}, len(candidates))
	for i, r := range candidates {
		tokenSets[i] = t.tokenizeSimple(t.getEnrichedPassage(ctx, r.SearchResult))
	}

	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0

		for i, r := range candidates {
			relevance := r.Score
			redundancy := 0.0
			for _, s := range selected {
				selectedTokens := t.tokenizeSimple(t.getEnrichedPassage(ctx, s.SearchResult))
				redundancy = math.Max(redundancy, t.jaccard(tokenSets[i], selectedTokens))
			}
			mmr := lambda*relevance - (1.0-lambda)*redundancy
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}

		selected = append(selected, candidates[bestIdx])
		candidates = append(candidates[:bestIdx], candidates[bestIdx+1:]...)
		tokenSets = append(tokenSets[:bestIdx], tokenSets[bestIdx+1:]...)
	}

	return selected
}

// mmrTestCorpus builds a deterministic candidate set whose passages share
// vocabulary in overlapping bands, so redundancy actually drives selection
// instead of the score alone.
func mmrTestCorpus(n int) []*searchResultWithMeta {
	vocab := []string{
		"insurance", "policy", "claim", "premium", "deductible", "liability",
		"coverage", "endorsement", "underwriting", "reinsurance", "subrogation",
		"indemnity", "exclusion", "rider", "annuity",
	}

	results := make([]*searchResultWithMeta, 0, n)
	// Simple LCG so the corpus is identical on every run and every platform.
	state := uint64(42)
	next := func(mod int) int {
		state = state*6364136223846793005 + 1442695040888963407
		return int((state >> 33) % uint64(mod))
	}

	for i := 0; i < n; i++ {
		words := make([]byte, 0, 128)
		for j := 0; j < 12; j++ {
			words = append(words, vocab[next(len(vocab))]...)
			words = append(words, ' ')
		}
		content := fmt.Sprintf("chunk %d %s", i, string(words))
		results = append(results, &searchResultWithMeta{
			SearchResult: &types.SearchResult{
				ID:          fmt.Sprintf("chunk-%03d", i),
				Content:     content,
				KnowledgeID: fmt.Sprintf("doc-%d", i%7),
				// Scores intentionally collide across candidates so the
				// tie-breaking path is exercised too.
				Score: float64(next(20)) / 20.0,
			},
			QueryType:       "vector",
			KnowledgeBaseID: "kb-1",
		})
	}
	return results
}

func TestApplyMMR_matchesNaiveSelection(t *testing.T) {
	t.Parallel()

	tool := &KnowledgeSearchTool{}
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
		tc := tc
		t.Run(fmt.Sprintf("k=%d/lambda=%.1f", tc.k, tc.lambda), func(t *testing.T) {
			want := tool.applyMMRNaive(ctx, results, tc.k, tc.lambda)
			got := tool.applyMMR(ctx, results, tc.k, tc.lambda)

			if len(got) != len(want) {
				t.Fatalf("selected %d results, naive selected %d", len(got), len(want))
			}
			for i := range want {
				if got[i].ID != want[i].ID {
					t.Fatalf("rank %d: got %s, naive got %s", i, got[i].ID, want[i].ID)
				}
			}
		})
	}
}

func TestApplyMMR_emptyAndNonPositiveK(t *testing.T) {
	t.Parallel()

	tool := &KnowledgeSearchTool{}
	ctx := context.Background()

	if got := tool.applyMMR(ctx, mmrTestCorpus(3), 0, 0.7); got != nil {
		t.Fatalf("expected nil for k=0, got %d results", len(got))
	}
	if got := tool.applyMMR(ctx, nil, 5, 0.7); got != nil {
		t.Fatalf("expected nil for empty candidates, got %d results", len(got))
	}
}

func BenchmarkApplyMMR(b *testing.B) {
	tool := &KnowledgeSearchTool{}
	ctx := context.Background()
	results := mmrTestCorpus(250)

	b.Run("incremental", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			tool.applyMMR(ctx, results, 250, 0.7)
		}
	})
	b.Run("naive", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			tool.applyMMRNaive(ctx, results, 250, 0.7)
		}
	})
}
