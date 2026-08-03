// Package chatpipeline
package chatpipeline

import (
	"context"
	"strconv"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginFeedbackWeight multiplies each candidate's retrieval score by the
// chunk's stored RecallWeight after CHUNK_RERANK. RecallWeight is maintained
// by the answer-feedback path (#1248) on every rating mutation and recomputed
// wholesale when the tenant's RetrievalConfig.FeedbackRankingEnabled flag
// changes.
//
// The weight is applied between rerank and FILTER_TOP_K so the effective
// ordering on entering the LLM context window reflects user-rejection signals
// from earlier turns, while still keeping the rerank model in charge of the
// per-result semantics. The plugin is a no-op when feedback ranking is
// disabled or when no candidate has a non-neutral weight — this preserves
// the historical ordering exactly for tenants that never opt in.
type PluginFeedbackWeight struct {
	chunkRepo ChunkWeightLookup
}

// ChunkWeightLookup is the minimal chunk-side contract the feedback-weight
// plugin needs from the chunk repository. Defining a narrow interface here
// keeps the chat-pipeline package independent of the full repository surface,
// and exporting it allows the DI container (dig) to resolve an implementation
// from the providers registered in internal/container.
// ChunkWeightLookup lets the plugin pull RecallWeight for a batch of chunk
// IDs without depending on the full ChunkRepository type. Tests inject a
// stub.
type ChunkWeightLookup interface {
	ListChunkRecallWeights(ctx context.Context, tenantID uint64, chunkIDs []string) (map[string]float64, error)
}

// NewPluginFeedbackWeight wires the plugin into the event manager. It is safe
// to register the plugin even when feedback weights are not initialized —
// the OnEvent handler returns early in that case, so a partial setup only
// disables the feature instead of breaking the pipeline.
func NewPluginFeedbackWeight(
	eventManager *EventManager,
	chunkRepo ChunkWeightLookup,
) *PluginFeedbackWeight {
	res := &PluginFeedbackWeight{chunkRepo: chunkRepo}
	eventManager.Register(res)
	return res
}

// ActivationEvents runs the plugin right after CHUNK_RERANK so weights
// apply before FILTER_TOP_K, while also being available between CHUNK_MERGE
// and INTO_CHAT_MESSAGE so the merge branch (which bypasses rerank entirely)
// still benefits from feedback data. Both branches meet before
// into-chat-message, so we attach to one shared event type that is emitted
// in the same spot of either branch.
func (p *PluginFeedbackWeight) ActivationEvents() []types.EventType {
	return []types.EventType{types.FEEDBACK_WEIGHT}
}

// OnEvent applies RecallWeight to the active candidate list. The plugin runs
// against RerankResult, MergeResult or SearchResult in that priority order —
// the same preference used by FilterTopK so the downstream truncation sees
// the same set we just re-weighted.
func (p *PluginFeedbackWeight) OnEvent(
	ctx context.Context,
	eventType types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	_ = eventType
	if !chatManage.NeedsRetrieval() {
		return next()
	}

	candidates := activeCandidates(chatManage)
	if len(candidates) == 0 {
		return next()
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	if !feedbackRankingEnabledForTenant(ctx, tenantID) {
		// Feature off → original ordering, identical skip-no-op semantics
		// so we don't touch logs / counters.
		return next()
	}

	chunkIDs := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if c == nil || c.ID == "" {
			continue
		}
		if _, ok := seen[c.ID]; ok {
			continue
		}
		seen[c.ID] = struct{}{}
		chunkIDs = append(chunkIDs, c.ID)
		// Sub-chunks inherit the parent passage weight — see extractChunkRefs
		// in the feedback service. We do not double-key the parent here:
		// the parent ID itself will appear in candidates if the merge
		// pipeline folded them, so it gets its own weight.
		for _, sub := range c.SubChunkID {
			if sub == "" {
				continue
			}
			if _, ok := seen[sub]; ok {
				continue
			}
			seen[sub] = struct{}{}
			chunkIDs = append(chunkIDs, sub)
		}
	}
	if len(chunkIDs) == 0 {
		return next()
	}

	weights, err := p.chunkRepo.ListChunkRecallWeights(ctx, tenantID, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "recall weight lookup failed, feedback ranking skipped: %v", err)
		return next()
	}

	adjusted := 0
	for _, c := range candidates {
		if c == nil {
			continue
		}
		w := lookupRecallWeight(weights, c.ID, c.SubChunkID)
		if w == 1.0 {
			continue
		}
		c.Score *= w
		if c.Metadata == nil {
			c.Metadata = map[string]string{}
		}
		c.Metadata["feedback_weight"] = strconv.FormatFloat(w, 'f', 4, 64)
		adjusted++
	}

	if adjusted > 0 {
		// The score mutation can break the deterministic tie-breakers that
		// FilterTopK relies on, so re-sort in the global relevance order
		// before the next stage consumes the candidate list.
		sortSearchResultsDeterministically(candidates)
		pipelineInfo(ctx, "FeedbackWeight", "applied", map[string]interface{}{
			"adjusted":     adjusted,
			"total":        len(candidates),
			"distinct_ids": len(chunkIDs),
		})
	} else {
		pipelineInfo(ctx, "FeedbackWeight", "noop", map[string]interface{}{
			"total":        len(candidates),
			"distinct_ids": len(chunkIDs),
		})
	}
	return next()
}

// activeCandidates returns the currently populated candidate list using the
// same priority order FilterTopK follows, so a plugin that mutates scores
// earlier in the pipeline cannot accidentally pick a different list than the
// later FilterTopK stage would.
func activeCandidates(chatManage *types.ChatManage) []*types.SearchResult {
	switch {
	case len(chatManage.MergeResult) > 0:
		return chatManage.MergeResult
	case len(chatManage.RerankResult) > 0:
		return chatManage.RerankResult
	case len(chatManage.SearchResult) > 0:
		return chatManage.SearchResult
	default:
		return nil
	}
}

// feedbackRankingEnabledForTenant reads the tenant's RetrievalConfig directly
// from the request context. The middleware populates it on every authenticated
// request so this avoids touching the tenant service from a hot pipeline
// path. Missing tenant / missing config are both treated as "off" — opting in
// is an explicit save through the retrieval-config endpoint, so the default
// behaviour matches existing tenants.
func feedbackRankingEnabledForTenant(ctx context.Context, _ uint64) bool {
	tenant, ok := types.TenantInfoFromContext(ctx)
	if !ok || tenant == nil || tenant.RetrievalConfig == nil {
		return false
	}
	return tenant.RetrievalConfig.GetEffectiveFeedbackRankingEnabled()
}

// lookupRecallWeight returns the weight for a candidate. If the parent chunk
// has no record but one of its sub-chunks does (or vice versa) we average —
// both should usually agree because the feedback service writes one row per
// chunk and the score span is bounded (default: 0.8..1.2). Falls back to 1.0
// when nothing is found.
func lookupRecallWeight(weights map[string]float64, primary string, subs []string) float64 {
	w, ok := weights[primary]
	if !ok || len(subs) == 0 {
		if !ok {
			return 1.0
		}
		return w
	}
	count := 1
	sum := w
	for _, sub := range subs {
		if v, hit := weights[sub]; hit {
			count++
			sum += v
		}
	}
	if count == 1 {
		return w
	}
	return sum / float64(count)
}
