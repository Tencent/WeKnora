package chatpipeline

import (
	"context"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

// PluginFilterTopK is a plugin that filters search results to keep only the top K items
type PluginFilterTopK struct{}

// NewPluginFilterTopK creates a new instance of PluginFilterTopK and registers it with the event manager
func NewPluginFilterTopK(eventManager *EventManager) *PluginFilterTopK {
	res := &PluginFilterTopK{}
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
		sortSearchResultsDeterministically(searchResult)
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

// sortSearchResultsDeterministically restores the global relevance order after
// merge stages group results through maps. Stable tie-breakers keep identical
// requests reproducible before TopK truncation.
func sortSearchResultsDeterministically(results []*types.SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		left, right := results[i], results[j]
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
		if left.StartAt != right.StartAt {
			return left.StartAt < right.StartAt
		}
		if left.EndAt != right.EndAt {
			return left.EndAt < right.EndAt
		}
		return left.ID < right.ID
	})
}
