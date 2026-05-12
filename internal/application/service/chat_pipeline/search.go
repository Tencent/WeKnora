package chatpipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginSearch implements search functionality for chat pipeline
type PluginSearch struct {
	knowledgeBaseService  interfaces.KnowledgeBaseService
	knowledgeService      interfaces.KnowledgeService
	chunkService          interfaces.ChunkService
	config                *config.Config
	webSearchService      interfaces.WebSearchService
	tenantService         interfaces.TenantService
	sessionService        interfaces.SessionService
	webSearchStateService interfaces.WebSearchStateService
	webSearchProviderRepo interfaces.WebSearchProviderRepository
}

func NewPluginSearch(eventManager *EventManager,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	config *config.Config,
	webSearchService interfaces.WebSearchService,
	tenantService interfaces.TenantService,
	sessionService interfaces.SessionService,
	webSearchStateService interfaces.WebSearchStateService,
	webSearchProviderRepo interfaces.WebSearchProviderRepository,
) *PluginSearch {
	res := &PluginSearch{
		knowledgeBaseService:  knowledgeBaseService,
		knowledgeService:      knowledgeService,
		chunkService:          chunkService,
		config:                config,
		webSearchService:      webSearchService,
		tenantService:         tenantService,
		sessionService:        sessionService,
		webSearchStateService: webSearchStateService,
		webSearchProviderRepo: webSearchProviderRepo,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginSearch) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_SEARCH}
}

// OnEvent handles search events in the chat pipeline
func (p *PluginSearch) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// Check if we have search targets or web search enabled
	hasKBTargets := len(chatManage.SearchTargets) > 0 || len(chatManage.KnowledgeBaseIDs) > 0 || len(chatManage.KnowledgeIDs) > 0
	if !hasKBTargets && !chatManage.WebSearchEnabled {
		pipelineError(ctx, "Search", "kb_not_found", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return nil
	}

	pipelineInfo(ctx, "Search", "input", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"rewrite_query":  chatManage.RewriteQuery,
		"search_targets": len(chatManage.SearchTargets),
		"tenant_id":      chatManage.TenantID,
		"web_enabled":    chatManage.WebSearchEnabled,
	})

	// Run KB search and web search concurrently
	pipelineInfo(ctx, "Search", "plan", map[string]interface{}{
		"search_targets":    len(chatManage.SearchTargets),
		"embedding_top_k":   chatManage.EmbeddingTopK,
		"vector_threshold":  chatManage.VectorThreshold,
		"keyword_threshold": chatManage.KeywordThreshold,
	})
	var wg sync.WaitGroup
	var mu sync.Mutex
	allResults := make([]*types.SearchResult, 0)

	wg.Add(2)
	// Goroutine 1: Knowledge base search using SearchTargets
	go func() {
		defer wg.Done()
		kbResults := p.searchByTargets(ctx, chatManage)
		if len(kbResults) > 0 {
			mu.Lock()
			allResults = append(allResults, kbResults...)
			mu.Unlock()
		}
	}()

	// Goroutine 2: Web search (if enabled)
	go func() {
		defer wg.Done()
		webResults := p.searchWebIfEnabled(ctx, chatManage)
		if len(webResults) > 0 {
			mu.Lock()
			allResults = append(allResults, webResults...)
			mu.Unlock()
		}
	}()

	wg.Wait()

	chatManage.SearchResult = allResults

	logSearchScoreSample(ctx, "result_score_before_normalize", chatManage.SearchResult)

	// If recall is low, attempt query expansion with keyword-focused search
	if chatManage.EnableQueryExpansion && len(chatManage.SearchResult) < max(1, chatManage.EmbeddingTopK) {
		expResults := p.runQueryExpansion(ctx, chatManage)
		if len(expResults) > 0 {
			chatManage.SearchResult = append(chatManage.SearchResult, expResults...)
		}
	}

	logSearchScoreSample(ctx, "final_score", chatManage.SearchResult)

	// Return if we have results
	if len(chatManage.SearchResult) != 0 {
		pipelineInfo(ctx, "Search", "output", map[string]interface{}{
			"session_id":   chatManage.SessionID,
			"result_count": len(chatManage.SearchResult),
		})
		return next()
	}
	pipelineWarn(ctx, "Search", "output", map[string]interface{}{
		"session_id":   chatManage.SessionID,
		"result_count": 0,
	})
	return ErrSearchNothing
}

// getSearchResultFromHistory retrieves relevant knowledge references from chat history
func getSearchResultFromHistory(chatManage *types.ChatManage) []*types.SearchResult {
	if len(chatManage.History) == 0 {
		return nil
	}
	// Search history in reverse chronological order
	for i := len(chatManage.History) - 1; i >= 0; i-- {
		if len(chatManage.History[i].KnowledgeReferences) > 0 {
			// Mark all references as history matches
			for _, reference := range chatManage.History[i].KnowledgeReferences {
				reference.MatchType = types.MatchTypeHistory
			}
			return chatManage.History[i].KnowledgeReferences
		}
	}
	return nil
}

func removeDuplicateResults(results []*types.SearchResult) []*types.SearchResult {
	seen := make(map[string]bool)
	contentSig := make(map[string]string) // sig -> first chunk ID
	var uniqueResults []*types.SearchResult
	for _, r := range results {
		// Only deduplicate by exact chunk ID — do NOT treat shared ParentChunkID
		// as duplicates, because different child chunks of the same parent carry
		// different content segments that may all be relevant.
		if seen[r.ID] {
			logger.Debugf(context.Background(), "Dedup: chunk %s removed due to duplicate ID", r.ID)
			continue
		}
		sig := buildContentSignature(r.Content)
		if sig != "" {
			if firstChunk, exists := contentSig[sig]; exists {
				logger.Debugf(context.Background(), "Dedup: chunk %s removed due to content signature (dup of %s, sig prefix: %.50s...)", r.ID, firstChunk, sig)
				continue
			}
			contentSig[sig] = r.ID
		}
		seen[r.ID] = true
		uniqueResults = append(uniqueResults, r)
	}
	return uniqueResults
}

func buildContentSignature(content string) string {
	return searchutil.BuildContentSignature(content)
}

// removePartialOverlaps drops chunks whose content is largely contained within
// a higher-scored chunk, even across different knowledge sources. This catches
// cross-KB duplicates and near-duplicates that exact-signature dedup misses.
//
// Two thresholds are used:
//   - Substring containment: if the normalized short text is a literal substring
//     of the normalized long text, the shorter chunk is removed.
//   - Token overlap coefficient >= 0.85: if 85%+ of the smaller chunk's tokens
//     appear in the larger chunk, the smaller one is redundant.
//
// The input slice MUST already be deduplicated by ID/signature. Within each
// pair the chunk with the lower score is the candidate for removal; ties are
// broken by content length (longer wins).
func removePartialOverlaps(ctx context.Context, results []*types.SearchResult) []*types.SearchResult {
	const overlapThreshold = 0.85

	if len(results) <= 1 {
		return results
	}

	type normEntry struct {
		norm   string
		result *types.SearchResult
	}

	entries := make([]normEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, normEntry{
			norm:   searchutil.NormalizeContent(r.Content),
			result: r,
		})
	}

	removed := make(map[int]bool)

	for i := 0; i < len(entries); i++ {
		if removed[i] {
			continue
		}
		for j := i + 1; j < len(entries); j++ {
			if removed[j] {
				continue
			}

			a, b := entries[i], entries[j]

			shortIdx, longIdx := i, j
			if len(a.norm) > len(b.norm) {
				shortIdx, longIdx = j, i
			}

			contained := searchutil.IsContentContained(
				entries[shortIdx].norm, entries[longIdx].norm,
			)

			if !contained {
				ratio := searchutil.ContentOverlapRatio(
					entries[shortIdx].result.Content,
					entries[longIdx].result.Content,
				)
				if ratio < overlapThreshold {
					continue
				}
			}

			victim := shortIdx
			if entries[shortIdx].result.Score > entries[longIdx].result.Score {
				victim = longIdx
			}
			removed[victim] = true

			keptIdx := i
			if victim == i {
				keptIdx = j
			}
			pipelineInfo(ctx, "Merge", "partial_overlap_drop", map[string]interface{}{
				"kept_id":    entries[keptIdx].result.ID,
				"dropped_id": entries[victim].result.ID,
				"contained":  contained,
			})
		}
	}

	out := make([]*types.SearchResult, 0, len(results)-len(removed))
	for i, e := range entries {
		if !removed[i] {
			out = append(out, e.result)
		}
	}
	return out
}

func logSearchScoreSample(ctx context.Context, action string, results []*types.SearchResult) {
	const maxLogRows = 8
	limit := min(maxLogRows, len(results))
	for i := 0; i < limit; i++ {
		r := results[i]
		pipelineInfo(ctx, "Search", action, map[string]interface{}{
			"index":      i,
			"chunk_id":   r.ID,
			"score":      fmt.Sprintf("%.4f", r.Score),
			"match_type": r.MatchType,
		})
	}
	if len(results) > limit {
		pipelineInfo(ctx, "Search", action+"_summary", map[string]interface{}{
			"total":     len(results),
			"logged":    limit,
			"truncated": len(results) - limit,
		})
	}
}

// searchByTargets performs KB searches using pre-computed SearchTargets.
// Targets sharing the same underlying embedding model (identified by model
// name + endpoint, not just model ID) are grouped so the query embedding is
// computed once per model AND all full-KB targets in a group are combined into
// a single retrieval call, reducing both embedding API calls and DB round-trips.
func (p *PluginSearch) searchByTargets(
	ctx context.Context,
	chatManage *types.ChatManage,
) []*types.SearchResult {
	if len(chatManage.SearchTargets) == 0 {
		return nil
	}

	queryText := strings.TrimSpace(chatManage.RewriteQuery)

	// Batch-fetch KB records to determine embedding model grouping.
	// On failure, all targets fall into an empty-key group and HybridSearch
	// computes the embedding per-KB (graceful degradation).
	kbIDs := make([]string, 0, len(chatManage.SearchTargets))
	for _, t := range chatManage.SearchTargets {
		kbIDs = append(kbIDs, t.KnowledgeBaseID)
	}
	var kbList []*types.KnowledgeBase
	kbMap := make(map[string]*types.KnowledgeBase)
	if kbs, err := p.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(ctx, kbIDs); err == nil {
		kbList = kbs
		for _, kb := range kbs {
			if kb != nil {
				kbMap[kb.ID] = kb
			}
		}
	} else {
		pipelineWarn(ctx, "Search", "batch_kb_fetch_error", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Resolve actual model identities (name + endpoint) so that cross-tenant
	// KBs backed by the same physical model share one embedding computation.
	modelKeyMap := p.knowledgeBaseService.ResolveEmbeddingModelKeys(ctx, kbList)

	groups := make(map[string][]*types.SearchTarget)
	for _, t := range chatManage.SearchTargets {
		key := modelKeyMap[t.KnowledgeBaseID] // empty string if unresolved
		groups[key] = append(groups[key], t)
	}

	pipelineInfo(ctx, "Search", "embedding_groups", map[string]interface{}{
		"total_targets": len(chatManage.SearchTargets),
		"unique_models": len(groups),
	})

	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []*types.SearchResult

	for modelKey, targets := range groups {
		wg.Add(1)
		go func(modelKey string, targets []*types.SearchTarget) {
			defer wg.Done()

			// Compute embedding once for this model group.
			var queryEmbedding []float32
			if modelKey != "" {
				emb, err := p.knowledgeBaseService.GetQueryEmbedding(ctx, targets[0].KnowledgeBaseID, queryText)
				if err != nil {
					pipelineWarn(ctx, "Search", "group_embed_error", map[string]interface{}{
						"model_key": modelKey,
						"kb_id":     targets[0].KnowledgeBaseID,
						"error":     err.Error(),
					})
				} else {
					queryEmbedding = emb
				}
			}

			// Separate full-KB targets (can be combined into one retrieval)
			// from specific-knowledge targets (need per-target direct loading).
			var fullKBIDs []string
			var knowledgeTargets []*types.SearchTarget
			for _, t := range targets {
				if t.Type == types.SearchTargetTypeKnowledgeBase {
					fullKBIDs = append(fullKBIDs, t.KnowledgeBaseID)
				} else {
					knowledgeTargets = append(knowledgeTargets, t)
				}
			}

			pipelineInfo(ctx, "Search", "group_plan", map[string]interface{}{
				"model_key":          modelKey,
				"combined_kb_count":  len(fullKBIDs),
				"individual_targets": len(knowledgeTargets),
				"vector_len":         len(queryEmbedding),
			})

			var innerWg sync.WaitGroup

			// Combined search: one HybridSearch call spanning all full-KB targets
			if len(fullKBIDs) > 0 {
				innerWg.Add(1)
				go func() {
					defer innerWg.Done()

					params := types.SearchParams{
						QueryText:             queryText,
						QueryEmbedding:        queryEmbedding,
						KnowledgeBaseIDs:      fullKBIDs,
						VectorThreshold:       chatManage.VectorThreshold,
						KeywordThreshold:      chatManage.KeywordThreshold,
						MatchCount:            chatManage.EmbeddingTopK,
						SkipContextEnrichment: true,
					}
					res, err := p.knowledgeBaseService.HybridSearch(ctx, fullKBIDs[0], params)
					if err != nil {
						pipelineWarn(ctx, "Search", "combined_kb_search_error", map[string]interface{}{
							"kb_ids": fullKBIDs,
							"error":  err.Error(),
						})
						return
					}
					pipelineInfo(ctx, "Search", "combined_kb_result", map[string]interface{}{
						"kb_ids":    fullKBIDs,
						"hit_count": len(res),
					})
					mu.Lock()
					results = append(results, res...)
					mu.Unlock()
				}()
			}

			// Individual search: per-target handling for specific-knowledge targets
			for _, target := range knowledgeTargets {
				innerWg.Add(1)
				go func(t *types.SearchTarget) {
					defer innerWg.Done()
					p.searchSingleTarget(ctx, chatManage, t, queryText, queryEmbedding, &mu, &results)
				}(target)
			}

			innerWg.Wait()
		}(modelKey, targets)
	}

	wg.Wait()

	pipelineInfo(ctx, "Search", "kb_result_summary", map[string]interface{}{
		"total_hits": len(results),
	})
	return results
}

// searchSingleTarget handles the search logic for a single SearchTarget
// with specific knowledge IDs, including direct chunk loading and HybridSearch.
func (p *PluginSearch) searchSingleTarget(
	ctx context.Context,
	chatManage *types.ChatManage,
	t *types.SearchTarget,
	queryText string,
	queryEmbedding []float32,
	mu *sync.Mutex,
	results *[]*types.SearchResult,
) {
	searchKnowledgeIDs := t.KnowledgeIDs

	if t.Type == types.SearchTargetTypeKnowledge {
		directResults, skippedIDs := p.tryDirectChunkLoading(ctx, chatManage.TenantID, t.KnowledgeIDs)

		if len(directResults) > 0 {
			for _, r := range directResults {
				r.KnowledgeBaseID = t.KnowledgeBaseID
			}
			pipelineInfo(ctx, "Search", "direct_load", map[string]interface{}{
				"kb_id":        t.KnowledgeBaseID,
				"loaded_count": len(directResults),
				"skipped_ids":  len(skippedIDs),
			})
			mu.Lock()
			*results = append(*results, directResults...)
			mu.Unlock()
		}

		if len(skippedIDs) == 0 && len(t.KnowledgeIDs) > 0 {
			return
		}
		searchKnowledgeIDs = skippedIDs
	}

	if t.Type == types.SearchTargetTypeKnowledge && len(searchKnowledgeIDs) == 0 {
		return
	}

	params := types.SearchParams{
		QueryText:             queryText,
		QueryEmbedding:        queryEmbedding,
		VectorThreshold:       chatManage.VectorThreshold,
		KeywordThreshold:      chatManage.KeywordThreshold,
		MatchCount:            chatManage.EmbeddingTopK,
		SkipContextEnrichment: true,
	}
	if t.Type == types.SearchTargetTypeKnowledge {
		params.KnowledgeIDs = searchKnowledgeIDs
	}
	res, err := p.knowledgeBaseService.HybridSearch(ctx, t.KnowledgeBaseID, params)
	if err != nil {
		pipelineWarn(ctx, "Search", "kb_search_error", map[string]interface{}{
			"kb_id":       t.KnowledgeBaseID,
			"target_type": t.Type,
			"query":       params.QueryText,
			"error":       err.Error(),
		})
		return
	}
	pipelineInfo(ctx, "Search", "kb_result", map[string]interface{}{
		"kb_id":       t.KnowledgeBaseID,
		"target_type": t.Type,
		"hit_count":   len(res),
	})
	mu.Lock()
	*results = append(*results, res...)
	mu.Unlock()
}

// tryDirectChunkLoading attempts to load chunks for given knowledge IDs directly
// Returns loaded results and a list of knowledge IDs that were skipped (e.g. due to size limits)
func (p *PluginSearch) tryDirectChunkLoading(ctx context.Context, tenantID uint64, knowledgeIDs []string) ([]*types.SearchResult, []string) {
	if len(knowledgeIDs) == 0 {
		return nil, nil
	}

	// Limit direct loading to avoid OOM or context overflow
	// 50 chunks * ~500 chars/chunk ~= 25k chars
	const maxTotalChunks = 50

	var allChunks []*types.Chunk
	var skippedIDs []string
	loadedKnowledgeIDs := make(map[string]bool)

	for _, kid := range knowledgeIDs {
		// Optimization: Check chunk count first if possible?
		chunks, err := p.chunkService.ListChunksByKnowledgeID(ctx, kid)
		if err != nil {
			logger.Warnf(ctx, "DirectLoad: Failed to list chunks for knowledge %s: %v", kid, err)
			skippedIDs = append(skippedIDs, kid)
			continue
		}

		if len(allChunks)+len(chunks) > maxTotalChunks {
			logger.Infof(ctx, "DirectLoad: Skipped knowledge %s due to size limit (%d + %d > %d)",
				kid, len(allChunks), len(chunks), maxTotalChunks)
			skippedIDs = append(skippedIDs, kid)
			continue
		}
		allChunks = append(allChunks, chunks...)
		loadedKnowledgeIDs[kid] = true
	}

	if len(allChunks) == 0 {
		return nil, skippedIDs
	}

	// Fetch Knowledge metadata
	var uniqueKIDs []string
	for kid := range loadedKnowledgeIDs {
		uniqueKIDs = append(uniqueKIDs, kid)
	}

	knowledgeMap := make(map[string]*types.Knowledge)
	if len(uniqueKIDs) > 0 {
		knowledges, err := p.knowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, uniqueKIDs)
		if err != nil {
			logger.Warnf(ctx, "DirectLoad: Failed to fetch knowledge batch: %v", err)
			// Continue without metadata
		} else {
			for _, k := range knowledges {
				knowledgeMap[k.ID] = k
			}
		}
	}

	var results []*types.SearchResult
	for _, chunk := range allChunks {
		res := &types.SearchResult{
			ID:            chunk.ID,
			Content:       chunk.Content,
			Score:         1.0, // Maximum score for direct matches
			KnowledgeID:   chunk.KnowledgeID,
			ChunkIndex:    chunk.ChunkIndex,
			MatchType:     types.MatchTypeDirectLoad,
			ChunkType:     string(chunk.ChunkType),
			ParentChunkID: chunk.ParentChunkID,
			ImageInfo:     chunk.ImageInfo,
			ChunkMetadata: chunk.Metadata,
			StartAt:       chunk.StartAt,
			EndAt:         chunk.EndAt,
		}

		if k, ok := knowledgeMap[chunk.KnowledgeID]; ok {
			res.KnowledgeTitle = k.Title
			res.KnowledgeFilename = k.FileName
			res.KnowledgeSource = k.Source
			res.KnowledgeChannel = k.Channel
			res.Metadata = k.GetMetadata()
		}

		results = append(results, res)
	}

	searchutil.EnrichSearchResultsImageInfo(ctx, p.chunkService.GetRepository(), tenantID, results)

	return results, skippedIDs
}

// searchWebIfEnabled executes web search when enabled and returns converted results
func (p *PluginSearch) searchWebIfEnabled(ctx context.Context, chatManage *types.ChatManage) []*types.SearchResult {
	if !chatManage.WebSearchEnabled || p.webSearchService == nil || p.tenantService == nil {
		return nil
	}
	tenant, _ := types.TenantInfoFromContext(ctx)
	providerID := chatManage.WebSearchProviderID

	if providerID == "" {
		pipelineWarn(ctx, "Search", "web_config_missing", map[string]interface{}{
			"tenant_id": chatManage.TenantID,
		})
		return nil
	}

	webConfig := types.EffectiveWebSearchConfig(nil)
	if tenant != nil {
		webConfig = types.EffectiveWebSearchConfig(tenant.WebSearchConfig)
	}

	// Apply agent-level web search overrides
	if chatManage.WebSearchMaxResults > 0 {
		webConfig.MaxResults = chatManage.WebSearchMaxResults
	}

	pipelineInfo(ctx, "Search", "web_request", map[string]interface{}{
		"tenant_id":   chatManage.TenantID,
		"provider_id": providerID,
	})
	webCtx, webSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "web_search",
		Input: map[string]interface{}{
			"provider_id": providerID,
			"query":       chatManage.RewriteQuery,
			"max_results": webConfig.MaxResults,
		},
	})
	webResults, err := p.webSearchService.Search(webCtx, providerID, webConfig, chatManage.RewriteQuery)
	webSpan.Finish(map[string]interface{}{
		"hit_count": len(webResults),
	}, nil, err)
	if err != nil {
		pipelineWarn(ctx, "Search", "web_search_error", map[string]interface{}{
			"tenant_id": chatManage.TenantID,
			"error":     err.Error(),
		})
		return nil
	}
	// Build questions using RewriteQuery only
	// questions := []string{strings.TrimSpace(chatManage.RewriteQuery)}
	// Load session-scoped temp KB state from Redis using WebSearchStateRepository
	// tempKBID, seen, ids := p.webSearchStateService.GetWebSearchTempKBState(ctx, chatManage.SessionID)
	// compressed, kbID, newSeen, newIDs, err := p.webSearchService.CompressWithRAG(
	// 	ctx, chatManage.SessionID, tempKBID, questions, webResults, webConfig,
	// 	p.knowledgeBaseService, p.knowledgeService, seen, ids,
	// )
	// if err != nil {
	// 	pipelineWarn(ctx, "Search", "web_compress_error", map[string]interface{}{
	// 		"error": err.Error(),
	// 	})
	// } else {
	// 	webResults = compressed
	// 	// Persist temp KB state back into Redis using WebSearchStateRepository
	// 	p.webSearchStateService.SaveWebSearchTempKBState(ctx, chatManage.SessionID, kbID, newSeen, newIDs)
	// }
	res := searchutil.ConvertWebSearchResults(webResults)
	pipelineInfo(ctx, "Search", "web_hits", map[string]interface{}{
		"hit_count": len(res),
	})
	return res
}
