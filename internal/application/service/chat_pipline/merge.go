package chatpipline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

// OnEvent processes the CHUNK_MERGE event to merge search result chunks
func (p *PluginMerge) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "Merge", "input", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"candidate_cnt": len(chatManage.RerankResult),
	})

	// Use rerank results if available, fallback to search results
	searchResult := chatManage.RerankResult
	if len(searchResult) == 0 {
		pipelineWarn(ctx, "Merge", "fallback", map[string]interface{}{
			"reason": "empty_rerank_result",
		})
		searchResult = chatManage.SearchResult
		// Sort by score descending so dedup keeps highest-scored entries
		sort.Slice(searchResult, func(i, j int) bool {
			return searchResult[i].Score > searchResult[j].Score
		})
	}

	// Deduplicate after rerank so higher-scored duplicates are preferred
	beforeDedup := len(searchResult)
	searchResult = removeDuplicateResults(searchResult)
	pipelineInfo(ctx, "Merge", "dedup_summary", map[string]interface{}{
		"before": beforeDedup,
		"after":  len(searchResult),
	})

	// Inject relevant results from chat history with similarity filtering.
	// History references were produced for a previous query, so we only keep
	// those that are textually relevant to the current query to avoid injecting
	// stale or off-topic context.
	historyResults := filterHistoryResults(ctx, chatManage, searchResult)
	if len(historyResults) > 0 {
		pipelineInfo(ctx, "Merge", "history_inject", map[string]interface{}{
			"session_id":   chatManage.SessionID,
			"history_hits": len(historyResults),
		})
		searchResult = append(searchResult, historyResults...)
		searchResult = removeDuplicateResults(searchResult)
	}

	pipelineInfo(ctx, "Merge", "candidate_ready", map[string]interface{}{
		"chunk_cnt": len(searchResult),
	})

	// If there are no search results, return early
	if len(searchResult) == 0 {
		pipelineWarn(ctx, "Merge", "output", map[string]interface{}{
			"chunk_cnt": 0,
			"reason":    "no_candidates",
		})
		return next()
	}

	// Resolve parent chunks: replace child content with fuller parent content
	searchResult = p.resolveParentChunks(ctx, chatManage, searchResult)

	// Group chunks by their knowledge source ID
	knowledgeGroup := make(map[string]map[string][]*types.SearchResult)
	for _, chunk := range searchResult {
		if _, ok := knowledgeGroup[chunk.KnowledgeID]; !ok {
			knowledgeGroup[chunk.KnowledgeID] = make(map[string][]*types.SearchResult)
		}
		knowledgeGroup[chunk.KnowledgeID][chunk.ChunkType] = append(knowledgeGroup[chunk.KnowledgeID][chunk.ChunkType], chunk)
	}

	pipelineInfo(ctx, "Merge", "group_summary", map[string]interface{}{
		"knowledge_cnt": len(knowledgeGroup),
	})

	mergedChunks := []*types.SearchResult{}
	// Process each knowledge source separately
	for knowledgeID, chunkGroup := range knowledgeGroup {
		for _, chunks := range chunkGroup {
			pipelineInfo(ctx, "Merge", "group_process", map[string]interface{}{
				"knowledge_id": knowledgeID,
				"chunk_cnt":    len(chunks),
			})

			// Sort chunks by their start position in the original document
			sort.Slice(chunks, func(i, j int) bool {
				if chunks[i].StartAt == chunks[j].StartAt {
					return chunks[i].EndAt < chunks[j].EndAt
				}
				return chunks[i].StartAt < chunks[j].StartAt
			})

			// Merge overlapping or adjacent chunks
			knowledgeMergedChunks := []*types.SearchResult{chunks[0]}
			for i := 1; i < len(chunks); i++ {
				lastChunk := knowledgeMergedChunks[len(knowledgeMergedChunks)-1]
				// If the current chunk starts after the last chunk ends, add it to the merged chunks
				if chunks[i].StartAt > lastChunk.EndAt {
					knowledgeMergedChunks = append(knowledgeMergedChunks, chunks[i])
					continue
				}
			// Merge overlapping chunks
			if chunks[i].EndAt > lastChunk.EndAt {
				contentRunes := []rune(chunks[i].Content)
				offset := len(contentRunes) - (chunks[i].EndAt - lastChunk.EndAt)
				lastChunk.Content = lastChunk.Content + string(contentRunes[offset:])
				lastChunk.EndAt = chunks[i].EndAt
				lastChunk.SubChunkID = append(lastChunk.SubChunkID, chunks[i].ID)

				if err := mergeImageInfo(ctx, lastChunk, chunks[i]); err != nil {
					pipelineWarn(ctx, "Merge", "image_merge", map[string]interface{}{
						"knowledge_id": knowledgeID,
						"error":        err.Error(),
					})
				}
			} else {
				// Fully contained: track the subsumed chunk and merge its ImageInfo
				if !containsID(lastChunk.SubChunkID, chunks[i].ID) {
					lastChunk.SubChunkID = append(lastChunk.SubChunkID, chunks[i].ID)
				}
				if err := mergeImageInfo(ctx, lastChunk, chunks[i]); err != nil {
					pipelineWarn(ctx, "Merge", "image_merge_contained", map[string]interface{}{
						"knowledge_id": knowledgeID,
						"error":        err.Error(),
					})
				}
			}
			if chunks[i].Score > lastChunk.Score {
				lastChunk.Score = chunks[i].Score
			}
			}

			pipelineInfo(ctx, "Merge", "group_output", map[string]interface{}{
				"knowledge_id":  knowledgeID,
				"merged_chunks": len(knowledgeMergedChunks),
			})

			// Sort merged chunks by their score (highest first)
			sort.Slice(knowledgeMergedChunks, func(i, j int) bool {
				return knowledgeMergedChunks[i].Score > knowledgeMergedChunks[j].Score
			})

			mergedChunks = append(mergedChunks, knowledgeMergedChunks...)
		}
	}

	pipelineInfo(ctx, "Merge", "output", map[string]interface{}{
		"merged_total": len(mergedChunks),
	})

	mergedChunks = p.populateFAQAnswers(ctx, chatManage, mergedChunks)
	mergedChunks = p.expandShortContextWithNeighbors(ctx, chatManage, mergedChunks)

	chatManage.MergeResult = mergedChunks
	return next()
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

	// Replace child content with parent content
	for _, r := range results {
		if r.ParentChunkID == "" {
			continue
		}
		parent, ok := parentMap[r.ParentChunkID]
		if !ok || parent.Content == "" {
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

// filterHistoryResults retrieves history references and filters them by
// textual similarity to the current query. Only references that are above
// a Jaccard similarity threshold are kept, and their scores are discounted
// to reflect that they were not directly retrieved for the current query.
// Results already present in currentResults (by chunk ID) are excluded.
func filterHistoryResults(
	ctx context.Context,
	chatManage *types.ChatManage,
	currentResults []*types.SearchResult,
) []*types.SearchResult {
	const (
		// minSimilarity is the minimum Jaccard similarity between the current
		// query and a history chunk's content for it to be injected.
		minSimilarity = 0.15
		// historyScoreDiscount reduces the original score of history results
		// to rank them below freshly-retrieved results of similar relevance.
		historyScoreDiscount = 0.6
		// maxHistoryResults caps the number of history results injected to
		// avoid overwhelming the context with stale references.
		maxHistoryResults = 3
	)

	raw := getSearchResultFromHistory(chatManage)
	if len(raw) == 0 {
		return nil
	}

	// Build a set of chunk IDs already in current results for fast dedup
	existingIDs := make(map[string]struct{}, len(currentResults))
	for _, r := range currentResults {
		existingIDs[r.ID] = struct{}{}
	}

	// Use RewriteQuery if available (it's the cleaned-up retrieval query),
	// otherwise fall back to the original query.
	query := chatManage.RewriteQuery
	if query == "" {
		query = chatManage.Query
	}
	queryTokens := searchutil.TokenizeSimple(query)

	var filtered []*types.SearchResult
	for _, r := range raw {
		if _, exists := existingIDs[r.ID]; exists {
			continue
		}
		contentTokens := searchutil.TokenizeSimple(r.Content)
		sim := searchutil.Jaccard(queryTokens, contentTokens)
		if sim < minSimilarity {
			pipelineInfo(ctx, "Merge", "history_filter_drop", map[string]interface{}{
				"chunk_id":   r.ID,
				"similarity": sim,
			})
			continue
		}
		r.MatchType = types.MatchTypeHistory
		r.Score = r.Score * historyScoreDiscount
		r.Metadata = ensureMetadata(r.Metadata)
		r.Metadata["history_similarity"] = strings.TrimRight(strings.TrimRight(
			fmt.Sprintf("%.4f", sim), "0"), ".")
		filtered = append(filtered, r)

		pipelineInfo(ctx, "Merge", "history_filter_keep", map[string]interface{}{
			"chunk_id":   r.ID,
			"similarity": sim,
			"new_score":  r.Score,
		})

		if len(filtered) >= maxHistoryResults {
			break
		}
	}
	return filtered
}

// mergeImageInfo 合并两个chunk的ImageInfo
func mergeImageInfo(ctx context.Context, target *types.SearchResult, source *types.SearchResult) error {
	// 如果source没有ImageInfo，不需要合并
	if source.ImageInfo == "" {
		return nil
	}

	var sourceImageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(source.ImageInfo), &sourceImageInfos); err != nil {
		pipelineWarn(ctx, "Merge", "image_unmarshal_source", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	// 如果source的ImageInfo为空，不需要合并
	if len(sourceImageInfos) == 0 {
		return nil
	}

	// 处理target的ImageInfo
	var targetImageInfos []types.ImageInfo
	if target.ImageInfo != "" {
		if err := json.Unmarshal([]byte(target.ImageInfo), &targetImageInfos); err != nil {
			pipelineWarn(ctx, "Merge", "image_unmarshal_target", map[string]interface{}{
				"error": err.Error(),
			})
			// 如果目标解析失败，直接使用源数据
			target.ImageInfo = source.ImageInfo
			return nil
		}
	}

	// 合并ImageInfo
	targetImageInfos = append(targetImageInfos, sourceImageInfos...)

	// 去重
	uniqueMap := make(map[string]bool)
	uniqueImageInfos := make([]types.ImageInfo, 0, len(targetImageInfos))

	for _, imgInfo := range targetImageInfos {
		// 使用URL作为唯一标识
		if imgInfo.URL != "" && !uniqueMap[imgInfo.URL] {
			uniqueMap[imgInfo.URL] = true
			uniqueImageInfos = append(uniqueImageInfos, imgInfo)
		}
	}

	// 序列化合并后的ImageInfo
	mergedImageInfoJSON, err := json.Marshal(uniqueImageInfos)
	if err != nil {
		pipelineWarn(ctx, "Merge", "image_marshal", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	// 更新目标chunk的ImageInfo
	target.ImageInfo = string(mergedImageInfoJSON)
	pipelineInfo(ctx, "Merge", "image_merged", map[string]interface{}{
		"image_refs": len(uniqueImageInfos),
	})
	return nil
}

// populateFAQAnswers populates FAQ answers for the search results
func (p *PluginMerge) populateFAQAnswers(
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
		pipelineWarn(ctx, "Merge", "faq_enrich_skip", map[string]interface{}{
			"reason": "missing_tenant",
		})
		return results
	}

	chunkResultMap := make(map[string][]*types.SearchResult)
	chunkIDSet := make(map[string]struct{})
	for _, r := range results {
		if r == nil || r.ID == "" {
			continue
		}
		if r.ChunkType != string(types.ChunkTypeFAQ) {
			continue
		}
		chunkResultMap[r.ID] = append(chunkResultMap[r.ID], r)
		if _, exists := chunkIDSet[r.ID]; !exists {
			chunkIDSet[r.ID] = struct{}{}
		}
	}

	if len(chunkIDSet) == 0 {
		return results
	}

	chunkIDs := make([]string, 0, len(chunkIDSet))
	for id := range chunkIDSet {
		chunkIDs = append(chunkIDs, id)
	}

	chunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		pipelineWarn(ctx, "Merge", "faq_chunk_fetch_failed", map[string]interface{}{
			"error": err.Error(),
		})
		return results
	}

	updated := 0
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			if err != nil {
				pipelineWarn(ctx, "Merge", "faq_metadata_parse_failed", map[string]interface{}{
					"chunk_id": chunk.ID,
					"error":    err.Error(),
				})
			}
			continue
		}
		content := buildFAQAnswerContent(meta)
		if content == "" {
			continue
		}
		for _, r := range chunkResultMap[chunk.ID] {
			if r == nil {
				continue
			}
			r.Content = content
			updated++
		}
	}

	if updated > 0 {
		pipelineInfo(ctx, "Merge", "faq_content_enriched", map[string]interface{}{
			"chunk_cnt": updated,
		})
	}
	return results
}

// buildFAQAnswerContent builds the content of a FAQ answer
func buildFAQAnswerContent(meta *types.FAQChunkMetadata) string {
	if meta == nil {
		return ""
	}

	question := strings.TrimSpace(meta.StandardQuestion)
	answers := make([]string, 0, len(meta.Answers))
	for _, ans := range meta.Answers {
		if trimmed := strings.TrimSpace(ans); trimmed != "" {
			answers = append(answers, trimmed)
		}
	}

	if question == "" && len(answers) == 0 {
		return ""
	}

	var builder strings.Builder
	if question != "" {
		builder.WriteString("Q: ")
		builder.WriteString(question)
		builder.WriteString("\n")
	}

	if len(answers) > 0 {
		builder.WriteString("Answer:\n")
		for _, ans := range answers {
			builder.WriteString("- ")
			builder.WriteString(ans)
			builder.WriteString("\n")
		}
	}

	return strings.TrimSpace(builder.String())
}

// expandShortContextWithNeighbors expands the short context with neighbors
func (p *PluginMerge) expandShortContextWithNeighbors(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
) []*types.SearchResult {
	const (
		minLen = 350
		maxLen = 850
	)

	if len(results) == 0 || p.chunkRepo == nil {
		return results
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	if tenantID == 0 && chatManage != nil {
		tenantID = chatManage.TenantID
	}
	if tenantID == 0 {
		pipelineWarn(ctx, "Merge", "expand_skip", map[string]interface{}{
			"reason": "missing_tenant",
		})
		return results
	}

	type targetInfo struct {
		result *types.SearchResult
	}

	targets := make([]targetInfo, 0)
	baseIDsSet := make(map[string]struct{})

	for _, r := range results {
		if r == nil || r.ID == "" || r.Content == "" {
			continue
		}
		if r.ChunkType != string(types.ChunkTypeText) {
			continue
		}
		if runeLen(r.Content) >= minLen {
			continue
		}
		targets = append(targets, targetInfo{result: r})
		baseIDsSet[r.ID] = struct{}{}
		pipelineInfo(ctx, "Merge", "need_expand", map[string]interface{}{
			"chunk_id":   r.ID,
			"content":    r.Content,
			"chunk_type": r.ChunkType,
			"len":        runeLen(r.Content),
		})
	}

	if len(targets) == 0 {
		return results
	}

	baseIDs := make([]string, 0, len(baseIDsSet))
	for id := range baseIDsSet {
		baseIDs = append(baseIDs, id)
	}

	chunkMap := make(map[string]*types.Chunk, len(baseIDs))
	chunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, baseIDs)
	if err != nil {
		pipelineWarn(ctx, "Merge", "expand_list_base_failed", map[string]interface{}{
			"error": err.Error(),
		})
		return results
	}
	for _, chunk := range chunks {
		chunkMap[chunk.ID] = chunk
	}

	neighborIDsSet := make(map[string]struct{})
	for _, chunk := range chunkMap {
		if chunk == nil {
			continue
		}
		if chunk.PreChunkID != "" {
			if _, exists := chunkMap[chunk.PreChunkID]; !exists {
				neighborIDsSet[chunk.PreChunkID] = struct{}{}
			}
		}
		if chunk.NextChunkID != "" {
			if _, exists := chunkMap[chunk.NextChunkID]; !exists {
				neighborIDsSet[chunk.NextChunkID] = struct{}{}
			}
		}
	}

	if len(neighborIDsSet) > 0 {
		neighborIDs := make([]string, 0, len(neighborIDsSet))
		for id := range neighborIDsSet {
			neighborIDs = append(neighborIDs, id)
		}
		neighbors, err := p.chunkRepo.ListChunksByID(ctx, tenantID, neighborIDs)
		if err != nil {
			pipelineWarn(ctx, "Merge", "expand_list_neighbor_failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			for _, chunk := range neighbors {
				chunkMap[chunk.ID] = chunk
				pipelineInfo(ctx, "Merge", "expand_list_neighbor_success", map[string]interface{}{
					"neighbor_chunk_id":   chunk.ID,
					"neighbor_content":    chunk.Content,
					"neighbor_chunk_type": chunk.ChunkType,
					"neighbor_len":        runeLen(chunk.Content),
				})
			}
		}
	}

	for _, target := range targets {
		res := target.result
		p.fetchChunksIfMissing(ctx, tenantID, chunkMap, res.ID)
		baseChunk := chunkMap[res.ID]
		if baseChunk == nil || baseChunk.Content == "" || baseChunk.ChunkType != types.ChunkTypeText {
			continue
		}

		prevContent := ""
		nextContent := ""
		prevIDs := []string{}
		nextIDs := []string{}

		prevCursor := baseChunk.PreChunkID
		nextCursor := baseChunk.NextChunkID

		p.fetchChunksIfMissing(ctx, tenantID, chunkMap, prevCursor, nextCursor)

		if prevCursor != "" {
			if prevChunk := chunkMap[prevCursor]; prevChunk != nil && prevChunk.KnowledgeID == baseChunk.KnowledgeID {
				prevContent = prevChunk.Content
				prevIDs = append(prevIDs, prevChunk.ID)
				prevCursor = prevChunk.PreChunkID
			} else {
				prevCursor = ""
			}
		}

		if nextCursor != "" {
			if nextChunk := chunkMap[nextCursor]; nextChunk != nil && nextChunk.KnowledgeID == baseChunk.KnowledgeID {
				nextContent = nextChunk.Content
				nextIDs = append(nextIDs, nextChunk.ID)
				nextCursor = nextChunk.NextChunkID
			} else {
				nextCursor = ""
			}
		}

		var merged string
		for {
			merged = mergeOrderedContent(prevContent, baseChunk.Content, nextContent, maxLen)
			if merged == "" {
				break
			}
			if runeLen(merged) >= minLen {
				break
			}
			if prevCursor == "" && nextCursor == "" {
				break
			}

			expanded := false
			if prevCursor != "" {
				p.fetchChunksIfMissing(ctx, tenantID, chunkMap, prevCursor)
				if prevChunk := chunkMap[prevCursor]; prevChunk != nil &&
					prevChunk.KnowledgeID == baseChunk.KnowledgeID {
					prevContent = concatNoOverlap(prevChunk.Content, prevContent)
					prevIDs = append([]string{prevChunk.ID}, prevIDs...)
					prevCursor = prevChunk.PreChunkID
					expanded = true
				} else {
					prevCursor = ""
				}
			}

			merged = mergeOrderedContent(prevContent, baseChunk.Content, nextContent, maxLen)
			if runeLen(merged) >= minLen {
				break
			}

			if nextCursor != "" {
				p.fetchChunksIfMissing(ctx, tenantID, chunkMap, nextCursor)
				if nextChunk := chunkMap[nextCursor]; nextChunk != nil &&
					nextChunk.KnowledgeID == baseChunk.KnowledgeID {
					nextContent = concatNoOverlap(nextContent, nextChunk.Content)
					nextIDs = append(nextIDs, nextChunk.ID)
					nextCursor = nextChunk.NextChunkID
					expanded = true
				} else {
					nextCursor = ""
				}
			}

			if !expanded {
				break
			}
		}

		if merged == "" {
			continue
		}

		beforeLen := runeLen(res.Content)
		res.Content = merged

		for _, id := range prevIDs {
			if id != "" && !containsID(res.SubChunkID, id) {
				res.SubChunkID = append(res.SubChunkID, id)
			}
		}
		for _, id := range nextIDs {
			if id != "" && !containsID(res.SubChunkID, id) {
				res.SubChunkID = append(res.SubChunkID, id)
			}
		}

		if prevContent != "" {
			res.StartAt = baseChunk.StartAt - runeLen(prevContent)
			if res.StartAt < 0 {
				res.StartAt = 0
			}
		}
		res.EndAt = res.StartAt + runeLen(res.Content)

		pipelineInfo(ctx, "Merge", "expand_short_chunk", map[string]interface{}{
			"chunk_id":       res.ID,
			"prev_ids":       prevIDs,
			"next_ids":       nextIDs,
			"before_len":     beforeLen,
			"after_len":      runeLen(res.Content),
			"base_content":   baseChunk.Content,
			"after_content":  res.Content,
			"chunk_type":     res.ChunkType,
			"remaining_prev": prevCursor,
			"remaining_next": nextCursor,
		})
	}

	return results
}

// runeLen returns the length of a string in runes
func runeLen(s string) int {
	return len([]rune(s))
}

// mergeOrderedContent merges ordered content
func mergeOrderedContent(prev, base, next string, maxLen int) string {
	content := base
	if prev != "" {
		content = concatNoOverlap(prev, content)
	}
	if next != "" {
		content = concatNoOverlap(content, next)
	}
	runes := []rune(content)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return content
}

// concatNoOverlap concatenates two strings, removing potential overlapping prefix/suffix
func concatNoOverlap(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}

	ar := []rune(a)
	br := []rune(b)
	maxOverlap := minInt(len(ar), len(br))
	for k := maxOverlap; k > 0; k-- {
		if string(ar[len(ar)-k:]) == string(br[:k]) {
			return string(ar) + string(br[k:])
		}
	}
	return string(ar) + string(br)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func (p *PluginMerge) fetchChunksIfMissing(
	ctx context.Context,
	tenantID uint64,
	chunkMap map[string]*types.Chunk,
	chunkIDs ...string,
) {
	missing := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if id == "" {
			continue
		}
		if _, exists := chunkMap[id]; !exists {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}

	chunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, missing)
	if err != nil {
		pipelineWarn(ctx, "Merge", "expand_fetch_missing_failed", map[string]interface{}{
			"missing_cnt": len(missing),
			"error":       err.Error(),
		})
	}

	found := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		chunkMap[chunk.ID] = chunk
		found[chunk.ID] = struct{}{}
	}

	for _, id := range missing {
		if _, ok := found[id]; !ok {
			chunkMap[id] = nil
		}
	}
}
