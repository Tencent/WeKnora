package chatpipeline

import (
	"context"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRecallWeightApplierAppliesSearchResultsBeforeNextWhenNoRerankModel(t *testing.T) {
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
	nextCalled := false
	err := applier.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		nextCalled = true
		if got := cm.SearchResult[0].ID; got != "boosted" {
			t.Fatalf("first result seen by downstream plugin = %q, want boosted", got)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if !nextCalled {
		t.Fatal("next plugin was not called")
	}
	if got := cm.SearchResult[0].Score; math.Abs(got-0.90) > 0.0001 {
		t.Fatalf("boosted score = %v, want 0.90", got)
	}
	if got := cm.SearchResult[0].Metadata["recall_weight"]; got != "1.50" {
		t.Fatalf("metadata recall_weight = %q, want 1.50", got)
	}
}

func TestRecallWeightApplierSkipsWhenRerankModelConfigured(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankModelID: "rerank-model"},
		PipelineState: types.PipelineState{
			Intent: types.IntentKBSearch,
			SearchResult: []*types.SearchResult{
				{ID: "neutral", Score: 0.80, RecallWeight: 1.0},
				{ID: "boosted", Score: 0.60, RecallWeight: 1.5},
			},
		},
	}

	applier := &RecallWeightApplier{}
	nextCalled := false
	err := applier.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
		nextCalled = true
		return nil
	})

	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if !nextCalled {
		t.Fatal("next plugin was not called")
	}
	if got := cm.SearchResult[0].ID; got != "neutral" {
		t.Fatalf("first result = %q, want neutral before model rerank", got)
	}
	if got := cm.SearchResult[1].Metadata["recall_weight"]; got != "" {
		t.Fatalf("recall metadata = %q, want empty before model rerank", got)
	}
}

func TestApplyRecallWeightToRerankScore(t *testing.T) {
	result := &types.SearchResult{
		ID:           "high-quality",
		Score:        0.60,
		RecallWeight: 1.5,
	}

	applyRecallWeightToRerankScore(result)

	if got := result.Score; math.Abs(got-0.90) > 0.0001 {
		t.Fatalf("rerank weighted score = %v, want 0.90", got)
	}
	if got := result.Metadata["recall_weight"]; got != "1.50" {
		t.Fatalf("metadata recall_weight = %q, want 1.50", got)
	}
	if got := result.Metadata["recall_weight_original_score"]; got != "0.6000" {
		t.Fatalf("metadata original score = %q, want 0.6000", got)
	}
}
