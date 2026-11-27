package service

import (
	"context"
	"slices"

	"github.com/Tencent/WeKnora/internal/types"
)

// applyChunkRecallWeights multiplies each hit's score by the chunk's stored
// recall weight and re-sorts when any weight is not neutral. A weight of 0
// is treated as 1 so chunks written before feedback existed stay in place.
// Ranking stays off when the tenant has disabled the policy.
func applyChunkRecallWeights(
	ctx context.Context,
	candidates []*types.IndexWithScore,
	chunks map[string]*types.Chunk,
) {
	if len(candidates) == 0 || len(chunks) == 0 || !chunkFeedbackRankingEnabled(ctx) {
		return
	}
	changed := false
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		chunk := chunks[candidate.ChunkID]
		if chunk == nil {
			continue
		}
		weight := types.EffectiveRecallWeight(chunk.RecallWeight)
		if weight == 1 {
			continue
		}
		candidate.Score *= weight
		changed = true
	}
	if changed {
		slices.SortFunc(candidates, sortByScoreDesc)
	}
}

func chunkFeedbackRankingEnabled(ctx context.Context) bool {
	info, ok := types.TenantInfoFromContext(ctx)
	if !ok || info == nil {
		return true
	}
	return info.RetrievalConfig.ChunkFeedbackRankingEnabled()
}
