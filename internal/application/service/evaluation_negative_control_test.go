package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFilterRelevantEvaluationResults(t *testing.T) {
	qaPair := &types.QAPair{Passages: []string{"the expected passage"}}
	results := []*types.SearchResult{
		{ID: "relevant", Content: "expected passage"},
		{ID: "unrelated", Content: "different material"},
	}

	filtered := filterRelevantEvaluationResults(qaPair, results)
	if len(filtered) != 1 || filtered[0].ID != "unrelated" {
		t.Fatalf("filtered = %#v, want only unrelated result", filtered)
	}
}

func TestFilterRelevantEvaluationResultsLeavesInputUnchanged(t *testing.T) {
	qaPair := &types.QAPair{Passages: []string{"expected passage"}}
	results := []*types.SearchResult{{ID: "unrelated", Content: "different material"}}

	filtered := filterRelevantEvaluationResults(qaPair, results)
	if len(filtered) != 1 || filtered[0] != results[0] {
		t.Fatalf("unexpected filtering: %#v", filtered)
	}
}
