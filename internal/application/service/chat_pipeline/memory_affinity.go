package chatpipeline

import (
	"context"
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Affinity boost bounds.
//
// The multiplier is capped well below the wiki boost because the signal is
// weaker: a document appearing in past answers means the retriever kept picking
// it, not that the user found it useful. The boost is meant to break ties
// between comparable passages, never to drag an irrelevant document to the top
// of a question it has nothing to do with.
const (
	affinityMaxBoost  = 1.15
	affinityFullHits  = 8.0
	affinityMinHits   = types.MemoryDocAffinityMinHits
	affinityMaxLookup = 200
)

// PluginMemoryAffinity prefers documents this person's answers keep drawing on.
//
// This exists because personalising the answer prompt while retrieving exactly
// the same passages for everyone is the shallow half of a memory feature. In a
// knowledge-base product the durable per-person signal is which material they
// actually work from, and the reranker is where it belongs.
//
// The table it reads is written by the same feature that reads it. The previous
// attempt at this shipped an anchor table with no consumer; the rule since is
// that a per-person retrieval signal ships with the code that uses it.
type PluginMemoryAffinity struct {
	memoryService interfaces.MemoryService
}

// NewPluginMemoryAffinity creates and registers the affinity rerank plugin.
func NewPluginMemoryAffinity(
	eventManager *EventManager, memoryService interfaces.MemoryService,
) *PluginMemoryAffinity {
	p := &PluginMemoryAffinity{memoryService: memoryService}
	eventManager.Register(p)
	return p
}

// ActivationEvents returns the event types this plugin handles.
func (p *PluginMemoryAffinity) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent applies the per-person document boost after reranking.
func (p *PluginMemoryAffinity) OnEvent(
	ctx context.Context,
	eventType types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	if err := next(); err != nil {
		return err
	}
	if p.memoryService == nil || len(chatManage.RerankResult) == 0 {
		return nil
	}

	ids := make([]string, 0, len(chatManage.RerankResult))
	seen := make(map[string]struct{}, len(chatManage.RerankResult))
	for i := range chatManage.RerankResult {
		id := chatManage.RerankResult[i].KnowledgeID
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) >= affinityMaxLookup {
			break
		}
	}
	if len(ids) == 0 {
		return nil
	}

	affinity := p.memoryService.DocumentAffinity(ctx, ids)
	if len(affinity) == 0 {
		return nil
	}

	boosted := 0
	for i := range chatManage.RerankResult {
		hits := affinity[chatManage.RerankResult[i].KnowledgeID]
		if hits < affinityMinHits {
			continue
		}
		chatManage.RerankResult[i].Score *= affinityFactor(hits)
		boosted++
	}
	if boosted == 0 {
		return nil
	}

	sort.SliceStable(chatManage.RerankResult, func(i, j int) bool {
		return chatManage.RerankResult[i].Score > chatManage.RerankResult[j].Score
	})
	logger.Infof(ctx, "MemoryAffinity: boosted %d chunks from familiar documents", boosted)
	return nil
}

// affinityFactor grows with use and saturates.
//
// The curve is logarithmic so the tenth reuse of a document counts for far less
// than the second: familiarity should be a nudge that compounds slowly, not a
// feedback loop that locks a person into the first document they ever opened.
func affinityFactor(hits int) float64 {
	if hits < affinityMinHits {
		return 1
	}
	ratio := math.Log1p(float64(hits)) / math.Log1p(affinityFullHits)
	if ratio > 1 {
		ratio = 1
	}
	return 1 + (affinityMaxBoost-1)*ratio
}
