package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyChunkRecallWeightsReordersHits(t *testing.T) {
	candidates := []*types.IndexWithScore{
		{ChunkID: "low", Score: 0.50},
		{ChunkID: "high", Score: 0.40},
	}
	chunks := map[string]*types.Chunk{
		"low":  {ID: "low", RecallWeight: 0.8},
		"high": {ID: "high", RecallWeight: 1.2},
	}
	applyChunkRecallWeights(context.Background(), candidates, chunks)
	if candidates[0].ChunkID != "high" {
		t.Fatalf("order = %s, %s", candidates[0].ChunkID, candidates[1].ChunkID)
	}
	if candidates[0].Score != 0.48 || candidates[1].Score != 0.4 {
		t.Fatalf("scores = %v, %v", candidates[0].Score, candidates[1].Score)
	}
}

func TestApplyChunkRecallWeightsLeavesNeutralOrder(t *testing.T) {
	candidates := []*types.IndexWithScore{
		{ChunkID: "a", Score: 0.2},
		{ChunkID: "b", Score: 0.2},
	}
	chunks := map[string]*types.Chunk{
		"a": {ID: "a", RecallWeight: 0},
		"b": {ID: "b", RecallWeight: 1},
	}
	applyChunkRecallWeights(context.Background(), candidates, chunks)
	if candidates[0].ChunkID != "a" || candidates[1].ChunkID != "b" {
		t.Fatalf("neutral weights changed order: %s, %s", candidates[0].ChunkID, candidates[1].ChunkID)
	}
}

func TestApplyChunkRecallWeightsHonorsDisabledPolicy(t *testing.T) {
	cfg := types.DefaultChunkFeedbackConfig()
	cfg.Enabled = false
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{
		RetrievalConfig: &types.RetrievalConfig{ChunkFeedback: &cfg},
	})
	candidates := []*types.IndexWithScore{{ChunkID: "a", Score: 0.5}}
	chunks := map[string]*types.Chunk{"a": {ID: "a", RecallWeight: 1.2}}
	applyChunkRecallWeights(ctx, candidates, chunks)
	if candidates[0].Score != 0.5 {
		t.Fatalf("disabled policy changed score to %v", candidates[0].Score)
	}
}
