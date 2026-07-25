package chatpipeline

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

type RecallWeightApplier struct{}

func NewRecallWeightApplier(eventManager *EventManager) *RecallWeightApplier {
	applier := &RecallWeightApplier{}
	eventManager.Register(applier)
	return applier
}

func (p *RecallWeightApplier) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

func (p *RecallWeightApplier) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if !chatManage.NeedsRetrieval() {
		return next()
	}
	if chatManage.RerankModelID != "" {
		return next()
	}

	applied := applyRecallWeightsToResults(chatManage.SearchResult)
	if applied > 0 {
		pipelineInfo(ctx, "RecallWeightApplier", "applied", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"result_set": "search",
			"applied":    applied,
		})
	}
	return next()
}

func applyRecallWeightsToResults(results []*types.SearchResult) int {
	applied := 0
	for _, result := range results {
		if result == nil || result.RecallWeight == 0 || result.RecallWeight == 1.0 {
			continue
		}
		originalScore := result.Score
		result.Score *= result.RecallWeight
		result.Metadata = ensureMetadata(result.Metadata)
		result.Metadata["recall_weight"] = fmt.Sprintf("%.2f", result.RecallWeight)
		result.Metadata["recall_weight_original_score"] = fmt.Sprintf("%.4f", originalScore)
		result.Metadata["recall_weighted_score"] = fmt.Sprintf("%.4f", result.Score)
		applied++
	}
	if applied > 0 {
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})
	}
	return applied
}
