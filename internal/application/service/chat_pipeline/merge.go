package chatpipeline

import (
	"context"
	"sort"

	"github.com/Tencent/WeKnora/internal/searchutil"
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
//  5. Group by knowledge source + chunk type, merge sequential current bodies
//  6. Populate FAQ answers
//  7. Expand short contexts with neighboring chunks
//     7.5. Re-merge sequential or contained bodies introduced by expansion
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
	mergedChunks := p.groupAndMergeCurrentContent(ctx, searchResult)

	// Step 6: Populate FAQ answers
	mergedChunks = p.populateFAQAnswers(ctx, chatManage, mergedChunks)

	// Step 7: Expand short contexts
	mergedChunks = p.expandShortContextWithNeighbors(ctx, chatManage, mergedChunks)

	// Step 7.5: Re-merge overlapping ranges introduced by expansion
	mergedChunks = p.groupAndMergeCurrentContent(ctx, mergedChunks)

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
	historyResults = p.refreshHistoryRecallWeights(ctx, chatManage, historyResults)
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

// groupAndMergeCurrentContent groups chunks by KnowledgeID + ChunkType, then joins
// sequential current bodies without consulting source character offsets.
func (p *PluginMerge) groupAndMergeCurrentContent(ctx context.Context, results []*types.SearchResult) []*types.SearchResult {
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
			if u.chunks[i].ChunkIndex == u.chunks[j].ChunkIndex {
				return u.chunks[i].ID < u.chunks[j].ID
			}
			return u.chunks[i].ChunkIndex < u.chunks[j].ChunkIndex
		})

		grouped := p.mergeSequentialChunks(ctx, u.knowledgeID, u.chunks)

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

	// Global sort restores relevance order after map-based grouping.
	// n is typically < 100, O(n log n) is negligible here.
	sortSearchResultsDeterministically(mergedChunks)

	pipelineInfo(ctx, "Merge", "output", map[string]interface{}{
		"merged_total": len(mergedChunks),
	})
	return mergedChunks
}

// resolveParentChunks expands parent-child retrieval results with current
// parent_text context. Text children and image grandchildren share the same
// behavior, while image Markdown is scoped to the matched text child by
// durable URLs rather than parser coordinates.
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

	// Image hits have an image -> text -> parent_text chain. Fetch the
	// grandparent only for those results so they retain parent-child context
	// without using editable StartAt/EndAt coordinates.
	imageTextParentIDs := make(map[string]struct{})
	for _, r := range results {
		if r.ChunkType == string(types.ChunkTypeImageOCR) || r.ChunkType == string(types.ChunkTypeImageCaption) {
			imageTextParentIDs[r.ParentChunkID] = struct{}{}
		}
	}
	if len(imageTextParentIDs) > 0 {
		grandparentIDs := make([]string, 0)
		grandparentSeen := make(map[string]struct{})
		for _, parent := range parentChunks {
			if _, needed := imageTextParentIDs[parent.ID]; !needed {
				continue
			}
			if parent.ParentChunkID == "" || parent.ChunkType != types.ChunkTypeText {
				continue
			}
			if _, already := parentMap[parent.ParentChunkID]; already {
				continue
			}
			if _, already := grandparentSeen[parent.ParentChunkID]; already {
				continue
			}
			grandparentSeen[parent.ParentChunkID] = struct{}{}
			grandparentIDs = append(grandparentIDs, parent.ParentChunkID)
		}
		if len(grandparentIDs) > 0 {
			grandparents, fetchErr := p.chunkRepo.ListChunksByID(ctx, tenantID, grandparentIDs)
			if fetchErr != nil {
				pipelineWarn(ctx, "Merge", "grandparent_fetch_failed", map[string]interface{}{
					"error": fetchErr.Error(),
				})
			} else {
				for _, grandparent := range grandparents {
					parentMap[grandparent.ID] = grandparent
				}
			}
		}
	}

	// Batch-fetch image_info scoped to matched text children only.
	textChildIDs := collectScopedTextChildIDs(results, parentMap)
	var scopedImageInfo map[string]string
	if len(textChildIDs) > 0 {
		scopedImageInfo = searchutil.CollectImageInfoByChunkIDs(ctx, p.chunkRepo, tenantID, textChildIDs)
	}

	for _, r := range results {
		if r.ParentChunkID == "" {
			continue
		}

		switch r.ChunkType {
		case string(types.ChunkTypeText):
			// text → parent_text: expand to full parent for surrounding context
			// (the core parent-child value). Scope ImageInfo to this child only so
			// image-heavy parents do not inject every sibling page's OCR/Caption.
			parent, ok := parentMap[r.ParentChunkID]
			if !ok || parent.Content == "" || parent.ChunkType != types.ChunkTypeParentText {
				continue
			}
			pipelineInfo(ctx, "Merge", "parent_resolve", map[string]interface{}{
				"child_id":   r.ID,
				"parent_id":  r.ParentChunkID,
				"child_len":  runeLen(r.Content),
				"parent_len": runeLen(parent.Content),
				"scoped_img": true,
			})
			assignScopedImageInfo(r, scopedImageInfo, r.ID)
			parentContent := searchutil.PruneMarkdownImagesByImageInfo(parent.Content, r.ImageInfo)
			r.Content = searchutil.JoinChunkContent(parentContent, r.Content, "\n\n")
			if !containsID(r.SubChunkID, r.ID) {
				r.SubChunkID = append(r.SubChunkID, r.ID)
			}

		case string(types.ChunkTypeImageOCR), string(types.ChunkTypeImageCaption):
			textParent, ok := parentMap[r.ParentChunkID]
			if !ok || textParent.Content == "" || textParent.ChunkType != types.ChunkTypeText {
				continue
			}
			hitImageInfo := r.ImageInfo
			contentSource := textParent
			if textParent.ParentChunkID != "" {
				if grandparent, found := parentMap[textParent.ParentChunkID]; found &&
					grandparent.ChunkType == types.ChunkTypeParentText && grandparent.Content != "" {
					contentSource = grandparent
				}
			}
			r.Content = textParent.Content
			r.ChunkIndex = textParent.ChunkIndex
			assignScopedImageInfo(r, scopedImageInfo, textParent.ID)
			if r.ImageInfo == "" && hitImageInfo != "" {
				r.ImageInfo = searchutil.FilterImageInfoByContentURLs(textParent.Content, hitImageInfo)
			}
			textContent := searchutil.PruneMarkdownImagesByImageInfo(textParent.Content, r.ImageInfo)
			parentContent := searchutil.PruneMarkdownImagesByImageInfo(contentSource.Content, r.ImageInfo)
			r.Content = searchutil.JoinChunkContent(parentContent, textContent, "\n\n")
			pipelineInfo(ctx, "Merge", "image_parent_resolve", map[string]interface{}{
				"child_id":   r.ID,
				"child_type": r.ChunkType,
				"text_id":    textParent.ID,
				"parent_id":  contentSource.ID,
				"match_len":  runeLen(r.Content),
				"parent_len": runeLen(contentSource.Content),
				"scoped":     true,
			})
			if !containsID(r.SubChunkID, r.ID) {
				r.SubChunkID = append(r.SubChunkID, r.ID)
			}
		}
	}

	return results
}

// collectScopedTextChildIDs returns text chunk IDs whose image_info should be
// loaded for parent-child merge scoping.
func collectScopedTextChildIDs(
	results []*types.SearchResult,
	parentMap map[string]*types.Chunk,
) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, r := range results {
		if r.ParentChunkID == "" {
			continue
		}
		switch r.ChunkType {
		case string(types.ChunkTypeText):
			parent := parentMap[r.ParentChunkID]
			if parent == nil || parent.ChunkType != types.ChunkTypeParentText {
				continue
			}
			if _, ok := seen[r.ID]; ok {
				continue
			}
			seen[r.ID] = struct{}{}
			ids = append(ids, r.ID)
		case string(types.ChunkTypeImageOCR), string(types.ChunkTypeImageCaption):
			if _, ok := seen[r.ParentChunkID]; ok {
				continue
			}
			seen[r.ParentChunkID] = struct{}{}
			ids = append(ids, r.ParentChunkID)
		}
	}
	return ids
}

// assignScopedImageInfo sets ImageInfo from the per-text-child map, falling
// back to URLs present in the result content.
func assignScopedImageInfo(r *types.SearchResult, scoped map[string]string, textChildID string) {
	if scoped != nil {
		if info, ok := scoped[textChildID]; ok && info != "" {
			r.ImageInfo = info
			return
		}
	}
	if r.ImageInfo != "" {
		r.ImageInfo = searchutil.FilterImageInfoByContentURLs(r.Content, r.ImageInfo)
	}
}
