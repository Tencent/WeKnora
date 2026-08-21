package rerank

import (
	"encoding/json"
	"testing"
)

// The live rerank API omits `index` and only echoes document text with a
// relevance score. Regression test for the bug where every result
// unmarshaled to index 0 and collapsed onto the first candidate.
func TestMapZhipuResultsWithoutIndexField(t *testing.T) {
	raw := `{
		"results": [
			{"document": "doc-b", "relevance_score": 0.97},
			{"document": "doc-d", "relevance_score": 0.0005},
			{"document": "doc-a", "relevance_score": 0.00008}
		]
	}`
	var resp ZhipuRerankResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := mapZhipuResults(resp.Results, []string{"doc-a", "doc-b", "doc-c", "doc-d"})
	if len(results) != 3 {
		t.Fatalf("expected 3 mapped results, got %d", len(results))
	}
	wantOrder := []int{1, 3, 0}
	wantScores := []float64{0.97, 0.0005, 0.00008}
	for i, r := range results {
		if r.Index != wantOrder[i] {
			t.Errorf("result %d: index = %d, want %d", i, r.Index, wantOrder[i])
		}
		if r.RelevanceScore != wantScores[i] {
			t.Errorf("result %d: score = %v, want %v", i, r.RelevanceScore, wantScores[i])
		}
	}
}

func TestMapZhipuResultsWithIndexField(t *testing.T) {
	raw := `{"results": [
		{"index": 2, "relevance_score": 0.9},
		{"index": 0, "relevance_score": 0.4}
	]}`
	var resp ZhipuRerankResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := mapZhipuResults(resp.Results, []string{"doc-a", "doc-b", "doc-c"})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Index != 2 || results[1].Index != 0 {
		t.Errorf("indexes = [%d %d], want [2 0]", results[0].Index, results[1].Index)
	}
}

// Duplicate documents in the request must not produce duplicate candidate
// mappings; the highest-scored (first) occurrence wins.
func TestMapZhipuResultsDeduplicatesCandidates(t *testing.T) {
	raw := `{"results": [
		{"document": "dup", "relevance_score": 0.9},
		{"document": "dup", "relevance_score": 0.5},
		{"document": "uniq", "relevance_score": 0.3}
	]}`
	var resp ZhipuRerankResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := mapZhipuResults(resp.Results, []string{"dup", "uniq", "dup"})
	if len(results) != 2 {
		t.Fatalf("expected 2 results after dedup, got %d", len(results))
	}
	if results[0].Index != 0 || results[0].RelevanceScore != 0.9 {
		t.Errorf("first result = index %d score %v, want index 0 score 0.9", results[0].Index, results[0].RelevanceScore)
	}
	if results[1].Index != 1 {
		t.Errorf("second result index = %d, want 1", results[1].Index)
	}
}

// Unmatchable results (no index, unknown text) are dropped instead of
// being assigned a guessed position.
func TestMapZhipuResultsDropsUnmatchable(t *testing.T) {
	raw := `{"results": [
		{"document": "doc-a", "relevance_score": 0.9},
		{"document": "unknown-text", "relevance_score": 0.8},
		{"index": 99, "document": "out-of-range", "relevance_score": 0.7}
	]}`
	var resp ZhipuRerankResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := mapZhipuResults(resp.Results, []string{"doc-a", "doc-b"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Index != 0 {
		t.Errorf("index = %d, want 0", results[0].Index)
	}
}
