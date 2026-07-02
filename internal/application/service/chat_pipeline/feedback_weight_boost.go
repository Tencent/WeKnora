package chatpipeline

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginFeedbackWeightBoost applies chunk-level feedback recall weights after rerank.
type PluginFeedbackWeightBoost struct {
	chunkRepo interfaces.ChunkRepository
}

func NewPluginFeedbackWeightBoost(
	eventManager *EventManager,
	chunkRepo interfaces.ChunkRepository,
) *PluginFeedbackWeightBoost {
	p := &PluginFeedbackWeightBoost{chunkRepo: chunkRepo}
	eventManager.Register(p)
	return p
}

func (p *PluginFeedbackWeightBoost) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

func (p *PluginFeedbackWeightBoost) OnEvent(
	ctx context.Context,
	eventType types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	if err := next(); err != nil {
		return err
	}
	if len(chatManage.RerankResult) == 0 {
		return nil
	}

	ids := make([]string, 0, len(chatManage.RerankResult))
	seen := make(map[string]struct{}, len(chatManage.RerankResult))
	for _, result := range chatManage.RerankResult {
		if result == nil || result.ID == "" || !isFeedbackWeightCandidate(result) {
			continue
		}
		if _, ok := seen[result.ID]; ok {
			continue
		}
		seen[result.ID] = struct{}{}
		ids = append(ids, result.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	chunks, err := p.chunkRepo.ListChunksByIDOnly(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "FeedbackWeightBoost: failed to load chunk weights: %v", err)
		return nil
	}
	weights := make(map[string]float64, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		weight := chunk.RecallWeight
		if weight == 0 {
			weight = 1.0
		}
		weights[chunk.ID] = weight
	}

	boosted := 0
	for _, result := range chatManage.RerankResult {
		if result == nil {
			continue
		}
		weight, ok := weights[result.ID]
		if !ok || weight == 1.0 {
			continue
		}
		result.Metadata = ensureMetadata(result.Metadata)
		result.Metadata["feedback_recall_weight"] = fmt.Sprintf("%.4f", weight)
		result.Score *= weight
		boosted++
	}
	if boosted == 0 {
		return nil
	}
	sort.SliceStable(chatManage.RerankResult, func(i, j int) bool {
		return chatManage.RerankResult[i].Score > chatManage.RerankResult[j].Score
	})
	logger.Infof(ctx, "FeedbackWeightBoost: adjusted %d chunks by recall weight", boosted)
	return nil
}

func isFeedbackWeightCandidate(result *types.SearchResult) bool {
	if result.MatchType == types.MatchTypeHistory || result.MatchType == types.MatchTypeWebSearch {
		return false
	}
	if result.KnowledgeSource == "web_search" {
		return false
	}
	return true
}
