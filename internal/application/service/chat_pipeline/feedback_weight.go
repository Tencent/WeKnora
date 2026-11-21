package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/service/feedbackweight"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginFeedbackWeight scales reranked chunks by their approval-rate recall weight.
// It runs in the same CHUNK_RERANK phase as the other score adjustments and
// calls next first so the model rerank has already written RerankResult.
type PluginFeedbackWeight struct {
	chunks interfaces.ChunkRepository
}

// NewPluginFeedbackWeight registers the recall-weight adjustment.
func NewPluginFeedbackWeight(eventManager *EventManager, chunks interfaces.ChunkRepository) *PluginFeedbackWeight {
	plugin := &PluginFeedbackWeight{chunks: chunks}
	eventManager.Register(plugin)
	return plugin
}

// ActivationEvents returns the rerank phase.
func (p *PluginFeedbackWeight) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent multiplies reranked scores by each chunk's recall weight.
func (p *PluginFeedbackWeight) OnEvent(
	ctx context.Context,
	_ types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	if err := next(); err != nil {
		return err
	}
	if p.chunks == nil || chatManage == nil || len(chatManage.RerankResult) == 0 {
		return nil
	}
	ids := make([]string, 0, len(chatManage.RerankResult))
	seen := map[string]struct{}{}
	for _, result := range chatManage.RerankResult {
		if !types.AttributableKnowledgeRef(result) {
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
	chunks, err := p.chunks.ListChunksByIDOnly(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "FeedbackWeight: failed to load chunk weights: %v", err)
		return nil
	}
	adjusted := feedbackweight.Apply(chatManage.RerankResult, feedbackweight.WeightsByID(chunks))
	if adjusted > 0 {
		logger.Infof(ctx, "FeedbackWeight: adjusted %d chunks by recall weight", adjusted)
	}
	return nil
}
