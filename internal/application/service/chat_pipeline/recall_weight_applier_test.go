package chatpipeline

import (
	"context"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRecallWeightApplierAppliesLoadedWeightsAfterNext(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			Intent: types.IntentKBSearch,
			RerankResult: []*types.SearchResult{
				{ID: "high-quality", Score: 0.60},
				{ID: "low-quality", Score: 0.90},
			},
		},
	}

	applier := &RecallWeightApplier{}
	err := applier.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		cm.RerankResult[0].RecallWeight = 1.5
		cm.RerankResult[1].RecallWeight = 0.5
		return nil
	})

	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if got := cm.RerankResult[0].ID; got != "high-quality" {
		t.Fatalf("first result = %q, want high-quality", got)
	}
	if got := cm.RerankResult[0].Score; math.Abs(got-0.90) > 0.0001 {
		t.Fatalf("boosted score = %v, want 0.90", got)
	}
	if got := cm.RerankResult[1].Score; math.Abs(got-0.45) > 0.0001 {
		t.Fatalf("penalized score = %v, want 0.45", got)
	}
	if got := cm.RerankResult[0].Metadata["recall_weight"]; got != "1.50" {
		t.Fatalf("metadata recall_weight = %q, want 1.50", got)
	}
}

func TestRecallWeightApplierUsesSearchResultsWhenRerankIsEmpty(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			Intent: types.IntentKBSearch,
			SearchResult: []*types.SearchResult{
				{ID: "neutral", Score: 0.80, RecallWeight: 1.0},
				{ID: "boosted", Score: 0.60, RecallWeight: 1.5},
			},
		},
	}

	applier := &RecallWeightApplier{}
	if err := applier.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return nil }); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	if got := cm.SearchResult[0].ID; got != "boosted" {
		t.Fatalf("first fallback result = %q, want boosted", got)
	}
}
