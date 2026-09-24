package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/reranking"
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
		"error codes, product names and exact phrases. A base without the requested index is searched with the " +
		"index it has; the result reports that as a mode fallback.\n" +
		"In hybrid and semantic mode write query as one complete natural-language question or statement that " +
		"names the subject and what you want to know (\"Why does Echo use open-weight models to cut cost?\"), " +
		"not a keyword list (\"Echo open-weight cost\"): results are ranked by a relevance model that scores " +
		"meaning. In keyword mode write the exact terms. Pass knowledge_base_ids to focus on the bases whose " +
		"profile fits the question. When nothing passes the relevance check, rephrase with terms the documents " +
		"would use; repeating the same query in another mode usually does not help. For identifiers, error " +
		"codes or exact names retry with mode=keyword.\n" +
		"Every chunk carries a cN handle and belongs to a dN document. Use read_document(id=dN) to read the " +
		"surrounding context or the whole document. Retrieval matches chunk text; the relevance model also " +
		"sees the document title. To find a document by its title or file name use list_documents(keyword=...).",
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "A complete natural-language question, not a keyword list; exact terms when mode is keyword",
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
	// patternFilter, when set, keeps only results whose text matches it.
	// It is not exposed to the model; the MCP grep_chunks endpoint uses it
	// to keep grep semantics on top of index-backed retrieval.
	patternFilter *regexp.Regexp
}

// WithPatternFilter restricts results to chunks whose text matches re. The
// candidate pool is widened to the maximum limit before filtering.
func (t *SearchKnowledgeTool) WithPatternFilter(re *regexp.Regexp) *SearchKnowledgeTool {
	t.patternFilter = re
	return t
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
	kbModes, msg := resolveKBSearchModes(mode, kbList)
	if msg != "" {
		return &types.ToolResult{Success: false, Error: msg}, fmt.Errorf("%s", msg)
	}
	kbTypeMap := make(map[string]string, len(kbList))
	for _, kb := range kbList {
		if kb != nil {
			kbTypeMap[kb.ID] = kb.Type
		}
	}

	retrievalLimit := limit
	if t.patternFilter != nil {
		retrievalLimit = searchKnowledgeMaxLimit
	}
	topK, vectorThreshold, keywordThreshold := t.retrievalParams(retrievalLimit)
	logger.Infof(ctx,
		"[Tool][SearchKnowledge] query=%q mode=%s limit=%d top_k=%d targets=%d kbs=%d",
		query, mode, limit, topK, len(searchTargets), len(kbIDs))

	allResults := t.concurrentSearchByTargets(ctx, query, mode, kbModes, searchTargets, kbList,
		topK, vectorThreshold, keywordThreshold, kbTypeMap)

	deduplicated := t.deduplicateResults(allResults)

	ranked := deduplicated
	rerankRejected := 0
	if t.rerankModel != nil && len(deduplicated) > 0 {
		reranked, err := t.rerankResults(ctx, query, deduplicated)
		if err != nil {
			logger.Warnf(ctx, "[Tool][SearchKnowledge] Rerank failed, using retrieval order: %v", err)
		} else {
			ranked = reranked
			if len(reranked) == 0 {
				rerankRejected = len(deduplicated)
			}
		}
	}

	if len(ranked) > 0 {
		if selected := selectMMR(ctx, ranked, min(len(ranked), retrievalLimit)); len(selected) > 0 {
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
	if t.patternFilter != nil {
		matched := final[:0]
		for _, r := range final {
			if t.patternFilter.MatchString(t.getEnrichedPassage(ctx, r.SearchResult)) {
				matched = append(matched, r)
			}
		}
		final = matched
	}
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

	result := t.formatOutput(ctx, final, kbIDs, query, mode)
	annotateModeFallback(result.Data, mode, kbModes)
	if rerankRejected > 0 {
		result.Data["rerank_rejected"] = rerankRejected
	}
	if len(final) == 0 {
		result.Output = emptySearchStatement(query, result.Data, len(kbIDs))
	}
	return result, nil
}

// emptySearchStatement describes an empty result, including any mode
// fallback, so the model does not read "no semantic neighbours" as "the
// exact term does not occur". When retrieval found candidates and the rerank
// model rejected all of them, it says so: repeating the same query in
// another mode is usually wasted, but keyword mode can still surface a
// different candidate set for identifiers and exact terms.
func emptySearchStatement(query string, data map[string]interface{}, kbCount int) string {
	mode, _ := data["mode"].(string)
	msg := fmt.Sprintf("No matching chunks for %q (mode=%s) in %d knowledge base(s).", query, mode, kbCount)
	if rejected, _ := data["rerank_rejected"].(int); rejected > 0 {
		msg = fmt.Sprintf("No chunk passed the relevance check for %q (mode=%s) in %d knowledge base(s): "+
			"retrieval found %d candidate chunks but the relevance model scored none of them as answering the "+
			"query. Repeating the same query in another mode usually will not help; rephrase with terms the "+
			"documents would use. For identifiers, error codes or exact names retry with mode=keyword.",
			query, mode, kbCount, rejected)
	}
	fallbacks, _ := data["mode_fallbacks"].([]map[string]interface{})
	for _, fb := range fallbacks {
		msg += fmt.Sprintf(" Knowledge base %v was searched with mode=%v (%v).",
			fb["knowledge_base_id"], fb["mode"], fb["reason"])
	}
	return msg
}

// kbSearchMode is the retrieval path used for one knowledge base.
type kbSearchMode struct {
	mode     string
	fallback bool   // mode differs from the requested one
	reason   string // why the requested mode could not be served
}

// resolveKBSearchModes decides, per knowledge base, which retrieval path
// serves the requested mode. A base that lacks the requested index falls
// back to the one it has (FAQ bases have no keyword index; vector-only bases
// have no keyword index; keyword-only bases have no vector index) instead of
// failing the whole call. Bases with no chunk index at all (wiki-only /
// graph-only) are left out. The call is refused only when no base in scope
// can be searched. An empty kbs list (lookup failed) resolves to nil, which
// callers treat as "use the requested mode everywhere".
func resolveKBSearchModes(mode string, kbs []*types.KnowledgeBase) (map[string]kbSearchMode, string) {
	if len(kbs) == 0 {
		return nil, ""
	}
	modes := make(map[string]kbSearchMode, len(kbs))
	for _, kb := range kbs {
		if kb == nil {
			continue
		}
		hasVector := kb.IsVectorEnabled()
		hasKeyword := kb.IsKeywordEnabled() && kb.Type != types.KnowledgeBaseTypeFAQ
		switch {
		case !hasVector && !hasKeyword:
			continue
		case mode == SearchModeKeyword && !hasKeyword:
			reason := "no keyword index"
			if kb.Type == types.KnowledgeBaseTypeFAQ {
				reason = "FAQ bases are indexed for semantic search only"
			}
			modes[kb.ID] = kbSearchMode{mode: SearchModeSemantic, fallback: true, reason: reason}
		case mode == SearchModeSemantic && !hasVector:
			modes[kb.ID] = kbSearchMode{mode: SearchModeKeyword, fallback: true, reason: "no vector index"}
		default:
			modes[kb.ID] = kbSearchMode{mode: mode}
		}
	}
	if len(modes) == 0 {
		return nil, "none of the selected knowledge bases has a chunk index; use a wiki or graph tool for this scope"
	}
	return modes, ""
}

// annotateModeFallback records in the result which bases were searched with
// a different mode than requested, so neither the model nor the UI mistakes
// a semantic fallback for literal keyword matches.
func annotateModeFallback(data map[string]interface{}, requested string, kbModes map[string]kbSearchMode) {
	if data == nil {
		return
	}
	data["requested_mode"] = requested
	var fallbacks []map[string]interface{}
	used := make(map[string]bool)
	ids := make([]string, 0, len(kbModes))
	for kbID := range kbModes {
		ids = append(ids, kbID)
	}
	sort.Strings(ids)
	for _, kbID := range ids {
		m := kbModes[kbID]
		used[m.mode] = true
		if m.fallback {
			fallbacks = append(fallbacks, map[string]interface{}{
				"knowledge_base_id": kbID, "mode": m.mode, "reason": m.reason,
			})
		}
	}
	if len(fallbacks) == 0 {
		return
	}
	data["mode_fallbacks"] = fallbacks
	if len(used) == 1 {
		for effective := range used {
			data["mode"] = effective
		}
	}
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
	kbModes map[string]kbSearchMode,
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
	knownKBs := make(map[string]bool, len(kbList))
	for _, kb := range kbList {
		if kb != nil {
			knownKBs[kb.ID] = true
		}
	}
	modeFor := func(kbID string) (string, bool) {
		if m, ok := kbModes[kbID]; ok {
			return m.mode, true
		}
		// Unknown to the lookup: keep it so HybridSearch surfaces the real error.
		return mode, !knownKBs[kbID]
	}
	filteredTargets := make(types.SearchTargets, 0, len(searchTargets))
	for _, st := range searchTargets {
		if st == nil || st.KnowledgeBaseID == "" {
			continue
		}
		if _, ok := modeFor(st.KnowledgeBaseID); ok {
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
	collect := func(rows []*types.SearchResult, usedMode string) {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			allResults = append(allResults, &searchResultWithMeta{
				SearchResult:      r,
				SourceQuery:       query,
				QueryType:         usedMode,
				KnowledgeBaseID:   r.KnowledgeBaseID,
				KnowledgeBaseType: kbTypeMap[r.KnowledgeBaseID],
			})
		}
	}

	for modelKey, targets := range groups {
		wg.Add(1)
		go func(modelKey string, targets []*types.SearchTarget) {
			defer wg.Done()

			needsEmbedding := false
			for _, st := range targets {
				if m, _ := modeFor(st.KnowledgeBaseID); m != SearchModeKeyword {
					needsEmbedding = true
					break
				}
			}
			// Compute embedding once for this (model, query) pair
			var queryEmbedding []float32
			if modelKey != "" && needsEmbedding {
				emb, err := t.knowledgeBaseService.GetQueryEmbedding(ctx, targets[0].KnowledgeBaseID, query)
				if err != nil {
					logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to pre-compute embedding for model %s: %v",
						modelKey, err)
				} else {
					queryEmbedding = emb
				}
			}

			// Separate full-KB targets (combinable per retrieval mode) from
			// specific-knowledge targets.
			fullKBIDsByMode := make(map[string][]string)
			var knowledgeTargets []*types.SearchTarget
			for _, st := range targets {
				if st.Type == types.SearchTargetTypeKnowledgeBase && len(st.TagIDs) == 0 {
					m, _ := modeFor(st.KnowledgeBaseID)
					fullKBIDsByMode[m] = append(fullKBIDsByMode[m], st.KnowledgeBaseID)
				} else {
					knowledgeTargets = append(knowledgeTargets, st)
				}
			}

			var innerWg sync.WaitGroup
			for usedMode, fullKBIDs := range fullKBIDsByMode {
				innerWg.Add(1)
				go func(usedMode string, fullKBIDs []string) {
					defer innerWg.Done()
					kbResults, err := t.knowledgeBaseService.HybridSearch(ctx, fullKBIDs[0], types.SearchParams{
						QueryText:            query,
						QueryEmbedding:       queryEmbedding,
						KnowledgeBaseIDs:     fullKBIDs,
						MatchCount:           topK,
						VectorThreshold:      vectorThreshold,
						KeywordThreshold:     keywordThreshold,
						DisableVectorMatch:   usedMode == SearchModeKeyword,
						DisableKeywordsMatch: usedMode == SearchModeSemantic,
					})
					if err != nil {
						logger.Warnf(ctx, "[Tool][SearchKnowledge] Combined search failed for KBs %v: %v",
							fullKBIDs, err)
						return
					}
					collect(kbResults, usedMode)
				}(usedMode, fullKBIDs)
			}

			for _, target := range knowledgeTargets {
				st := target
				innerWg.Add(1)
				go func() {
					defer innerWg.Done()
					usedMode, _ := modeFor(st.KnowledgeBaseID)
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
						DisableVectorMatch:   usedMode == SearchModeKeyword,
						DisableKeywordsMatch: usedMode == SearchModeSemantic,
					})
					if err != nil {
						logger.Warnf(ctx, "[Tool][SearchKnowledge] Failed to search KB %s: %v", st.KnowledgeBaseID, err)
						return
					}
					collect(kbResults, usedMode)
				}()
			}
			innerWg.Wait()
		}(modelKey, targets)
	}
	wg.Wait()
	return allResults
}

// rerankResults scores all search results (including FAQ entries) with the
// configured rerank model through the shared rerank stage, keeping those
// that pass the threshold with composite scores, best first.
//
// A failed rerank call degrades to the raw retrieval order. An empty result
// is kept empty: the shared stage already preserves the top candidate down to
// its fallback floor, so reaching zero means even the best match is below it.
func (t *SearchKnowledgeTool) rerankResults(
	ctx context.Context,
	query string,
	results []*searchResultWithMeta,
) ([]*searchResultWithMeta, error) {
	if len(results) == 0 || t.rerankModel == nil {
		return results, nil
	}

	rows := make([]*types.SearchResult, len(results))
	for i, r := range results {
		rows[i] = r.SearchResult
	}
	threshold := t.rerankThreshold()
	res := reranking.Rerank(ctx, t.rerankModel, query, rows, reranking.Options{
		Threshold:        threshold,
		FallbackMinScore: reranking.FallbackMinScore(t.searchTargets.HasRecallThresholdOverride()),
	})
	if res.Diagnostics.Outcome == types.RerankOutcomeModelError {
		logger.Warnf(ctx, "[Tool][SearchKnowledge] Rerank model failed, using raw retrieval results: %s",
			res.Diagnostics.Error)
		return results, nil
	}

	reranked := make([]*searchResultWithMeta, 0, len(res.Results))
	for i, row := range res.Results {
		withMeta := *results[res.Indices[i]]
		withMeta.SearchResult = row
		reranked = append(reranked, &withMeta)
	}
	logger.Infof(ctx, "[Tool][SearchKnowledge] Reranked %d/%d results above threshold %.2f (%s)",
		len(reranked), len(results), threshold, res.Diagnostics.Outcome)
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

func (t *SearchKnowledgeTool) rerankThreshold() float64 {
	if t.config != nil && t.config.Conversation != nil && t.config.Conversation.RerankThreshold > 0 {
		return t.config.Conversation.RerankThreshold
	}
	return reranking.DefaultThreshold
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
		if snippet == "" && t.patternFilter != nil {
			snippet = extractSnippetRegex(result.Content, []*regexp.Regexp{t.patternFilter})
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

// getEnrichedPassage merges the raw chunk content with image captions / OCR
// text. The pattern filter matches against it, so unlike the rerank passage
// it keeps markdown, URLs and other literal text a grep may target.
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

// selectMMR reduces redundancy among results through the shared MMR stage.
func selectMMR(ctx context.Context, results []*searchResultWithMeta, k int) []*searchResultWithMeta {
	rows := make([]*types.SearchResult, len(results))
	for i, r := range results {
		rows[i] = r.SearchResult
	}
	picks := reranking.SelectMMR(ctx, rows, k, reranking.DefaultMMRLambda)
	if picks == nil {
		return nil
	}
	selected := make([]*searchResultWithMeta, len(picks))
	for i, p := range picks {
		selected[i] = results[p]
	}
	return selected
}
