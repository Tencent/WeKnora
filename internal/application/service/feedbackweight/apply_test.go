package feedbackweight

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyOrdersByRecallWeight(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "low", KnowledgeID: "k", Score: 0.9, MatchType: types.MatchTypeEmbedding},
		{ID: "high", KnowledgeID: "k", Score: 0.8, MatchType: types.MatchTypeEmbedding},
		{ID: "web", KnowledgeID: "k", Score: 0.95, MatchType: types.MatchTypeWebSearch},
	}
	adjusted := Apply(results, map[string]float64{"low": 0.8, "high": 1.2, "web": 0.1})
	if adjusted != 2 {
		t.Fatalf("adjusted = %d, want 2", adjusted)
	}
	if results[0].ID != "high" || results[1].ID != "web" || results[2].ID != "low" {
		t.Fatalf("order = %s %s %s", results[0].ID, results[1].ID, results[2].ID)
	}
	if results[1].Score != 0.95 {
		t.Fatalf("web score changed to %v", results[1].Score)
	}
	if results[0].Metadata["feedback_recall_weight"] != "1.2000" {
		t.Fatalf("metadata = %+v", results[0].Metadata)
	}
}

func TestApplyTreatsZeroWeightAsNeutral(t *testing.T) {
	results := []*types.SearchResult{{ID: "c", KnowledgeID: "k", Score: 0.5}}
	if adjusted := Apply(results, map[string]float64{"c": 0}); adjusted != 0 || results[0].Score != 0.5 {
		t.Fatalf("zero weight adjusted=%d score=%v", adjusted, results[0].Score)
	}
}
