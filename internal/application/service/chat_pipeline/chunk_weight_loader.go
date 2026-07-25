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

	chunkIDs := make([]string, 0, len(chatManage.SearchResult))
	for _, result := range chatManage.SearchResult {
		if result.ID != "" {
			chunkIDs = append(chunkIDs, result.ID)
		}
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
			"total":      len(chatManage.SearchResult),
		})
	}

	return next()
}
