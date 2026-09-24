package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/reranking"
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

// Execute must surface rerank_rejected through Data and the empty statement
// when the model scores every retrieved candidate below the fallback floor.
func TestExecuteReportsRerankRejection(t *testing.T) {
	t.Parallel()
	model := &stubReranker{scores: []float64{0.10, 0.04}}
	svc := &stubKnowledgeBaseService{
		results: []*types.SearchResult{
			{
				ID: "c1", Content: "comments section", KnowledgeID: "d1", KnowledgeBaseID: "kb-1",
				KnowledgeTitle: "Show HN: Echo", Score: 0.8,
			},
			{
				ID: "c2", Content: "unrelated notes", KnowledgeID: "d2", KnowledgeBaseID: "kb-1",
				KnowledgeTitle: "Other", Score: 0.7,
			},
		},
	}
	tool := NewSearchKnowledgeTool(
		svc, nil, nil,
		types.SearchTargets{{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 1,
		}},
		model,
		&config.Config{Conversation: &config.ConversationConfig{RerankThreshold: 0.3}},
	)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"query":"Why does Echo use open-weight models to cut cost?"}`,
	))
	if err != nil || res == nil || !res.Success {
		t.Fatalf("Execute: res=%+v err=%v", res, err)
	}
	if got, _ := res.Data["rerank_rejected"].(int); got != 2 {
		t.Fatalf("rerank_rejected = %v, want 2; data=%+v", res.Data["rerank_rejected"], res.Data)
	}
	if !strings.Contains(res.Output, "found 2 candidate chunks") ||
		!strings.Contains(res.Output, "Repeating the same query") ||
		!strings.Contains(res.Output, "mode=keyword") {
		t.Fatalf("empty statement = %q", res.Output)
	}
	if strings.Contains(res.Output, "switching mode will not help") {
		t.Fatalf("statement must not forbid a keyword retry: %q", res.Output)
	}
}

func TestRerankThreshold_default(t *testing.T) {
	t.Parallel()
	tool := &SearchKnowledgeTool{}
	if got := tool.rerankThreshold(); got != reranking.DefaultThreshold {
		t.Fatalf("default threshold = %v, want %v", got, reranking.DefaultThreshold)
	}
}
