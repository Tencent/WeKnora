package chatpipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginMerge handles merging of search result chunks
type PluginMerge struct {
	chunkRepo    interfaces.ChunkRepository
	chunkService interfaces.ChunkService // for parent chunk resolution
}

// NewPluginMerge creates and registers a new PluginMerge instance
func NewPluginMerge(eventManager *EventManager, chunkRepo interfaces.ChunkRepository, chunkService interfaces.ChunkService) *PluginMerge {
	res := &PluginMerge{
		chunkRepo:    chunkRepo,
		chunkService: chunkService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginMerge) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_MERGE}
}

// OnEvent processes the CHUNK_MERGE event to merge search result chunks.
// The merge pipeline is:
//  1. Select input (rerank or search fallback)
//  2. Deduplicate by ID and content signature
//  3. Inject relevant history references
//  4. Resolve parent chunks (child → parent content)
//  5. Group by knowledge source + chunk type, merge overlapping ranges
//  6. Populate FAQ answers
//  7. Expand short contexts with neighboring chunks
//  7.5. Re-merge overlapping ranges introduced by expansion
//  8. Final deduplication (ID + signature + partial content overlap)
func (p *PluginMerge) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if !chatManage.NeedsRetrieval() {
		return next()
	}
	pipelineInfo(ctx, "Merge", "input", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"candidate_cnt": len(chatManage.RerankResult),
	})

	// Step 1: Select input
	searchResult := p.selectInputResults(ctx, chatManage)

	// Step 2: Initial dedup
	searchResult = p.dedup(ctx, "dedup_summary", searchResult)

	// Step 3: Inject history references
	searchResult = p.injectHistoryResults(ctx, chatManage, searchResult)

	pipelineInfo(ctx, "Merge", "candidate_ready", map[string]interface{}{
		"chunk_cnt": len(searchResult),
	})

	if len(searchResult) == 0 {
		pipelineWarn(ctx, "Merge", "output", map[string]interface{}{
			"chunk_cnt": 0,
			"reason":    "no_candidates",
		})
		return next()
	}

	// Step 4: Resolve parent chunks
	searchResult = p.resolveParentChunks(ctx, chatManage, searchResult)

	// Step 5: Group by knowledge/chunkType and merge overlapping ranges
	mergedChunks := p.groupAndMergeOverlapping(ctx, searchResult)

	// Step 6: Populate FAQ answers
	mergedChunks = p.populateFAQAnswers(ctx, chatManage, mergedChunks)

	// Step 7: Expand short contexts
	mergedChunks = p.expandShortContextWithNeighbors(ctx, chatManage, mergedChunks)

	// Step 7.5: Re-merge overlapping ranges introduced by expansion
	mergedChunks = p.groupAndMergeOverlapping(ctx, mergedChunks)

	// Step 8: Final dedup — catches exact duplicates plus partial content overlaps
	mergedChunks = p.dedup(ctx, "final_dedup", mergedChunks)
	mergedChunks = removePartialOverlaps(ctx, mergedChunks)

	chatManage.MergeResult = mergedChunks
	return next()
}

// selectInputResults picks rerank results if available, falling back to search
// results sorted by score descending.
func (p *PluginMerge) selectInputResults(ctx context.Context, chatManage *types.ChatManage) []*types.SearchResult {
	if len(chatManage.RerankResult) > 0 {
		return chatManage.RerankResult
	}
	pipelineWarn(ctx, "Merge", "fallback", map[string]interface{}{
		"reason": "empty_rerank_result",
	})
	result := chatManage.SearchResult
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result
}

// dedup wraps removeDuplicateResults with before/after logging.
func (p *PluginMerge) dedup(ctx context.Context, label string, results []*types.SearchResult) []*types.SearchResult {
	before := len(results)
	out := removeDuplicateResults(results)
	if len(out) < before {
		pipelineInfo(ctx, "Merge", label, map[string]interface{}{
			"before": before,
			"after":  len(out),
		})
	}
	return out
}

// injectHistoryResults appends relevant history references to the current results
// and deduplicates the combined set.
func (p *PluginMerge) injectHistoryResults(
	ctx context.Context,
	chatManage *types.ChatManage,
	current []*types.SearchResult,
) []*types.SearchResult {
	historyResults := filterHistoryResults(ctx, chatManage, current)
	if len(historyResults) == 0 {
		return current
	}
	pipelineInfo(ctx, "Merge", "history_inject", map[string]interface{}{
		"session_id":   chatManage.SessionID,
		"history_hits": len(historyResults),
	})
	combined := append(current, historyResults...)
	return removeDuplicateResults(combined)
}

// groupAndMergeOverlapping groups chunks by KnowledgeID + ChunkType, then merges
// overlapping ranges within each group using mergeOverlappingChunks.
func (p *PluginMerge) groupAndMergeOverlapping(ctx context.Context, results []*types.SearchResult) []*types.SearchResult {
	// Group by KnowledgeID → ChunkType
	knowledgeGroup := make(map[string]map[string][]*types.SearchResult)
	for _, chunk := range results {
		if _, ok := knowledgeGroup[chunk.KnowledgeID]; !ok {
			knowledgeGroup[chunk.KnowledgeID] = make(map[string][]*types.SearchResult)
		}
		knowledgeGroup[chunk.KnowledgeID][chunk.ChunkType] = append(
			knowledgeGroup[chunk.KnowledgeID][chunk.ChunkType], chunk,
		)
	}

	pipelineInfo(ctx, "Merge", "group_summary", map[string]interface{}{
		"knowledge_cnt": len(knowledgeGroup),
	})

	// Flatten into independent (knowledgeID, chunks) work units for parallel merge.
	type mergeUnit struct {
		knowledgeID string
		chunks      []*types.SearchResult
	}
	var units []mergeUnit
	for knowledgeID, chunkGroup := range knowledgeGroup {
		for _, chunks := range chunkGroup {
			units = append(units, mergeUnit{knowledgeID: knowledgeID, chunks: chunks})
		}
	}

	groupResults := ParallelMap(units, 0, func(_ int, u mergeUnit) []*types.SearchResult {
		pipelineInfo(ctx, "Merge", "group_process", map[string]interface{}{
			"knowledge_id": u.knowledgeID,
			"chunk_cnt":    len(u.chunks),
		})

		sort.Slice(u.chunks, func(i, j int) bool {
			if u.chunks[i].StartAt == u.chunks[j].StartAt {
				return u.chunks[i].EndAt < u.chunks[j].EndAt
			}
			return u.chunks[i].StartAt < u.chunks[j].StartAt
		})

		grouped := p.mergeOverlappingChunks(ctx, u.knowledgeID, u.chunks)

		pipelineInfo(ctx, "Merge", "group_output", map[string]interface{}{
			"knowledge_id":  u.knowledgeID,
			"merged_chunks": len(grouped),
		})
		return grouped
	})

	var mergedChunks []*types.SearchResult
	for _, g := range groupResults {
		mergedChunks = append(mergedChunks, g...)
	}

	pipelineInfo(ctx, "Merge", "output", map[string]interface{}{
		"merged_total": len(mergedChunks),
	})
	return mergedChunks
}

// resolveParentChunks replaces child chunk content with parent chunk content
// for results that have ParentChunkID set. This provides fuller context
// for small child chunks used in parent-child chunking strategy.
func (p *PluginMerge) resolveParentChunks(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
) []*types.SearchResult {
	if len(results) == 0 || p.chunkRepo == nil {
		return results
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	if tenantID == 0 && chatManage != nil {
		tenantID = chatManage.TenantID
	}
	if tenantID == 0 {
		pipelineWarn(ctx, "Merge", "parent_resolve_skip", map[string]interface{}{
			"reason": "missing_tenant",
		})
		return results
	}

	// Collect unique parent chunk IDs
	parentIDs := make(map[string]struct{})
	for _, r := range results {
		if r.ParentChunkID != "" {
			parentIDs[r.ParentChunkID] = struct{}{}
		}
	}

	if len(parentIDs) == 0 {
		return results
	}

	// Batch fetch parent chunks
	ids := make([]string, 0, len(parentIDs))
	for id := range parentIDs {
		ids = append(ids, id)
	}
	parentChunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, ids)
	if err != nil {
		pipelineWarn(ctx, "Merge", "parent_resolve_failed", map[string]interface{}{
			"error": err.Error(),
		})
		return results
	}

	parentMap := make(map[string]*types.Chunk, len(parentChunks))
	for _, c := range parentChunks {
		parentMap[c.ID] = c
	}

	// Collect merged ImageInfo for each parent by fetching ALL sibling
	// child chunks. Individual child chunks only carry ImageInfo for images
	// within their own range, but the parent content spans all children.
	parentImageInfoMap := p.collectParentImageInfo(ctx, tenantID, ids)

	// Replace child content with parent content.
	// Only replace for text chunks whose parent is a parent_text chunk
	// (i.e., the parent-child chunking strategy). Summary chunks also carry
	// a ParentChunkID that points to their source chunk, but that is a
	// different semantic — replacing summary content with its source would
	// degrade quality.
	for _, r := range results {
		if r.ParentChunkID == "" {
			continue
		}
		// Skip non-text chunks (e.g., summary, image_caption) — their
		// ParentChunkID has a different meaning than the parent-child
		// chunking strategy.
		if r.ChunkType != string(types.ChunkTypeText) {
			continue
		}
		parent, ok := parentMap[r.ParentChunkID]
		if !ok || parent.Content == "" {
			continue
		}
		// Only replace if the parent is actually a parent_text chunk from
		// the parent-child chunking strategy.
		if parent.ChunkType != types.ChunkTypeParentText {
			continue
		}
		pipelineInfo(ctx, "Merge", "parent_resolve", map[string]interface{}{
			"child_id":   r.ID,
			"parent_id":  r.ParentChunkID,
			"child_len":  runeLen(r.Content),
			"parent_len": runeLen(parent.Content),
		})
		r.Content = parent.Content
		r.StartAt = parent.StartAt
		r.EndAt = parent.EndAt
		if mergedImageInfo, ok := parentImageInfoMap[r.ParentChunkID]; ok && mergedImageInfo != "" {
			r.ImageInfo = mergedImageInfo
		}
		// Track the original child as a sub-chunk
		if !containsID(r.SubChunkID, r.ID) {
			r.SubChunkID = append(r.SubChunkID, r.ID)
		}
	}

	return results
}

// collectParentImageInfo batch-fetches all child chunks for the given parents
// and merges their ImageInfo into a single JSON string per parent. This ensures
// that when child content is replaced with parent content, the complete set of
// image descriptions across all sibling chunks is preserved.
func (p *PluginMerge) collectParentImageInfo(
	ctx context.Context,
	tenantID uint64,
	parentIDs []string,
) map[string]string {
	result := make(map[string]string, len(parentIDs))

	allChildren, err := p.chunkRepo.ListChunksByParentIDs(ctx, tenantID, parentIDs)
	if err != nil {
		pipelineWarn(ctx, "Merge", "parent_imageinfo_fetch_failed", map[string]interface{}{
			"parent_cnt": len(parentIDs),
			"error":      err.Error(),
		})
		return result
	}

	// Group children by parent chunk ID, collecting unique ImageInfo entries
	type parentAgg struct {
		imageInfos []types.ImageInfo
		uniqueURLs map[string]bool
		siblingCnt int
	}
	aggMap := make(map[string]*parentAgg, len(parentIDs))

	for _, child := range allChildren {
		agg, ok := aggMap[child.ParentChunkID]
		if !ok {
			agg = &parentAgg{uniqueURLs: make(map[string]bool)}
			aggMap[child.ParentChunkID] = agg
		}
		agg.siblingCnt++

		if child.ImageInfo == "" {
			continue
		}
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(child.ImageInfo), &infos); err != nil {
			pipelineWarn(ctx, "Merge", "parent_imageinfo_parse", map[string]interface{}{
				"chunk_id": child.ID,
				"error":    err.Error(),
			})
			continue
		}
		for _, info := range infos {
			key := info.URL
			if key == "" {
				key = info.OriginalURL
			}
			if key != "" && !agg.uniqueURLs[key] {
				agg.uniqueURLs[key] = true
				agg.imageInfos = append(agg.imageInfos, info)
			}
		}
	}

	for parentID, agg := range aggMap {
		if len(agg.imageInfos) == 0 {
			continue
		}
		merged, err := json.Marshal(agg.imageInfos)
		if err != nil {
			pipelineWarn(ctx, "Merge", "parent_imageinfo_marshal", map[string]interface{}{
				"parent_id": parentID,
				"error":     err.Error(),
			})
			continue
		}
		result[parentID] = string(merged)

		pipelineInfo(ctx, "Merge", "parent_imageinfo_collected", map[string]interface{}{
			"parent_id":   parentID,
			"sibling_cnt": agg.siblingCnt,
			"image_cnt":   len(agg.imageInfos),
		})
	}

	return result
}
