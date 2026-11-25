package chatpipeline

import (
	"context"
	"sort"

	"github.com/Tencent/WeKnora/internal/searchutil"
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
		// Collapse before the cut: sibling children of one parent must not
		// consume several slots and push other pages out of the top-k.
		if chatManage.ParentSlotAggregation {
			before := len(searchResult)
			searchResult = collapseParentSlots(searchResult)
			if len(searchResult) != before {
				pipelineInfo(ctx, "FilterTopK", "parent_slot_aggregation", map[string]interface{}{
					"before": before,
					"after":  len(searchResult),
				})
			}
		}
		sortSearchResultsDeterministically(searchResult)
		if topK > 0 && len(searchResult) > topK {
			// Results the rerank stage kept outside its top-k (pictorial
			// images a text reranker could not judge) ride along.
			filtered := searchutil.TopKKeepingKept(searchResult, topK)
			pipelineInfo(ctx, "FilterTopK", "filter", map[string]interface{}{
				"before": len(searchResult),
				"after":  len(filtered),
			})
			if len(filtered) < len(searchResult) {
				chatManage.RecordRetrievalCut(types.RetrievalStageFilterTopK, len(searchResult))
			}
			searchResult = filtered
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
		if left.ChunkIndex != right.ChunkIndex {
			return left.ChunkIndex < right.ChunkIndex
		}
		return left.ID < right.ID
	})
}

// collapseParentSlots gives one slot to every group of hits that resolve to the
// same parent chunk. The returned slot is the parent body when the merge stage
// resolved it (MatchTypeParentChunk); otherwise the highest-scoring child
// stands in for the group. The group's highest score is kept so the parent is
// ranked where its best child was, and the matched child ids travel on
// SubChunkID for highlighting and citation.
//
// Hits without a parent keep one slot each; this never groups by text
// similarity, so different rows of the same table remain distinct as long as
// they belong to different parents (see issue #1485).
func collapseParentSlots(results []*types.SearchResult) []*types.SearchResult {
	if len(results) == 0 {
		return results
	}

	type parentGroup struct {
		parent   *types.SearchResult
		best     *types.SearchResult
		score    float64
		childIDs []string
	}

	order := make([]string, 0, len(results))
	groups := make(map[string]*parentGroup, len(results))

	for _, result := range results {
		if result == nil {
			continue
		}
		// A child names its parent; a resolved parent result names itself
		// (its own ParentChunkID is empty), so both land on the same key.
		key := result.ID
		if result.ParentChunkID != "" {
			key = result.ParentChunkID
		}

		group := groups[key]
		if group == nil {
			group = &parentGroup{}
			groups[key] = group
			order = append(order, key)
		}
		if result.Score > group.score {
			group.score = result.Score
		}
		if result.MatchType == types.MatchTypeParentChunk {
			if group.parent == nil {
				group.parent = result
			}
			for _, id := range result.SubChunkID {
				if id != "" && id != result.ID {
					group.childIDs = append(group.childIDs, id)
				}
			}
			continue
		}
		group.childIDs = append(group.childIDs, result.ID)
		if group.best == nil || result.Score > group.best.Score {
			group.best = result
		}
	}

	collapsed := make([]*types.SearchResult, 0, len(order))
	for _, key := range order {
		group := groups[key]
		chosen := group.parent
		if chosen == nil {
			chosen = group.best
		}
		if chosen == nil {
			continue
		}
		chosen.Score = group.score
		subChunkIDs := make([]string, 0, len(group.childIDs))
		for _, id := range group.childIDs {
			if id != chosen.ID && !containsID(subChunkIDs, id) {
				subChunkIDs = append(subChunkIDs, id)
			}
		}
		if len(subChunkIDs) > 0 {
			chosen.SubChunkID = subChunkIDs
		}
		collapsed = append(collapsed, chosen)
	}
	return collapsed
}
