package tools

import "github.com/Tencent/WeKnora/internal/types"

func feedbackScopeForKB(targets types.SearchTargets, knowledgeBaseID string) (uint64, bool) {
	if knowledgeBaseID == "" {
		return 0, false
	}
	for _, target := range targets {
		if target == nil || target.KnowledgeBaseID != knowledgeBaseID {
			continue
		}
		return target.TenantID, target.FeedbackRetrievalWeightEnabled
	}
	return 0, false
}

func feedbackAdjustedSearchScore(result *types.SearchResult) float64 {
	if result == nil || !result.FeedbackWeightApplied || result.EffectiveRecallWeight <= 0 {
		if result == nil {
			return 0
		}
		return result.Score
	}
	return result.Score * result.EffectiveRecallWeight
}

func feedbackAdjustedChunkScore(chunk *types.Chunk, score float64) float64 {
	if chunk == nil || !chunk.FeedbackWeightApplied || chunk.EffectiveRecallWeight <= 0 {
		return score
	}
	return score * chunk.EffectiveRecallWeight
}
