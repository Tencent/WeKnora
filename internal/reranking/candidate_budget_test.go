package reranking

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRerankCandidateBudgetSkipsEmptyPassages(t *testing.T) {
	input := rows("", "relevant document", "another document")
	input[0].Score = 1
	input[1].Score = 0.9
	input[2].Score = 0.8
	model := &stubReranker{scores: []float64{0.9}}
	result := Rerank(context.Background(), model, "query", input, Options{MaxCandidates: 1, Threshold: 0.3})
	if len(model.documents) != 1 || model.documents[0] != "relevant document" {
		t.Fatalf("model passages = %v, want the best nonempty document", model.documents)
	}
	if len(result.Results) != 1 || result.Results[0].ID != input[1].ID ||
		len(result.Indices) != 1 || result.Indices[0] != 1 {
		t.Fatalf("results = %+v, indices = %v, want input row 1", result.Results, result.Indices)
	}
	if result.Diagnostics.Outcome == types.RerankOutcomeNoCandidates {
		t.Fatal("nonempty candidates should be reranked")
	}
}
