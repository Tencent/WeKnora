package reranking

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

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

func (s *stubReranker) Rerank(_ context.Context, _ string, documents []string) ([]rerank.RankResult, error) {
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

func rows(contents ...string) []*types.SearchResult {
	out := make([]*types.SearchResult, len(contents))
	for i, c := range contents {
		out[i] = &types.SearchResult{ID: "c" + string(rune('1'+i)), Content: c, Score: 0.5}
	}
	return out
}

func ids(results []*types.SearchResult) string {
	parts := make([]string, len(results))
	for i, r := range results {
		parts[i] = r.ID
	}
	return strings.Join(parts, ",")
}

func TestRerank_thresholdKeepsPassingBestFirst(t *testing.T) {
	t.Parallel()
	in := rows("alpha", "beta", "gamma")
	res := Rerank(context.Background(), &stubReranker{scores: []float64{0.4, 0.9, 0.1}}, "q", in,
		Options{Threshold: 0.3, FallbackMinScore: DefaultFallbackMinScore})

	if got := ids(res.Results); got != "c2,c1" {
		t.Fatalf("results = %s, want c2,c1", got)
	}
	if res.Indices[0] != 1 || res.Indices[1] != 0 {
		t.Fatalf("indices = %v, want [1 0]", res.Indices)
	}
	d := res.Diagnostics
	if !d.Applied || d.Outcome != types.RerankOutcomeOK || d.TopScore != 0.9 ||
		d.CandidateCount != 3 || d.ResultCount != 2 {
		t.Fatalf("diagnostics = %+v", d)
	}
	if res.Results[0].Metadata["model_score"] != "0.9000" || res.Results[0].Metadata["base_score"] != "0.5000" {
		t.Fatalf("metadata = %v", res.Results[0].Metadata)
	}
	if want := 0.6*0.9 + 0.3*0.5 + 0.1; math.Abs(res.Results[0].Score-want) > 1e-9 {
		t.Fatalf("composite = %v, want %v", res.Results[0].Score, want)
	}
	// Inputs are never modified.
	if in[1].Score != 0.5 || in[1].Metadata != nil {
		t.Fatalf("input row was mutated: %+v", in[1])
	}
}

func TestRerank_degradesHighThresholdBeforeFallback(t *testing.T) {
	t.Parallel()
	// 0.5 rejects everything; degraded threshold is max(0.5*0.7, 0.3) = 0.35.
	res := Rerank(context.Background(), &stubReranker{scores: []float64{0.36, 0.2}}, "q", rows("a", "b"),
		Options{Threshold: 0.5, FallbackMinScore: DefaultFallbackMinScore})
	if got := ids(res.Results); got != "c1" {
		t.Fatalf("results = %s, want c1", got)
	}
	if res.Diagnostics.Outcome != types.RerankOutcomeThresholdDegraded ||
		math.Abs(res.Diagnostics.EffectiveThreshold-0.35) > 1e-9 {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestRerank_fallbackTop1AndEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	res := Rerank(ctx, &stubReranker{scores: []float64{0.05, 0.20}}, "q", rows("a", "b"),
		Options{Threshold: 0.3, FallbackMinScore: DefaultFallbackMinScore})
	if ids(res.Results) != "c2" || res.Diagnostics.Outcome != types.RerankOutcomeFallbackTop1 {
		t.Fatalf("expected best candidate fallback, got %s %+v", ids(res.Results), res.Diagnostics)
	}

	res = Rerank(ctx, &stubReranker{scores: []float64{0.10, 0.04}}, "q", rows("a", "b"),
		Options{Threshold: 0.3, FallbackMinScore: DefaultFallbackMinScore})
	if len(res.Results) != 0 || res.Diagnostics.Outcome != types.RerankOutcomeAllBelowThreshold ||
		res.Diagnostics.TopScore != 0.10 {
		t.Fatalf("expected empty result, got %s %+v", ids(res.Results), res.Diagnostics)
	}

	// An explicit scope keeps its best candidate even with a negative score.
	res = Rerank(ctx, &stubReranker{scores: []float64{-2, -1}}, "q", rows("a", "b"),
		Options{Threshold: 0.3, FallbackMinScore: FallbackMinScore(true)})
	if ids(res.Results) != "c2" {
		t.Fatalf("explicit scope must keep its best candidate, got %s", ids(res.Results))
	}
}

func TestRerank_modelErrorReturnsInputUnchanged(t *testing.T) {
	t.Parallel()
	in := rows("a", "b")
	model := &stubReranker{err: errors.New("upstream 500")}
	res := Rerank(context.Background(), model, "q", in, Options{Threshold: 0.3})
	if res.Diagnostics.Outcome != types.RerankOutcomeModelError || res.Diagnostics.Applied ||
		res.Diagnostics.Error != "upstream 500" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if len(res.Results) != 2 || res.Results[0] != in[0] || res.Results[1] != in[1] {
		t.Fatalf("expected the input rows, got %#v", res.Results)
	}
	if model.calls != 1 {
		t.Fatalf("expected one model call, got %d", model.calls)
	}
}

func TestRerank_skipsEmptyPassagesAndReportsNoCandidates(t *testing.T) {
	t.Parallel()
	model := &stubReranker{scores: []float64{0.9}}
	res := Rerank(context.Background(), model, "q", rows("   ", "body"), Options{Threshold: 0.3})
	if len(model.documents) != 1 || ids(res.Results) != "c2" || res.Indices[0] != 1 {
		t.Fatalf("documents=%q results=%s indices=%v", model.documents, ids(res.Results), res.Indices)
	}

	model = &stubReranker{}
	res = Rerank(context.Background(), model, "q", rows(""), Options{Threshold: 0.3})
	if model.calls != 0 || res.Diagnostics.Outcome != types.RerankOutcomeNoCandidates {
		t.Fatalf("calls=%d diagnostics=%+v", model.calls, res.Diagnostics)
	}
}

func TestRerank_topKAppliesMMRAndFAQBoost(t *testing.T) {
	t.Parallel()
	in := rows("alpha beta", "gamma delta", "epsilon zeta")
	in[2].ChunkType = string(types.ChunkTypeFAQ)
	res := Rerank(context.Background(), &stubReranker{scores: []float64{0.9, 0.8, 0.7}}, "q", in,
		Options{Threshold: 0.3, TopK: 2, FAQScoreBoost: 1.5})
	if len(res.Results) != 2 || len(res.Scored) != 3 {
		t.Fatalf("results=%d scored=%d", len(res.Results), len(res.Scored))
	}
	// The FAQ entry is boosted from 0.67 to 1.0 and ranks first.
	if res.Results[0].ID != "c3" || res.Results[0].Metadata["faq_boosted"] != "true" || res.Results[0].Score != 1 {
		t.Fatalf("FAQ boost not applied: %+v", res.Results[0])
	}
}

// A chunk rarely names its document's subject, so the rerank model sees the
// document title; FAQ entries are scored on their own question.
func TestModelPassage_prefixesDocumentTitle(t *testing.T) {
	t.Parallel()
	model := &stubReranker{scores: []float64{0.5, 0.5, 0.5}}
	Rerank(context.Background(), model, "q", []*types.SearchResult{
		{
			ID: "c1", Content: "allocate inference across **open-weight** models",
			KnowledgeTitle: " Show HN: Echo ", ChunkType: string(types.ChunkTypeText),
		},
		{ID: "c2", Content: "What is Echo?", KnowledgeTitle: "FAQ set", ChunkType: string(types.ChunkTypeFAQ)},
		{ID: "c3", Content: "untitled"},
	}, Options{})
	want := []string{"Show HN: Echo\n\nallocate inference across open-weight models", "What is Echo?", "untitled"}
	for i, w := range want {
		if model.documents[i] != w {
			t.Fatalf("passage %d = %q, want %q", i, model.documents[i], w)
		}
	}
}

func TestFallbackMinScore(t *testing.T) {
	t.Parallel()
	if got := FallbackMinScore(false); got != DefaultFallbackMinScore {
		t.Fatalf("default fallback minimum = %v", got)
	}
	if got := FallbackMinScore(true); !math.IsInf(got, -1) {
		t.Fatalf("explicit-scope fallback minimum = %v, want -Inf", got)
	}
}
