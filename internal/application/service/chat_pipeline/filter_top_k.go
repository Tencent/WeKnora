package chatpipeline

import (
	"context"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/application/feedbackweight"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginFilterTopK is a plugin that filters search results to keep only the top K items
type PluginFilterTopK struct {
	feedbackConfig *config.FeedbackConfig
	feedbackRepo   interfaces.FeedbackRepository
}

// NewPluginFilterTopK creates a new instance of PluginFilterTopK and registers it with the event manager
func NewPluginFilterTopK(
	eventManager *EventManager,
	cfg *config.Config,
	feedbackRepo interfaces.FeedbackRepository,
) *PluginFilterTopK {
	res := &PluginFilterTopK{feedbackRepo: feedbackRepo}
	if cfg != nil {
		res.feedbackConfig = cfg.Feedback
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types that this plugin responds to
func (p *PluginFilterTopK) ActivationEvents() []types.EventType {
	return []types.EventType{types.FILTER_TOP_K}
}

// OnEvent handles the FILTER_TOP_K event by filtering results to keep only the top K items
// It can filter MergeResult, RerankResult, or SearchResult depending on which is available
func (p *PluginFilterTopK) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if !chatManage.NeedsRetrieval() {
		return next()
	}
	pipelineInfo(ctx, "FilterTopK", "input", map[string]interface{}{
		"session_id": chatManage.SessionID,
		"top_k":      chatManage.RerankTopK,
		"merge_cnt":  len(chatManage.MergeResult),
		"rerank_cnt": len(chatManage.RerankResult),
		"search_cnt": len(chatManage.SearchResult),
	})

	filterTopK := func(searchResult []*types.SearchResult, topK int) []*types.SearchResult {
		// Preserve the upstream relevance contract before feedback is considered.
		// The feedback policy uses this deterministic order as its stable
		// tie-breaker and returns it unchanged for every disabled/fail-open path.
		sortSearchResultsDeterministically(searchResult)
		searchResult = p.applyFeedbackWeights(ctx, chatManage, searchResult, topK)
		if topK > 0 && len(searchResult) > topK {
			pipelineInfo(ctx, "FilterTopK", "filter", map[string]interface{}{
				"before": len(searchResult),
				"after":  topK,
			})
			searchResult = searchResult[:topK]
		}
		return searchResult
	}

	if len(chatManage.MergeResult) > 0 {
		chatManage.MergeResult = filterTopK(chatManage.MergeResult, chatManage.RerankTopK)
	} else if len(chatManage.RerankResult) > 0 {
		chatManage.RerankResult = filterTopK(chatManage.RerankResult, chatManage.RerankTopK)
	} else if len(chatManage.SearchResult) > 0 {
		chatManage.SearchResult = filterTopK(chatManage.SearchResult, chatManage.RerankTopK)
	} else {
		pipelineWarn(ctx, "FilterTopK", "skip", map[string]interface{}{
			"reason": "no_results",
		})
	}

	pipelineInfo(ctx, "FilterTopK", "output", map[string]interface{}{
		"merge_cnt":  len(chatManage.MergeResult),
		"rerank_cnt": len(chatManage.RerankResult),
		"search_cnt": len(chatManage.SearchResult),
	})
	return next()
}

func (p *PluginFilterTopK) applyFeedbackWeights(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
	topK int,
) []*types.SearchResult {
	candidates := make([]feedbackweight.Candidate, len(results))
	for i, result := range results {
		if result == nil {
			candidates[i] = feedbackweight.Candidate{OriginalIndex: i}
			continue
		}
		tenantID, optIn := feedbackScopeForResult(chatManage.SearchTargets, result)
		candidates[i] = feedbackweight.Candidate{
			TenantID: tenantID, KnowledgeBaseID: result.KnowledgeBaseID, ChunkID: result.ID,
			Score: result.Score, OriginalIndex: i, WorkspaceOptIn: optIn,
			AlreadyApplied: result.FeedbackWeightApplied,
		}
	}
	started := time.Now()
	outcome := feedbackweight.Apply(ctx, p.feedbackConfig, p.feedbackRepo, candidates, topK)
	summary := outcome.LogSummary(p.feedbackConfig)
	pipelineInfo(ctx, "FilterTopK", "feedback_weight", map[string]interface{}{
		"candidate_count":         len(candidates),
		"duration_ms":             time.Since(started).Milliseconds(),
		"reason":                  outcome.Reason,
		"changed_order":           outcome.ChangedOrder,
		"topk_changed":            outcome.TopKChanged,
		"policy":                  p.feedbackConfig.PolicyFingerprint(),
		"minimum_sample_count":    summary.MinimumSampleCount,
		"high_rate_threshold":     summary.HighThreshold,
		"low_rate_threshold":      summary.LowThreshold,
		"high_recall_weight":      summary.HighWeight,
		"normal_recall_weight":    summary.NormalWeight,
		"low_recall_weight":       summary.LowWeight,
		"stored_weight_min":       summary.StoredWeightMin,
		"stored_weight_max":       summary.StoredWeightMax,
		"effective_weight_min":    summary.EffectiveWeightMin,
		"effective_weight_max":    summary.EffectiveWeightMax,
		"high_candidate_count":    summary.HighCandidates,
		"normal_candidate_count":  summary.NormalCandidates,
		"low_candidate_count":     summary.LowCandidates,
		"neutral_candidate_count": summary.NeutralCandidates,
	})
	if !outcome.Applied {
		return results
	}
	weighted := make([]*types.SearchResult, 0, len(results))
	for _, candidate := range outcome.Candidates {
		if candidate.OriginalIndex < 0 || candidate.OriginalIndex >= len(results) {
			return results
		}
		result := results[candidate.OriginalIndex]
		if result != nil {
			result.StoredRecallWeight = candidate.StoredRecallWeight
			result.EffectiveRecallWeight = candidate.EffectiveRecallWeight
			result.FeedbackWeightApplied = true
		}
		weighted = append(weighted, result)
	}
	return weighted
}

func feedbackScopeForResult(
	targets types.SearchTargets,
	result *types.SearchResult,
) (uint64, bool) {
	if result == nil || result.KnowledgeBaseID == "" {
		return 0, false
	}
	for _, target := range targets {
		if target == nil || target.KnowledgeBaseID != result.KnowledgeBaseID {
			continue
		}
		return target.TenantID, target.FeedbackRetrievalWeightEnabled
	}
	return 0, false
}

// sortSearchResultsDeterministically restores relevance order after merge
// stages group results through maps. It is intentionally separate from
// feedback weighting so disabled policy never introduces a new reorder.
func sortSearchResultsDeterministically(results []*types.SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return searchResultLess(results[i], results[j])
	})
}

func searchResultLess(left, right *types.SearchResult) bool {
	if left == nil || right == nil {
		return left != nil
	}
	if left.Score != right.Score {
		return left.Score > right.Score
	}
	if left.KnowledgeID != right.KnowledgeID {
		return left.KnowledgeID < right.KnowledgeID
	}
	if left.ChunkType != right.ChunkType {
		return left.ChunkType < right.ChunkType
	}
	if left.ChunkIndex != right.ChunkIndex {
		return left.ChunkIndex < right.ChunkIndex
	}
	return left.ID < right.ID
}
