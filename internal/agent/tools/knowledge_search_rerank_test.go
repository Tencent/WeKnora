package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestFilterRerankRankResults_thresholdAndFallback(t *testing.T) {
	t.Parallel()
	rankResults := []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.02},
	}
	filtered := filterRerankRankResults(rankResults, 0.3, false)
	if len(filtered) != 0 {
		t.Fatalf("expected empty filter, got %#v", filtered)
	}

	rankResults = []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.20},
	}
	filtered = filterRerankRankResults(rankResults, 0.3, false)
	if len(filtered) != 1 || filtered[0].Index != 1 {
		t.Fatalf("expected fallback top score, got %#v", filtered)
	}

	rankResults = []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.02},
	}
	filtered = filterRerankRankResults(rankResults, 0.3, true)
	if len(filtered) != 1 || filtered[0].Index != 0 {
		t.Fatalf("expected explicit scope to preserve top result, got %#v", filtered)
	}

	rankResults = []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.8},
		{Index: 1, RelevanceScore: 0.4},
		{Index: 2, RelevanceScore: 0.1},
	}
	filtered = filterRerankRankResults(rankResults, 0.3, false)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 passing scores, got %#v", filtered)
	}
}

func TestApplyModelRerankScores_faqUsesCompositeScale(t *testing.T) {
	t.Parallel()
	tool := &KnowledgeSearchTool{
		config: &config.Config{
			Conversation: &config.ConversationConfig{RerankThreshold: 0.3},
		},
	}
	originals := []*searchResultWithMeta{
		{
			SearchResult:      &types.SearchResult{ID: "faq-1", Content: "Q: WeKnora", Score: 0.011},
			KnowledgeBaseType: types.KnowledgeBaseTypeFAQ,
		},
		{
			SearchResult: &types.SearchResult{ID: "doc-1", Content: "swimming club", Score: 0.02},
		},
	}
	rankResults := []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.9},
	}
	out := tool.applyModelRerankScores(originals, rankResults, 0.3, false)
	if len(out) != 1 || out[0].ID != "doc-1" {
		t.Fatalf("weak FAQ should be filtered out, got %#v", out)
	}
	if out[0].Score <= 0.011 {
		t.Fatalf("composite score should exceed raw retrieval score, got %.4f", out[0].Score)
	}
}

func TestRerankThreshold_default(t *testing.T) {
	t.Parallel()
	tool := &KnowledgeSearchTool{}
	if got := tool.rerankThreshold(); got != 0.3 {
		t.Fatalf("default threshold = %v, want 0.3", got)
	}
}
