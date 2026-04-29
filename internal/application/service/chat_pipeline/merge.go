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

	// Check if any results are image chunks; only then do we need
	// grandparent resolution and the extra DB round-trip.
	hasImageResults := false
	for _, r := range results {
		if r.ChunkType == string(types.ChunkTypeImageOCR) || r.ChunkType == string(types.ChunkTypeImageCaption) {
			hasImageResults = true
			break
		}
	}

	var grandparentIDs []string
	if hasImageResults {
		// Fetch grandparent chunks for the image → text → parent_text chain.
		for _, pc := range parentChunks {
			if pc.ParentChunkID != "" && pc.ChunkType == types.ChunkTypeText {
				if _, already := parentMap[pc.ParentChunkID]; !already {
					grandparentIDs = append(grandparentIDs, pc.ParentChunkID)
				}
			}
		}
		if len(grandparentIDs) > 0 {
			gpChunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, grandparentIDs)
			if err != nil {
				pipelineWarn(ctx, "Merge", "grandparent_fetch_failed", map[string]interface{}{
					"error": err.Error(),
				})
			} else {
				for _, c := range gpChunks {
					parentMap[c.ID] = c
				}
			}
		}
	}

	// Collect merged ImageInfo for each parent by fetching ALL sibling
	// child chunks. Individual child chunks only carry ImageInfo for images
	// within their own range, but the parent content spans all children.
	imageInfoIDs := ids
	if len(grandparentIDs) > 0 {
		imageInfoIDs = append(append([]string(nil), ids...), grandparentIDs...)
	}
	parentImageInfoMap := p.collectParentImageInfo(ctx, tenantID, imageInfoIDs)

	for _, r := range results {
		if r.ParentChunkID == "" {
			continue
		}

		switch r.ChunkType {
		case string(types.ChunkTypeText):
			// text → parent_text resolution (parent-child chunking strategy).
			// Summary chunks also carry a ParentChunkID that points to their
			// source chunk, but that is a different semantic — replacing
			// summary content with its source would degrade quality.
			parent, ok := parentMap[r.ParentChunkID]
			if !ok || parent.Content == "" || parent.ChunkType != types.ChunkTypeParentText {
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
			if !containsID(r.SubChunkID, r.ID) {
				r.SubChunkID = append(r.SubChunkID, r.ID)
			}

		case string(types.ChunkTypeImageOCR), string(types.ChunkTypeImageCaption):
			// image_ocr/image_caption → text parent → optional parent_text grandparent.
			// Replace content with parent text for surrounding context.
			parent, ok := parentMap[r.ParentChunkID]
			if !ok || parent.Content == "" {
				continue
			}
			resolvedParent := parent
			// If parent text uses parent-child chunking, resolve one more level
			if parent.ChunkType == types.ChunkTypeText && parent.ParentChunkID != "" {
				if gp, gpOK := parentMap[parent.ParentChunkID]; gpOK && gp.ChunkType == types.ChunkTypeParentText && gp.Content != "" {
					resolvedParent = gp
				}
			}
			pipelineInfo(ctx, "Merge", "image_parent_resolve", map[string]interface{}{
				"child_id":    r.ID,
				"child_type":  r.ChunkType,
				"resolved_id": resolvedParent.ID,
				"child_len":   runeLen(r.Content),
				"parent_len":  runeLen(resolvedParent.Content),
			})
			r.Content = resolvedParent.Content
			r.StartAt = resolvedParent.StartAt
			r.EndAt = resolvedParent.EndAt
			if mergedInfo, ok := parentImageInfoMap[resolvedParent.ID]; ok && mergedInfo != "" {
				r.ImageInfo = mergedInfo
			}
			if !containsID(r.SubChunkID, r.ID) {
				r.SubChunkID = append(r.SubChunkID, r.ID)
			}
		}
	}

	return results
}

// collectParentImageInfo batch-fetches image info for the given parent_text
// chunk IDs using a two-level query:
//
//	Level 1: parent_text → text children
//	Level 2: text children → image_ocr / image_caption grandchildren (carry image_info)
func (p *PluginMerge) collectParentImageInfo(
	ctx context.Context,
	tenantID uint64,
	parentIDs []string,
) map[string]string {
	// Level 1: get direct children of parent_text chunks
	children, err := p.chunkRepo.ListChunksByParentIDs(ctx, tenantID, parentIDs)
	if err != nil {
		pipelineWarn(ctx, "Merge", "parent_imageinfo_fetch_failed", map[string]interface{}{
			"parent_cnt": len(parentIDs),
			"error":      err.Error(),
		})
		return nil
	}

	type agg struct {
		infos    []types.ImageInfo
		seenURLs map[string]bool
	}
	aggMap := make(map[string]*agg)
	addInfo := func(targetID string, chunk *types.Chunk) {
		if chunk.ImageInfo == "" {
			return
		}
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(chunk.ImageInfo), &infos); err != nil || len(infos) == 0 {
			return
		}
		a, ok := aggMap[targetID]
		if !ok {
			a = &agg{seenURLs: make(map[string]bool)}
			aggMap[targetID] = a
		}
		for _, info := range infos {
			key := info.URL
			if key == "" {
				key = info.OriginalURL
			}
			if key != "" && !a.seenURLs[key] {
				a.seenURLs[key] = true
				a.infos = append(a.infos, info)
			}
		}
	}

	var textChildIDs []string
	textToParent := make(map[string]string, len(children))
	for _, child := range children {
		switch child.ChunkType {
		case types.ChunkTypeImageOCR, types.ChunkTypeImageCaption:
			addInfo(child.ParentChunkID, child)
		case types.ChunkTypeText:
			textChildIDs = append(textChildIDs, child.ID)
			textToParent[child.ID] = child.ParentChunkID
		}
	}

	// Level 2: text children → image grandchildren
	if len(textChildIDs) > 0 {
		grandChildren, err := p.chunkRepo.ListChunksByParentIDs(ctx, tenantID, textChildIDs)
		if err != nil {
			pipelineWarn(ctx, "Merge", "parent_imageinfo_l2_fetch_failed", map[string]interface{}{
				"text_cnt": len(textChildIDs),
				"error":    err.Error(),
			})
		} else {
			for _, gc := range grandChildren {
				if gc.ChunkType != types.ChunkTypeImageOCR && gc.ChunkType != types.ChunkTypeImageCaption {
					continue
				}
				if parentTextID, ok := textToParent[gc.ParentChunkID]; ok {
					addInfo(parentTextID, gc)
				}
			}
		}
	}

	result := make(map[string]string, len(aggMap))
	for id, a := range aggMap {
		if len(a.infos) == 0 {
			continue
		}
		data, err := json.Marshal(a.infos)
		if err == nil {
			result[id] = string(data)
		}
	}
	return result
}
