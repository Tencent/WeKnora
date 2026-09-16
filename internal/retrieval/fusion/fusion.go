// Package fusion contains the production retrieval ranking fusion used by search and evaluation.
package fusion

import (
	"context"
	"slices"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Results combines vector and keyword rankings with Reciprocal Rank Fusion (RRF).
// A single available ranking is deduplicated by chunk and retains its original scores.
func Results(
	ctx context.Context,
	vectorResults, keywordResults []*types.IndexWithScore,
	retrievalCfg *types.RetrievalConfig,
) []*types.IndexWithScore {
	if len(keywordResults) == 0 {
		result := deduplicateByScore(vectorResults)
		logger.Infof(ctx, "Result count after deduplication: %d", len(result))
		return result
	}
	if len(vectorResults) == 0 {
		result := deduplicateByScore(keywordResults)
		logger.Infof(ctx, "Result count after deduplication: %d", len(result))
		return result
	}
	result := withRRF(ctx, vectorResults, keywordResults, retrievalCfg)
	logger.Infof(ctx, "Result count after RRF fusion: %d", len(result))
	return result
}

func compareByScore(a, b *types.IndexWithScore) int {
	if a.Score > b.Score {
		return -1
	}
	if a.Score < b.Score {
		return 1
	}
	if a.ChunkID < b.ChunkID {
		return -1
	}
	if a.ChunkID > b.ChunkID {
		return 1
	}
	return 0
}

func deduplicateByScore(results []*types.IndexWithScore) []*types.IndexWithScore {
	byChunk := make(map[string]*types.IndexWithScore, len(results))
	for _, result := range results {
		if existing, exists := byChunk[result.ChunkID]; !exists || result.Score > existing.Score {
			byChunk[result.ChunkID] = result
		}
	}
	deduplicated := make([]*types.IndexWithScore, 0, len(byChunk))
	for _, result := range byChunk {
		deduplicated = append(deduplicated, result)
	}
	slices.SortFunc(deduplicated, compareByScore)
	return deduplicated
}

func withRRF(
	ctx context.Context,
	vectorResults, keywordResults []*types.IndexWithScore,
	retrievalCfg *types.RetrievalConfig,
) []*types.IndexWithScore {
	rrfK := retrievalCfg.GetEffectiveRRFK()
	vectorWeight, keywordWeight := retrievalCfg.GetEffectiveRRFWeights()

	vectorRanks := make(map[string]int, len(vectorResults))
	for index, result := range vectorResults {
		if _, exists := vectorRanks[result.ChunkID]; !exists {
			vectorRanks[result.ChunkID] = index + 1
		}
	}
	keywordRanks := make(map[string]int, len(keywordResults))
	for index, result := range keywordResults {
		if _, exists := keywordRanks[result.ChunkID]; !exists {
			keywordRanks[result.ChunkID] = index + 1
		}
	}

	byChunk := make(map[string]*types.IndexWithScore, len(vectorResults)+len(keywordResults))
	for _, result := range vectorResults {
		if existing, exists := byChunk[result.ChunkID]; !exists || result.Score > existing.Score {
			byChunk[result.ChunkID] = result
		}
	}
	for _, result := range keywordResults {
		if _, exists := byChunk[result.ChunkID]; !exists {
			byChunk[result.ChunkID] = result
		}
	}

	fused := make([]*types.IndexWithScore, 0, len(byChunk))
	for chunkID, result := range byChunk {
		score := 0.0
		if rank, exists := vectorRanks[chunkID]; exists {
			score += vectorWeight / float64(rrfK+rank)
		}
		if rank, exists := keywordRanks[chunkID]; exists {
			score += keywordWeight / float64(rrfK+rank)
		}
		result.Score = score
		fused = append(fused, result)
	}
	slices.SortFunc(fused, compareByScore)

	for index, result := range fused {
		if index >= 15 {
			break
		}
		vectorRank, vectorMatch := vectorRanks[result.ChunkID]
		keywordRank, keywordMatch := keywordRanks[result.ChunkID]
		logger.Debugf(
			ctx,
			"RRF rank %d: chunk_id=%s, rrf_score=%.6f, vector_rank=%v(%v), keyword_rank=%v(%v)",
			index, result.ChunkID, result.Score, vectorRank, vectorMatch, keywordRank, keywordMatch,
		)
	}
	return fused
}
