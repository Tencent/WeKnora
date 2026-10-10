package reranking

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
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

func TestSelectMMR_preservesDiversityAndTies(t *testing.T) {
	results := []*types.SearchResult{
		{Content: "hybrid retrieval combines semantic vectors and keyword search", Score: 0.9},
		{Content: "**hybrid retrieval** combines semantic vectors and keyword search", Score: 0.88},
		{Content: "wiki construction extracts entities concepts and source citations", Score: 0.83},
	}
	if got := SelectMMR(context.Background(), results, 3, DefaultMMRLambda); !slices.Equal(got, []int{0, 2, 1}) {
		t.Fatalf("selection = %v, want relevant, diverse, then redundant", got)
	}
	for _, r := range results {
		r.Content, r.Score = "", 0.5
	}
	if got := SelectMMR(context.Background(), results, 10, DefaultMMRLambda); !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("empty tied passages = %v, want original order", got)
	}
}

func TestSelectMMR_multilingualEnrichedPassages(t *testing.T) {
	results := []*types.SearchResult{
		{Content: "混合检索结合语义向量和关键词索引，提高知识库检索覆盖率。", Score: 0.9},
		{Content: "混合检索结合语义向量和关键词索引，提高知识库检索覆盖率。", Score: 0.85},
		{Content: "Wiki 构建从文档抽取实体和概念，保留事实引用与来源分块。", Score: 0.8},
		{Content: "retrieval", ImageInfo: `[{"caption":"semantic vectors and keyword search"}]`, Score: 0.75},
		{
			Content: "sources", Score: 0.7,
			ChunkMetadata: types.JSON(`{"generated_questions":[
				{"id":"q1","question":"How does wiki construction preserve source citations?"}
			]}`),
		},
		{Content: "", Score: 0.7},
	}
	for _, lambda := range []float64{0, 0.3, DefaultMMRLambda, 1} {
		for _, k := range []int{1, 3, len(results), len(results) + 1} {
			got := SelectMMR(context.Background(), results, k, lambda)
			want := selectMMRNaive(context.Background(), results, k, lambda)
			if !slices.Equal(got, want) {
				t.Fatalf("k=%d lambda=%v: selection = %v, naive = %v", k, lambda, got, want)
			}
		}
	}
}

// mmrPassageCorpus models a rerank pool with overlapping vocabulary across
// documents and less overlap across topics. It is synthetic and deterministic;
// the benchmarks measure MMR computation, not end-to-end retrieval quality.
func mmrPassageCorpus(n, words int, chinese bool) []*types.SearchResult {
	results := make([]*types.SearchResult, n)
	terms := []string{"检索", "知识库", "向量", "索引", "语义", "文档", "引用", "实体", "概念", "构建", "答案", "模型"}
	for i := range results {
		var content strings.Builder
		for j := 0; j < words; j++ {
			if chinese {
				fmt.Fprintf(&content, "%s ", terms[(i+j)%len(terms)])
			}
			// Different topics have disjoint bands; nearby documents within
			// a topic share most of their vocabulary.
			fmt.Fprintf(&content, "term%d ", (i%8)*1000+(j+i/8*7)%500)
		}
		results[i] = &types.SearchResult{
			ID: fmt.Sprintf("chunk-%03d", i), Content: content.String(),
			Score: 1 - float64(i%100)/200,
		}
	}
	return results
}

func BenchmarkSelectMMRPassages(b *testing.B) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		n, words, k int
		chinese     bool
	}{
		{name: "short", n: 50, words: 12, k: 10},
		{name: "english_top10", n: 200, words: 200, k: 10},
		{name: "english_top30", n: 200, words: 200, k: 30},
		{name: "mixed_top10", n: 200, words: 200, k: 10, chinese: true},
		{name: "mixed_top30", n: 200, words: 200, k: 30, chinese: true},
		{name: "top1", n: 200, words: 200, k: 1},
	} {
		b.Run(tc.name, func(b *testing.B) {
			results := mmrPassageCorpus(tc.n, tc.words, tc.chinese)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				SelectMMR(ctx, results, tc.k, DefaultMMRLambda)
			}
		})
	}
}
