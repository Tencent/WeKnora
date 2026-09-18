package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// stubReranker returns canned scores (or an error) without any network call.
type stubReranker struct {
	scores    []float64
	err       error
	calls     int
	documents []string
}

func (s *stubReranker) Rerank(
	_ context.Context, _ string, documents []string,
) ([]rerank.RankResult, error) {
	s.calls++
	s.documents = documents
	if s.err != nil {
		return nil, s.err
	}
	out := make([]rerank.RankResult, 0, len(documents))
	for i := range documents {
		score := 0.0
		if i < len(s.scores) {
			score = s.scores[i]
		}
		out = append(out, rerank.RankResult{Index: i, RelevanceScore: score})
	}
	return out, nil
}

func (s *stubReranker) GetModelName() string { return "stub-rerank" }
func (s *stubReranker) GetModelID() string   { return "stub-rerank-id" }

func newRerankTestTool(model rerank.Reranker) *SearchKnowledgeTool {
	return &SearchKnowledgeTool{
		rerankModel: model,
		config: &config.Config{
			Conversation: &config.ConversationConfig{RerankThreshold: 0.3},
		},
	}
}

func newRerankTestResults() []*searchResultWithMeta {
	return []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{ID: "c1", Content: "alpha", Score: 0.02}},
		{SearchResult: &types.SearchResult{ID: "c2", Content: "beta", Score: 0.01}},
	}
}

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
	tool := &SearchKnowledgeTool{
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

// A rerank API failure must degrade to the raw retrieval order rather than
// dropping the recall set or re-scoring it with a chat model.
func TestRerankResults_modelErrorKeepsRawResults(t *testing.T) {
	t.Parallel()
	model := &stubReranker{err: errors.New("upstream 500")}
	tool := newRerankTestTool(model)
	results := newRerankTestResults()

	out, err := tool.rerankResults(context.Background(), "query", results)
	if err != nil {
		t.Fatalf("rerankResults returned error: %v", err)
	}
	if len(out) != len(results) {
		t.Fatalf("expected raw results to be preserved, got %d", len(out))
	}
	for i := range out {
		if out[i] != results[i] {
			t.Fatalf("result %d was replaced: %#v", i, out[i])
		}
	}
	if model.calls != 1 {
		t.Fatalf("expected exactly one rerank call, got %d", model.calls)
	}
}

// Scores below agentRerankFallbackMinScore mean nothing is relevant; the tool
// must return empty instead of resurrecting the candidates.
func TestRerankResults_allBelowFallbackFloorReturnsEmpty(t *testing.T) {
	t.Parallel()
	tool := newRerankTestTool(&stubReranker{scores: []float64{0.10, 0.04}})

	out, err := tool.rerankResults(context.Background(), "query", newRerankTestResults())
	if err != nil {
		t.Fatalf("rerankResults returned error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no results, got %#v", out)
	}
}

func TestRerankResults_keepsCandidatesAboveThreshold(t *testing.T) {
	t.Parallel()
	tool := newRerankTestTool(&stubReranker{scores: []float64{0.9, 0.05}})

	out, err := tool.rerankResults(context.Background(), "query", newRerankTestResults())
	if err != nil {
		t.Fatalf("rerankResults returned error: %v", err)
	}
	if len(out) != 1 || out[0].ID != "c1" {
		t.Fatalf("expected only the strong candidate, got %#v", out)
	}
}

func TestRerankResults_withoutModelIsPassthrough(t *testing.T) {
	t.Parallel()
	tool := newRerankTestTool(nil)
	results := newRerankTestResults()

	out, err := tool.rerankResults(context.Background(), "query", results)
	if err != nil {
		t.Fatalf("rerankResults returned error: %v", err)
	}
	if len(out) != len(results) {
		t.Fatalf("expected passthrough, got %d results", len(out))
	}
}

// A chunk rarely names its document's subject, so the rerank model must see
// the document title; FAQ entries are scored on their own question.
func TestRerankScores_prefixDocumentTitle(t *testing.T) {
	t.Parallel()
	model := &stubReranker{scores: []float64{0.5, 0.5, 0.5}}
	tool := newRerankTestTool(model)
	results := []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{
			ID: "c1", Content: "allocate inference across open-weight models",
			KnowledgeTitle: " Show HN: Echo ", ChunkType: string(types.ChunkTypeText),
		}},
		{SearchResult: &types.SearchResult{
			ID: "c2", Content: "What is Echo?", KnowledgeTitle: "FAQ set", ChunkType: string(types.ChunkTypeFAQ),
		}},
		{SearchResult: &types.SearchResult{ID: "c3", Content: "untitled"}},
	}

	if _, err := tool.rerankScores(context.Background(), "query", results); err != nil {
		t.Fatalf("rerankScores returned error: %v", err)
	}
	want := []string{"Show HN: Echo\n\nallocate inference across open-weight models", "What is Echo?", "untitled"}
	for i, w := range want {
		if model.documents[i] != w {
			t.Fatalf("passage %d = %q, want %q", i, model.documents[i], w)
		}
	}
}

func TestRerankThreshold_default(t *testing.T) {
	t.Parallel()
	tool := &SearchKnowledgeTool{}
	if got := tool.rerankThreshold(); got != 0.3 {
		t.Fatalf("default threshold = %v, want 0.3", got)
	}
}
