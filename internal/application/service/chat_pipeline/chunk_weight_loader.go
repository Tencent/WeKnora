package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ChunkWeightLoader loads persisted recall weights before rerank/filter stages.
type ChunkWeightLoader struct {
	chunkRepo interfaces.ChunkRepository
}

func NewChunkWeightLoader(
	eventManager *EventManager,
	chunkRepo interfaces.ChunkRepository,
) *ChunkWeightLoader {
	loader := &ChunkWeightLoader{
		chunkRepo: chunkRepo,
	}
	eventManager.Register(loader)
	return loader
}

func (p *ChunkWeightLoader) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

func (p *ChunkWeightLoader) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if len(chatManage.SearchResult) == 0 {
		return next()
	}

	// HybridSearch now hydrates RecallWeight with the chunk row. Keep this
	// loader for legacy/directly-constructed results, but avoid a duplicate DB
	// query when every result already carries a persisted weight.
	chunkIDs := make([]string, 0, len(chatManage.SearchResult))
	for _, result := range chatManage.SearchResult {
		if result != nil && result.ID != "" && result.RecallWeight == 0 {
			chunkIDs = append(chunkIDs, result.ID)
		}
	}
	if len(chunkIDs) == 0 {
		return next()
	}

	chunks, err := p.chunkRepo.ListChunksByIDOnly(ctx, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to load chunk weights: %v", err)
		return next()
	}

	weightMap := make(map[string]float64, len(chunks))
	for _, chunk := range chunks {
		weightMap[chunk.ID] = chunk.RecallWeight
	}

	loaded := 0
	for _, result := range chatManage.SearchResult {
		if result == nil || result.RecallWeight != 0 {
			continue
		}
		if weight, ok := weightMap[result.ID]; ok {
			result.RecallWeight = weight
			loaded++
		} else {
			result.RecallWeight = 1.0
		}
	}

	if loaded > 0 {
		pipelineInfo(ctx, "ChunkWeightLoader", "loaded", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"loaded":     loaded,
			"requested":  len(chunkIDs),
		})
	}

	return next()
}
