package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Retrieval modes accepted by search_knowledge.
const (
	SearchModeHybrid   = "hybrid"
	SearchModeSemantic = "semantic"
	SearchModeKeyword  = "keyword"
)

const (
	searchKnowledgeDefaultLimit = 10
	searchKnowledgeMaxLimit     = 30
)

var searchKnowledgeTool = BaseTool{
	name: ToolSearchKnowledge,
	description: "Search the knowledge bases in scope and return the most relevant chunks with their content.\n" +
		"mode \"hybrid\" (default) combines semantic and keyword retrieval. \"semantic\" ranks by meaning and " +
		"tolerates paraphrase. \"keyword\" matches the literal terms and is the right choice for identifiers, " +
		"error codes, product names and exact phrases.\n" +
		"Write query as one natural-language question or a short phrase; in keyword mode write the exact terms. " +
		"Pass knowledge_base_ids to focus on the bases whose profile fits the question. Run the tool again with a " +
		"different query or mode when the results are thin.\n" +
		"Every chunk carries a cN handle and belongs to a dN document. Use read_document(id=dN) to read the " +
		"surrounding context or the whole document. Searches match chunk text only; to find a document by its " +
		"title or file name use list_documents(keyword=...).",
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "A natural-language question or short phrase; exact terms when mode is keyword",
      "minLength": 1
    },
    "mode": {
      "type": "string",
      "enum": ["hybrid", "semantic", "keyword"],
      "description": "Retrieval mode (default hybrid)"
    },
    "knowledge_base_ids": {
      "type": "array",
      "description": "Optional bN knowledge-base handles from the runtime context to restrict the search",
      "items": { "type": "string" },
      "maxItems": 10
    },
    "limit": {
      "type": "integer",
      "description": "Maximum chunks to return (default 10, max 30)",
      "minimum": 1,
      "maximum": 30
    }
  },
  "required": ["query"]
}`),
}

// SearchKnowledgeInput defines the input parameters for search_knowledge.
type SearchKnowledgeInput struct {
	Query            string   `json:"query"`
	Mode             string   `json:"mode,omitempty"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
	Limit            int      `json:"limit,omitempty"`
}

// searchResultWithMeta wraps search result with metadata about which query matched it
type searchResultWithMeta struct {
	*types.SearchResult
	SourceQuery       string
	QueryType         string // retrieval path that produced the row
	KnowledgeBaseID   string // ID of the knowledge base this result came from
	KnowledgeBaseType string // Type of the knowledge base (document, faq, etc.)
}

// SearchKnowledgeTool searches chunk-indexed knowledge bases in semantic,
// keyword or hybrid mode and returns reranked, de-duplicated chunks.
type SearchKnowledgeTool struct {
	BaseTool
	knowledgeBaseService interfaces.KnowledgeBaseService
	knowledgeService     interfaces.KnowledgeService
	chunkService         interfaces.ChunkService
	searchTargets        types.SearchTargets // Pre-computed unified search targets
	rerankModel          rerank.Reranker
	config               *config.Config // Global config for fallback values
}

// NewSearchKnowledgeTool creates a new search_knowledge tool.
func NewSearchKnowledgeTool(
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	searchTargets types.SearchTargets,
	rerankModel rerank.Reranker,
	cfg *config.Config,
) *SearchKnowledgeTool {
	return &SearchKnowledgeTool{
		BaseTool:             searchKnowledgeTool,
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		chunkService:         chunkService,
		searchTargets:        searchTargets,
		rerankModel:          rerankModel,
		config:               cfg,
	}
}

// normalizeSearchMode validates the mode argument and applies the default.
func normalizeSearchMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "":
		return SearchModeHybrid, nil
	case SearchModeHybrid, SearchModeSemantic, SearchModeKeyword:
		return mode, nil
	default:
		return "", fmt.Errorf("mode must be one of hybrid, semantic, keyword (got %q)", mode)
	}
}

// clampSearchLimit applies the default and upper bound for limit.
func clampSearchLimit(limit int) int {
	if limit <= 0 {
		return searchKnowledgeDefaultLimit
	}
	if limit > searchKnowledgeMaxLimit {
		return searchKnowledgeMaxLimit
	}
	return limit
}

// Execute executes the search.
func (t *SearchKnowledgeTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input SearchKnowledgeInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to parse args: %v", err)}, err
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return &types.ToolResult{Success: false, Error: "query is required"}, fmt.Errorf("no query provided")
	}
	mode, err := normalizeSearchMode(input.Mode)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	limit := clampSearchLimit(input.Limit)

	// Optional KB filter: reject hallucinated or out-of-scope handles.
	searchTargets := t.searchTargets
	if len(input.KnowledgeBaseIDs) > 0 {
		if err := validateKnowledgeBaseIDsInSearchTargets(t.searchTargets, input.KnowledgeBaseIDs); err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, err
		}
		wanted := make(map[string]bool, len(input.KnowledgeBaseIDs))
		for _, kbID := range input.KnowledgeBaseIDs {
			wanted[kbID] = true
		}
		var filtered types.SearchTargets
		for _, target := range t.searchTargets {
			if target != nil && wanted[target.KnowledgeBaseID] {
				filtered = append(filtered, target)
			}
		}
		searchTargets = filtered
	}
	if len(searchTargets) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "no knowledge base is in scope for this search",
		}, fmt.Errorf("no search targets available")
	}

	kbIDs := searchTargets.GetAllKnowledgeBaseIDs()
	kbList, err := t.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(ctx, kbIDs)
	if err != nil {
		logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to load knowledge bases %v: %v", kbIDs, err)
	}
	if msg := searchModeUnavailableReason(mode, kbList); msg != "" {
		return &types.ToolResult{Success: false, Error: msg}, fmt.Errorf("%s", msg)
	}
	kbTypeMap := make(map[string]string, len(kbList))
	for _, kb := range kbList {
		if kb != nil {
			kbTypeMap[kb.ID] = kb.Type
		}
	}

	topK, vectorThreshold, keywordThreshold := t.retrievalParams(limit)
	logger.Infof(ctx,
		"[Tool][SearchKnowledge] query=%q mode=%s limit=%d top_k=%d targets=%d kbs=%d",
		query, mode, limit, topK, len(searchTargets), len(kbIDs))

	allResults := t.concurrentSearchByTargets(ctx, query, mode, searchTargets, kbList,
		topK, vectorThreshold, keywordThreshold, kbTypeMap)

	deduplicated := t.deduplicateResults(allResults)

	ranked := deduplicated
	if t.rerankModel != nil && len(deduplicated) > 0 {
		reranked, err := t.rerankResults(ctx, query, deduplicated)
		if err != nil {
			logger.Warnf(ctx, "[Tool][SearchKnowledge] Rerank failed, using retrieval order: %v", err)
		} else {
			ranked = reranked
		}
	}

	if len(ranked) > 0 {
		mmrK := len(ranked)
		if mmrK > limit {
			mmrK = limit
		}
		if selected := t.applyMMR(ctx, ranked, mmrK, 0.7); len(selected) > 0 {
			ranked = selected
		}
	}

	final := t.deduplicateResults(ranked)
	sort.Slice(final, func(i, j int) bool {
		if final[i].Score != final[j].Score {
			return final[i].Score > final[j].Score
		}
		return final[i].KnowledgeID < final[j].KnowledgeID
	})
	if len(final) > limit {
		final = final[:limit]
	}

	// Enrich image info for search results (lazy-loaded from child image chunks)
	if t.chunkService != nil && len(final) > 0 {
		byTenant := make(map[uint64][]*types.SearchResult)
		for _, r := range final {
			tid := t.searchTargets.GetTenantIDForKB(r.KnowledgeBaseID)
			if tid == 0 {
				continue
			}
			byTenant[tid] = append(byTenant[tid], r.SearchResult)
		}
		for tid, batch := range byTenant {
			searchutil.EnrichSearchResultsImageInfo(ctx, t.chunkService.GetRepository(), tid, batch)
		}
	}

	return t.formatOutput(ctx, final, kbIDs, query, mode), nil
}

// searchModeUnavailableReason explains why a mode cannot run against the
// selected knowledge bases, or returns "" when at least one KB supports it.
// An unknown KB list (nil) is treated as capable so that a transient lookup
// failure surfaces as an empty retrieval rather than a misleading refusal.
func searchModeUnavailableReason(mode string, kbs []*types.KnowledgeBase) string {
	if len(kbs) == 0 {
		return ""
	}
	var hasVector, hasKeyword bool
	for _, kb := range kbs {
		if kb == nil {
			continue
		}
		if kb.IsVectorEnabled() {
			hasVector = true
		}
		if kb.IsKeywordEnabled() && kb.Type != types.KnowledgeBaseTypeFAQ {
			hasKeyword = true
		}
	}
	switch mode {
	case SearchModeKeyword:
		if !hasKeyword {
			return "keyword mode is unavailable: none of the selected knowledge bases has a keyword index " +
				"(FAQ bases are semantic-only). Use mode=hybrid or mode=semantic."
		}
	case SearchModeSemantic:
		if !hasVector {
			return "semantic mode is unavailable: none of the selected knowledge bases has a vector index. " +
				"Use mode=keyword."
		}
	default:
		if !hasVector && !hasKeyword {
			return "none of the selected knowledge bases has a chunk index; use a wiki or graph tool for this scope"
		}
	}
	return ""
}

// retrievalParams resolves the candidate pool size and recall thresholds from
// the global configuration, with hard-coded fallbacks.
func (t *SearchKnowledgeTool) retrievalParams(limit int) (topK int, vectorThreshold, keywordThreshold float64) {
	if t.config != nil && t.config.Conversation != nil {
		topK = t.config.Conversation.EmbeddingTopK
		vectorThreshold = t.config.Conversation.VectorThreshold
		keywordThreshold = t.config.Conversation.KeywordThreshold
	}
	if topK <= 0 {
		topK = searchKnowledgeMaxLimit
	}
	if topK < limit {
		topK = limit
	}
	if vectorThreshold == 0 {
		vectorThreshold = 0.6
	}
	if keywordThreshold == 0 {
		keywordThreshold = 0.5
	}
	return topK, vectorThreshold, keywordThreshold
}

// concurrentSearchByTargets executes retrieval using pre-computed search targets.
// Targets sharing the same underlying embedding model (identified by model name + endpoint)
// are grouped so the query embedding is computed once per model, and all
// full-KB targets in a group are combined into a single retrieval call.
func (t *SearchKnowledgeTool) concurrentSearchByTargets(
	ctx context.Context,
	query string,
	mode string,
	searchTargets types.SearchTargets,
	kbList []*types.KnowledgeBase,
	topK int,
	vectorThreshold, keywordThreshold float64,
	kbTypeMap map[string]string,
) []*searchResultWithMeta {
	// Filter out non-searchable KBs (wiki-only / graph-only). Feeding a
	// wiki-only KB into HybridSearch causes spurious "model ID cannot be
	// empty" errors because such KBs have no EmbeddingModelID configured.
	// KBs that we couldn't fetch (not in kbList) are kept so the downstream
	// HybridSearch path can still surface the real error.
	searchableKBs := make(map[string]bool, len(kbList))
	knownKBs := make(map[string]bool, len(kbList))
	for _, kb := range kbList {
		if kb == nil {
			continue
		}
		knownKBs[kb.ID] = true
		if kb.IsVectorEnabled() || kb.IsKeywordEnabled() {
			searchableKBs[kb.ID] = true
		}
	}
	filteredTargets := make(types.SearchTargets, 0, len(searchTargets))
	for _, st := range searchTargets {
		if st == nil || st.KnowledgeBaseID == "" {
			continue
		}
		if searchableKBs[st.KnowledgeBaseID] || !knownKBs[st.KnowledgeBaseID] {
			filteredTargets = append(filteredTargets, st)
			continue
		}
		logger.Infof(ctx, "[Tool][SearchKnowledge] Skipping non-searchable KB %s (no vector/keyword index)",
			st.KnowledgeBaseID)
	}
	if len(filteredTargets) == 0 {
		return nil
	}
	searchTargets = filteredTargets

	disableVector := mode == SearchModeKeyword
	disableKeywords := mode == SearchModeSemantic

	// Resolve actual model identities (name + endpoint) for cross-tenant grouping
	modelKeyMap := t.knowledgeBaseService.ResolveEmbeddingModelKeys(ctx, kbList)

	groups := make(map[string][]*types.SearchTarget)
	for _, st := range searchTargets {
		key := modelKeyMap[st.KnowledgeBaseID]
		groups[key] = append(groups[key], st)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	allResults := make([]*searchResultWithMeta, 0)
	collect := func(rows []*types.SearchResult) {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			allResults = append(allResults, &searchResultWithMeta{
				SearchResult:      r,
				SourceQuery:       query,
				QueryType:         mode,
				KnowledgeBaseID:   r.KnowledgeBaseID,
				KnowledgeBaseType: kbTypeMap[r.KnowledgeBaseID],
			})
		}
	}

	for modelKey, targets := range groups {
		wg.Add(1)
		go func(modelKey string, targets []*types.SearchTarget) {
			defer wg.Done()

			// Compute embedding once for this (model, query) pair
			var queryEmbedding []float32
			if modelKey != "" && !disableVector {
				emb, err := t.knowledgeBaseService.GetQueryEmbedding(ctx, targets[0].KnowledgeBaseID, query)
				if err != nil {
					logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to pre-compute embedding for model %s: %v",
						modelKey, err)
				} else {
					queryEmbedding = emb
				}
			}

			// Separate full-KB targets (combinable) from specific-knowledge targets
			var fullKBIDs []string
			var knowledgeTargets []*types.SearchTarget
			for _, st := range targets {
				if st.Type == types.SearchTargetTypeKnowledgeBase && len(st.TagIDs) == 0 {
					fullKBIDs = append(fullKBIDs, st.KnowledgeBaseID)
				} else {
					knowledgeTargets = append(knowledgeTargets, st)
				}
			}

			var innerWg sync.WaitGroup
			if len(fullKBIDs) > 0 {
				innerWg.Add(1)
				go func() {
					defer innerWg.Done()
					kbResults, err := t.knowledgeBaseService.HybridSearch(ctx, fullKBIDs[0], types.SearchParams{
						QueryText:            query,
						QueryEmbedding:       queryEmbedding,
						KnowledgeBaseIDs:     fullKBIDs,
						MatchCount:           topK,
						VectorThreshold:      vectorThreshold,
						KeywordThreshold:     keywordThreshold,
						DisableVectorMatch:   disableVector,
						DisableKeywordsMatch: disableKeywords,
					})
					if err != nil {
						logger.Warnf(ctx, "[Tool][SearchKnowledge] Combined search failed for KBs %v: %v",
							fullKBIDs, err)
						return
					}
					collect(kbResults)
				}()
			}

			for _, target := range knowledgeTargets {
				st := target
				innerWg.Add(1)
				go func() {
					defer innerWg.Done()
					stVectorThreshold, stKeywordThreshold := st.RecallThresholds(vectorThreshold, keywordThreshold)
					kbResults, err := t.knowledgeBaseService.HybridSearch(ctx, st.KnowledgeBaseID, types.SearchParams{
						QueryText:            query,
						QueryEmbedding:       queryEmbedding,
						MatchCount:           topK,
						VectorThreshold:      stVectorThreshold,
						KeywordThreshold:     stKeywordThreshold,
						KnowledgeIDs:         st.KnowledgeIDs,
						TagIDs:               st.TagIDs,
						ScopeTagIDs:          st.ScopeTagIDs,
						DisableVectorMatch:   disableVector,
						DisableKeywordsMatch: disableKeywords,
					})
					if err != nil {
						logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to search KB %s: %v", st.KnowledgeBaseID, err)
						return
					}
					collect(kbResults)
				}()
			}
			innerWg.Wait()
		}(modelKey, targets)
	}
	wg.Wait()
	return allResults
}

// rerankResults applies reranking to all search results (including FAQ entries)
// using the configured rerank model, then filters by threshold and applies
// composite scoring so MMR/sorting uses a single score scale.
//
// A failed rerank call degrades to the raw retrieval order. An empty result
// after threshold filtering is kept empty: filterRerankRankResults already
// preserves the top candidate down to agentRerankFallbackMinScore, so reaching
// zero means even the best match is below that floor.
func (t *SearchKnowledgeTool) rerankResults(
	ctx context.Context,
	query string,
	results []*searchResultWithMeta,
) ([]*searchResultWithMeta, error) {
	if len(results) == 0 || t.rerankModel == nil {
		return results, nil
	}

	rankResults, err := t.rerankScores(ctx, query, results)
	if err != nil {
		logger.Warnf(ctx, "[Tool][SearchKnowledge] Rerank model failed, using raw retrieval results: %v", err)
		return results, nil
	}

	threshold := t.rerankThreshold()
	reranked := t.applyModelRerankScores(
		results,
		rankResults,
		threshold,
		t.searchTargets.HasRecallThresholdOverride(),
	)
	logger.Infof(ctx, "[Tool][SearchKnowledge] Reranked %d/%d results above threshold %.2f",
		len(reranked), len(results), threshold)
	return reranked, nil
}

func (t *SearchKnowledgeTool) getFAQMetadata(
	ctx context.Context,
	chunkID string,
	cache map[string]*types.FAQChunkMetadata,
) (*types.FAQChunkMetadata, error) {
	if chunkID == "" || t.chunkService == nil {
		return nil, nil
	}
	if meta, ok := cache[chunkID]; ok {
		return meta, nil
	}
	chunk, err := t.chunkService.GetChunkByID(ctx, chunkID)
	if err != nil {
		cache[chunkID] = nil
		return nil, err
	}
	if chunk == nil {
		cache[chunkID] = nil
		return nil, nil
	}
	meta, err := chunk.FAQMetadata()
	if err != nil {
		cache[chunkID] = nil
		return nil, err
	}
	cache[chunkID] = meta
	return meta, nil
}

// rerankScores scores the candidates with the configured rerank model and
// returns the raw relevance scores, leaving threshold filtering and composite
// scoring to the caller.
func (t *SearchKnowledgeTool) rerankScores(
	ctx context.Context,
	query string,
	results []*searchResultWithMeta,
) ([]rerank.RankResult, error) {
	passages := make([]string, len(results))
	for i, result := range results {
		passages[i] = t.getEnrichedPassage(ctx, result.SearchResult)
	}
	rerankResp, err := t.rerankModel.Rerank(ctx, query, passages)
	if err != nil {
		return nil, fmt.Errorf("rerank call failed: %w", err)
	}
	return rerankResp, nil
}

func (t *SearchKnowledgeTool) rerankThreshold() float64 {
	if t.config != nil && t.config.Conversation != nil && t.config.Conversation.RerankThreshold > 0 {
		return t.config.Conversation.RerankThreshold
	}
	return 0.3
}

const agentRerankFallbackMinScore = 0.15

func filterRerankRankResults(
	rankResults []rerank.RankResult,
	threshold float64,
	preserveTop bool,
) []rerank.RankResult {
	if len(rankResults) == 0 {
		return nil
	}
	filtered := make([]rerank.RankResult, 0, len(rankResults))
	for _, r := range rankResults {
		if r.RelevanceScore >= threshold {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		top := rankResults[0]
		for _, r := range rankResults[1:] {
			if r.RelevanceScore > top.RelevanceScore {
				top = r
			}
		}
		if preserveTop || top.RelevanceScore >= agentRerankFallbackMinScore {
			return []rerank.RankResult{top}
		}
	}
	return filtered
}

func (t *SearchKnowledgeTool) applyModelRerankScores(
	originals []*searchResultWithMeta,
	rankResults []rerank.RankResult,
	threshold float64,
	preserveTop bool,
) []*searchResultWithMeta {
	filtered := filterRerankRankResults(rankResults, threshold, preserveTop)
	out := make([]*searchResultWithMeta, 0, len(filtered))
	for _, rr := range filtered {
		if rr.Index < 0 || rr.Index >= len(originals) {
			continue
		}
		newResult := *originals[rr.Index]
		baseScore := newResult.Score
		modelScore := rr.RelevanceScore
		newResult.Score = t.compositeScore(&newResult, modelScore, baseScore)
		out = append(out, &newResult)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}

// deduplicateResults removes duplicate chunks, keeping the first occurrence.
// Uses multiple keys (ID, parent chunk ID, knowledge+index) and a content
// signature for near-duplicate detection. Input order is preserved.
func (t *SearchKnowledgeTool) deduplicateResults(results []*searchResultWithMeta) []*searchResultWithMeta {
	seen := make(map[string]bool)
	contentSig := make(map[string]bool)
	uniqueResults := make([]*searchResultWithMeta, 0, len(results))

	for _, r := range results {
		if r == nil || r.SearchResult == nil {
			continue
		}
		keys := []string{r.ID}
		if r.ParentChunkID != "" {
			keys = append(keys, "parent:"+r.ParentChunkID)
		}
		if r.KnowledgeID != "" {
			keys = append(keys, fmt.Sprintf("kb:%s#%d", r.KnowledgeID, r.ChunkIndex))
		}
		dup := false
		for _, k := range keys {
			if seen[k] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		sig := searchutil.BuildContentSignature(r.Content)
		if sig != "" {
			if contentSig[sig] {
				continue
			}
			contentSig[sig] = true
		}
		for _, k := range keys {
			seen[k] = true
		}
		uniqueResults = append(uniqueResults, r)
	}
	return uniqueResults
}

// writeKnowledgeMetadataHeader emits document-scoped metadata once per
// knowledge item. Chunk entries keep only chunk-specific content so repeated
// results from the same document do not waste context.
func writeKnowledgeMetadataHeader(ob *strings.Builder, results []*searchResultWithMeta) {
	seen := make(map[string]struct{}, len(results))
	hasMetadata := false
	var documents strings.Builder
	for _, result := range results {
		if result == nil || result.SearchResult == nil || result.KnowledgeID == "" ||
			result.KnowledgeCustomMetadata == "" {
			continue
		}
		if _, ok := seen[result.KnowledgeID]; ok {
			continue
		}
		seen[result.KnowledgeID] = struct{}{}
		hasMetadata = true
		fmt.Fprintf(&documents,
			"<document knowledge_id=\"%s\" knowledge_base_id=\"%s\" title=\"%s\">\n",
			xmlEscape(result.KnowledgeID),
			xmlEscape(result.KnowledgeBaseID),
			xmlEscape(result.KnowledgeTitle),
		)
		fmt.Fprintf(&documents, "<metadata>%s</metadata>\n", xmlEscape(result.KnowledgeCustomMetadata))
		documents.WriteString("</document>\n")
	}
	if !hasMetadata {
		return
	}
	ob.WriteString("<documents>\n")
	ob.WriteString(documents.String())
	ob.WriteString("</documents>\n")
}

// formatOutput builds the structured Data (the source of truth for the UI and
// the model rendering) and a compact XML Output for logs.
func (t *SearchKnowledgeTool) formatOutput(
	ctx context.Context,
	results []*searchResultWithMeta,
	kbsToSearch []string,
	query string,
	mode string,
) *types.ToolResult {
	data := map[string]interface{}{
		"knowledge_base_ids": kbsToSearch,
		"query":              query,
		"queries":            []string{query}, // legacy alias for older frontends
		"mode":               mode,
		"results":            []interface{}{},
		"count":              0,
		"display_type":       "search_results",
	}
	if len(results) == 0 {
		return &types.ToolResult{
			Success: true,
			Output: fmt.Sprintf("No matching chunks for %q (mode=%s) in %d knowledge base(s).",
				query, mode, len(kbsToSearch)),
			Data: data,
		}
	}

	kbCounts := make(map[string]int)
	for _, r := range results {
		kbCounts[r.KnowledgeBaseID]++
	}

	var ob strings.Builder
	fmt.Fprintf(&ob, "<search_results count=\"%d\" mode=\"%s\">\n", len(results), xmlEscape(mode))
	fmt.Fprintf(&ob, "<query>%s</query>\n", xmlEscape(query))
	writeKnowledgeMetadataHeader(&ob, results)

	formattedResults := make([]map[string]interface{}, 0, len(results))
	faqMetadataCache := make(map[string]*types.FAQChunkMetadata)
	queries := []string{query}

	for i, result := range results {
		var faqMeta *types.FAQChunkMetadata
		if result.KnowledgeBaseType == types.KnowledgeBaseTypeFAQ {
			meta, err := t.getFAQMetadata(ctx, result.ID, faqMetadataCache)
			if err != nil {
				logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to load FAQ metadata for chunk %s: %v",
					result.ID, err)
			} else {
				faqMeta = meta
			}
		}
		isFAQ := faqMeta != nil

		snippet := ""
		if isFAQ {
			snippet = faqMatchSnippetFromQueries(faqMeta, queries)
		}
		if snippet == "" {
			snippet = extractSnippetForQueries(result.Content, queries)
		}

		tag := "chunk"
		idAttr := fmt.Sprintf("chunk_id=\"%s\" chunk_index=\"%d\" knowledge_id=\"%s\"",
			xmlEscape(result.ID), result.ChunkIndex, xmlEscape(result.KnowledgeID))
		if isFAQ {
			tag = "faq"
			idAttr = fmt.Sprintf("faq_id=\"%s\" index=\"%d\"", xmlEscape(result.ID), result.ChunkIndex)
		}
		fmt.Fprintf(&ob, "<%s rank=\"%d\" %s knowledge_base_id=\"%s\" knowledge_title=\"%s\" score=\"%.3f\">\n",
			tag, i+1, idAttr, xmlEscape(result.KnowledgeBaseID), xmlEscape(result.KnowledgeTitle), result.Score)
		if snippet != "" {
			fmt.Fprintf(&ob, "<match_snippet>%s</match_snippet>\n", xmlEscape(snippet))
		}
		fmt.Fprintf(&ob, "<content>%s</content>\n", result.Content)
		if result.ImageInfo != "" {
			var imageInfos []types.ImageInfo
			if err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos); err == nil {
				for _, img := range imageInfos {
					if md := searchutil.BuildImageInfoMarkdownWithURL(img.URL, &img); md != "" {
						ob.WriteString(md)
						ob.WriteString("\n")
					}
				}
			}
		}
		if isFAQ {
			writeFAQFieldsXML(&ob, faqMeta)
		}
		fmt.Fprintf(&ob, "</%s>\n", tag)

		row := map[string]interface{}{
			"result_index":        i + 1,
			"content":             result.Content,
			"knowledge_id":        result.KnowledgeID,
			"knowledge_base_id":   result.KnowledgeBaseID,
			"knowledge_title":     result.KnowledgeTitle,
			"knowledge_metadata":  result.KnowledgeCustomMetadata,
			"match_type":          result.MatchType,
			"match_snippet":       snippet,
			"score":               result.Score,
			"source_query":        result.SourceQuery,
			"query_type":          result.QueryType,
			"knowledge_base_type": result.KnowledgeBaseType,
		}
		if images := chunkImageList(result.ImageInfo); len(images) > 0 {
			row["images"] = images
		}
		if isFAQ {
			row["faq_id"] = result.ID
			row["index"] = result.ChunkIndex
			if faqMeta.StandardQuestion != "" {
				row["faq_standard_question"] = faqMeta.StandardQuestion
			}
			appendSimilarQuestionsToChunkData(row, faqMeta.SimilarQuestions)
			if len(faqMeta.Answers) > 0 {
				row["faq_answers"] = faqMeta.Answers
			}
		} else {
			row["chunk_id"] = result.ID
			row["chunk_index"] = result.ChunkIndex
		}
		formattedResults = append(formattedResults, row)
	}
	ob.WriteString("</search_results>")

	data["results"] = formattedResults
	data["count"] = len(formattedResults)
	data["kb_counts"] = kbCounts
	return &types.ToolResult{Success: true, Output: ob.String(), Data: data}
}

// getEnrichedPassage merges chunk content with image captions / OCR text so
// the reranker and MMR see the same evidence the model will.
func (t *SearchKnowledgeTool) getEnrichedPassage(ctx context.Context, result *types.SearchResult) string {
	if result.ImageInfo == "" {
		return result.Content
	}
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos); err != nil {
		logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to parse image info: %v", err)
		return result.Content
	}
	if len(imageInfos) == 0 {
		return result.Content
	}
	var imageTexts []string
	for _, img := range imageInfos {
		if img.Caption != "" {
			imageTexts = append(imageTexts, fmt.Sprintf("Image Caption: %s", img.Caption))
		}
		if img.OCRText != "" {
			imageTexts = append(imageTexts, fmt.Sprintf("Image Text: %s", img.OCRText))
		}
	}
	if len(imageTexts) == 0 {
		return result.Content
	}
	combinedText := result.Content
	if combinedText != "" {
		combinedText += "\n\n"
	}
	return combinedText + strings.Join(imageTexts, "\n")
}

// compositeScore calculates a composite score considering multiple factors
func (t *SearchKnowledgeTool) compositeScore(
	result *searchResultWithMeta,
	modelScore, baseScore float64,
) float64 {
	// Source weight: web_search results get slightly lower weight
	sourceWeight := 1.0
	if strings.ToLower(result.KnowledgeSource) == "web_search" {
		sourceWeight = 0.95
	}

	// Position prior: slightly favor chunks earlier in the document
	positionPrior := 1.0
	if result.StartAt >= 0 && result.EndAt > result.StartAt {
		positionRatio := 1.0 - float64(result.StartAt)/float64(result.EndAt+1)
		positionPrior += t.clampFloat(positionRatio, -0.05, 0.05)
	}

	composite := 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight
	composite *= positionPrior
	if composite < 0 {
		composite = 0
	}
	if composite > 1 {
		composite = 1
	}
	return composite
}

func (t *SearchKnowledgeTool) clampFloat(v, minV, maxV float64) float64 {
	return searchutil.ClampFloat(v, minV, maxV)
}

// applyMMR applies Maximal Marginal Relevance to reduce redundancy.
func (t *SearchKnowledgeTool) applyMMR(
	ctx context.Context,
	results []*searchResultWithMeta,
	k int,
	lambda float64,
) []*searchResultWithMeta {
	if k <= 0 || len(results) == 0 {
		return nil
	}

	selected := make([]*searchResultWithMeta, 0, k)
	candidates := make([]*searchResultWithMeta, len(results))
	copy(candidates, results)

	tokenSets := make([]map[string]struct{}, len(candidates))
	for i, r := range candidates {
		tokenSets[i] = t.tokenizeSimple(t.getEnrichedPassage(ctx, r.SearchResult))
	}

	// Incremental form: maxRedundancy[i] caches candidate i's maximum jaccard
	// against everything selected so far, so each round only needs one
	// comparison per remaining candidate. Selection output is identical to
	// the naive form, including tie-breaking.
	maxRedundancy := make([]float64, len(candidates))
	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0
		for i, r := range candidates {
			mmr := lambda*r.Score - (1.0-lambda)*maxRedundancy[i]
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}
		selected = append(selected, candidates[bestIdx])
		chosenTokens := tokenSets[bestIdx]
		candidates = append(candidates[:bestIdx], candidates[bestIdx+1:]...)
		tokenSets = append(tokenSets[:bestIdx], tokenSets[bestIdx+1:]...)
		maxRedundancy = append(maxRedundancy[:bestIdx], maxRedundancy[bestIdx+1:]...)
		for i := range candidates {
			maxRedundancy[i] = math.Max(maxRedundancy[i], t.jaccard(tokenSets[i], chosenTokens))
		}
	}
	return selected
}

func (t *SearchKnowledgeTool) tokenizeSimple(text string) map[string]struct{} {
	return searchutil.TokenizeSimple(text)
}

func (t *SearchKnowledgeTool) jaccard(a, b map[string]struct{}) float64 {
	return searchutil.Jaccard(a, b)
}
