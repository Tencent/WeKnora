package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var knowledgeSearchTool = BaseTool{
	name: ToolKnowledgeSearch,
	description: `Semantic/vector search tool for retrieving knowledge by meaning, intent, and conceptual relevance.

This tool uses embeddings to understand the user's query and find semantically similar content across knowledge base chunks.

## Purpose
Designed for high-level understanding tasks, such as:
- conceptual explanations
- topic overviews
- reasoning-based information needs
- contextual or intent-driven retrieval
- queries that cannot be answered with literal keyword matching

The tool searches by MEANING rather than exact text. It identifies chunks that are conceptually relevant even when the wording differs.

## What the Tool Does NOT Do
- Does NOT perform exact keyword matching
- Does NOT search for specific named entities
- Should NOT be used for literal lookup tasks
- Should NOT receive long raw text or user messages as queries
- Should NOT be used to locate specific strings or error codes

For literal/keyword/entity search, another tool should be used.

## Required Input Behavior
"queries" must contain **1–5 short, well-formed semantic questions or conceptual statements** that clearly express the meaning the model is trying to retrieve.

Each query should represent a **concept, idea, topic, explanation, or intent**, such as:
- abstract topics
- definitions
- mechanisms
- best practices
- comparisons
- how/why questions

Avoid:
- keyword lists
- raw text from user messages
- full paragraphs
- unprocessed input

## Examples of valid query shapes (not content):
- "What is the main idea of..."
- "How does X work in general?"
- "Explain the purpose of..."
- "What are the key principles behind..."
- "Overview of ..."

## Parameters
- queries (required): 1–5 semantic questions or conceptual statements.
  These should reflect the meaning or topic you want embeddings to capture.
- knowledge_base_ids (optional): limit the search scope.

## Output
Returns chunks ranked by semantic similarity, reranked when applicable.  
Results represent conceptual relevance, not literal keyword overlap.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "queries": {
      "type": "array",
      "description": "REQUIRED: 1-5 semantic questions/topics (e.g., ['What is RAG?', 'RAG benefits'])",
      "items": {
        "type": "string"
      },
      "minItems": 1,
      "maxItems": 5
    },
    "knowledge_base_ids": {
      "type": "array",
      "description": "Optional: KB IDs to search",
      "items": {
        "type": "string"
      },
      "minItems": 0,
      "maxItems": 10
    }
  },
  "required": ["queries"]
}`),
}

// KnowledgeSearchInput defines the input parameters for knowledge search tool
type KnowledgeSearchInput struct {
	Queries          []string `json:"queries"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
}

// searchResultWithMeta wraps search result with metadata about which query matched it
type searchResultWithMeta struct {
	*types.SearchResult
	SourceQuery       string
	QueryType         string // "vector" or "keyword"
	KnowledgeBaseID   string // ID of the knowledge base this result came from
	KnowledgeBaseType string // Type of the knowledge base (document, faq, etc.)
}

// KnowledgeSearchTool searches knowledge bases with flexible query modes.
// seenChunks lets repeated calls in the same session surface previously-
// returned chunks in a compact form (mirroring wiki_search's de-duping UX)
// so the LLM doesn't burn tokens re-reading identical content.
type KnowledgeSearchTool struct {
	BaseTool
	knowledgeBaseService interfaces.KnowledgeBaseService
	knowledgeService     interfaces.KnowledgeService
	chunkService         interfaces.ChunkService
	searchTargets        types.SearchTargets // Pre-computed unified search targets
	rerankModel          rerank.Reranker
	chatModel            chat.Chat      // Optional chat model for LLM-based reranking
	config               *config.Config // Global config for fallback values

	seenMu     sync.Mutex
	seenChunks map[string]bool
}

// NewKnowledgeSearchTool creates a new knowledge search tool
func NewKnowledgeSearchTool(
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	searchTargets types.SearchTargets,
	rerankModel rerank.Reranker,
	chatModel chat.Chat,
	cfg *config.Config,
) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{
		BaseTool:             knowledgeSearchTool,
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		chunkService:         chunkService,
		searchTargets:        searchTargets,
		rerankModel:          rerankModel,
		chatModel:            chatModel,
		config:               cfg,
		seenChunks:           make(map[string]bool),
	}
}

// Execute executes the knowledge search tool
func (t *KnowledgeSearchTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Execute started")

	// Parse args from json.RawMessage
	var input KnowledgeSearchInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][KnowledgeSearch] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Log input arguments
	argsJSON, _ := json.MarshalIndent(input, "", "  ")
	logger.Debugf(ctx, "[Tool][KnowledgeSearch] Input args:\n%s", string(argsJSON))

	// Determine which KBs to search - user can optionally filter to specific KBs
	var userSpecifiedKBs []string
	if len(input.KnowledgeBaseIDs) > 0 {
		userSpecifiedKBs = input.KnowledgeBaseIDs
		logger.Infof(ctx, "[Tool][KnowledgeSearch] User specified %d knowledge bases: %v", len(userSpecifiedKBs), userSpecifiedKBs)
	}

	// Use pre-computed search targets, optionally filtered by user-specified KBs
	searchTargets := t.searchTargets
	if len(userSpecifiedKBs) > 0 {
		// Filter search targets to only include user-specified KBs
		userKBSet := make(map[string]bool)
		for _, kbID := range userSpecifiedKBs {
			userKBSet[kbID] = true
		}
		var filteredTargets types.SearchTargets
		for _, target := range t.searchTargets {
			if userKBSet[target.KnowledgeBaseID] {
				filteredTargets = append(filteredTargets, target)
			}
		}
		searchTargets = filteredTargets
	}

	// Validate search targets
	if len(searchTargets) == 0 {
		logger.Errorf(ctx, "[Tool][KnowledgeSearch] No search targets available")
		return &types.ToolResult{
			Success: false,
			Error:   "no knowledge bases specified and no search targets configured",
		}, fmt.Errorf("no search targets available")
	}

	kbIDs := searchTargets.GetAllKnowledgeBaseIDs()
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Using %d search targets across %d KBs", len(searchTargets), len(kbIDs))

	// Parse query parameter
	queries := input.Queries

	// Validate: query must be provided
	if len(queries) == 0 {
		logger.Errorf(ctx, "[Tool][KnowledgeSearch] No queries provided")
		return &types.ToolResult{
			Success: false,
			Error:   "queries parameter is required",
		}, fmt.Errorf("no queries provided")
	}

	logger.Infof(ctx, "[Tool][KnowledgeSearch] Queries: %v", queries)

	// Get search parameters from tenant conversation config, fallback to global config
	var topK int
	var vectorThreshold, keywordThreshold, minScore float64

	// Try to get from tenant conversation config
	if tenantVal := ctx.Value(types.TenantInfoContextKey); tenantVal != nil {
		if tenant, ok := tenantVal.(*types.Tenant); ok && tenant != nil && tenant.ConversationConfig != nil {
			cc := tenant.ConversationConfig
			if cc.EmbeddingTopK > 0 {
				topK = cc.EmbeddingTopK
			}
			if cc.VectorThreshold > 0 {
				vectorThreshold = cc.VectorThreshold
			}
			if cc.KeywordThreshold > 0 {
				keywordThreshold = cc.KeywordThreshold
			}
			// minScore is not in ConversationConfig, use default or config
			minScore = 0.3
		}
	}

	// Fallback to global config if not set
	if topK == 0 && t.config != nil {
		topK = t.config.Conversation.EmbeddingTopK
	}
	if vectorThreshold == 0 && t.config != nil {
		vectorThreshold = t.config.Conversation.VectorThreshold
	}
	if keywordThreshold == 0 && t.config != nil {
		keywordThreshold = t.config.Conversation.KeywordThreshold
	}

	// Final fallback to hardcoded defaults if config is not available
	if topK == 0 {
		topK = 5
	}
	if vectorThreshold == 0 {
		vectorThreshold = 0.6
	}
	if keywordThreshold == 0 {
		keywordThreshold = 0.5
	}
	if minScore == 0 {
		minScore = 0.3
	}

	logger.Infof(
		ctx,
		"[Tool][KnowledgeSearch] Search params: top_k=%d, vector_threshold=%.2f, keyword_threshold=%.2f, min_score=%.2f",
		topK,
		vectorThreshold,
		keywordThreshold,
		minScore,
	)

	// Execute concurrent search using pre-computed search targets
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Starting concurrent search with %d search targets",
		len(searchTargets))
	kbTypeMap := t.getKnowledgeBaseTypes(ctx, kbIDs)

	allResults := t.concurrentSearchByTargets(ctx, queries, searchTargets,
		topK, vectorThreshold, keywordThreshold, kbTypeMap)
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Concurrent search completed: %d raw results", len(allResults))

	// Note: HybridSearch now uses RRF (Reciprocal Rank Fusion) which produces normalized scores
	// RRF scores are in range [0, ~0.033] (max when rank=1 on both sides: 2/(60+1))
	// Threshold filtering is already done inside HybridSearch before RRF, so we skip it here

	// Deduplicate before reranking to reduce processing overhead
	deduplicatedBeforeRerank := t.deduplicateResults(allResults)

	// Apply ReRank if model is configured
	// Prefer rerankModel; fall back to chatModel (LLM-based reranking) if unavailable
	// Use first query for reranking (or combine all queries if needed)
	rerankQuery := ""
	if len(queries) > 0 {
		rerankQuery = queries[0]
		if len(queries) > 1 {
			// Combine multiple queries for reranking
			rerankQuery = strings.Join(queries, " ")
		}
	}

	// Variable to hold results through reranking and MMR stages
	var filteredResults []*searchResultWithMeta

	if (t.rerankModel != nil || t.chatModel != nil) && len(deduplicatedBeforeRerank) > 0 && rerankQuery != "" {
		logger.Infof(ctx, "[Tool][KnowledgeSearch] Applying rerank, input: %d results, queries: %v",
			len(deduplicatedBeforeRerank), queries)
		rerankedResults, err := t.rerankResults(ctx, rerankQuery, deduplicatedBeforeRerank)
		if err != nil {
			logger.Warnf(ctx, "[Tool][KnowledgeSearch] Rerank failed, using original results: %v", err)
			filteredResults = deduplicatedBeforeRerank
		} else {
			filteredResults = rerankedResults
			logger.Infof(ctx, "[Tool][KnowledgeSearch] Rerank completed successfully: %d results",
				len(filteredResults))
		}
	} else {
		// No reranking model available, use deduplicated results
		filteredResults = deduplicatedBeforeRerank
	}

	// Apply MMR (Maximal Marginal Relevance) to reduce redundancy and improve diversity
	// Note: composite scoring is already applied inside rerankResults
	if len(filteredResults) > 0 {
		// Calculate k for MMR: use min(len(results), max(1, topK))
		mmrK := len(filteredResults)
		if topK > 0 && mmrK > topK {
			mmrK = topK
		}
		if mmrK < 1 {
			mmrK = 1
		}
		// Apply MMR with lambda=0.7 (balance between relevance and diversity)
		logger.Debugf(
			ctx,
			"[Tool][KnowledgeSearch] Applying MMR: k=%d, lambda=0.7, input=%d results",
			mmrK,
			len(filteredResults),
		)
		mmrResults := t.applyMMR(ctx, filteredResults, mmrK, 0.7)
		if len(mmrResults) > 0 {
			filteredResults = mmrResults
			logger.Infof(ctx, "[Tool][KnowledgeSearch] MMR completed: %d results selected", len(filteredResults))
		} else {
			logger.Warnf(ctx, "[Tool][KnowledgeSearch] MMR returned no results, using original results")
		}
	}

	// Note: minScore filter is skipped because HybridSearch now uses RRF scores
	// RRF scores are in range [0, ~0.033], not [0, 1], so old thresholds don't apply
	// Threshold filtering is already done inside HybridSearch before RRF fusion

	// Final deduplication after rerank (in case rerank changed scores/order but duplicates remain)
	logger.Debugf(ctx, "[Tool][KnowledgeSearch] Final deduplication after rerank...")
	deduplicatedResults := t.deduplicateResults(filteredResults)
	logger.Infof(ctx, "[Tool][KnowledgeSearch] After final deduplication: %d results (from %d)",
		len(deduplicatedResults), len(filteredResults))

	// Sort results by score (descending)
	sort.Slice(deduplicatedResults, func(i, j int) bool {
		if deduplicatedResults[i].Score != deduplicatedResults[j].Score {
			return deduplicatedResults[i].Score > deduplicatedResults[j].Score
		}
		// If scores are equal, sort by knowledge ID for consistency
		return deduplicatedResults[i].KnowledgeID < deduplicatedResults[j].KnowledgeID
	})

	// Log top results
	if len(deduplicatedResults) > 0 {
		for i := 0; i < len(deduplicatedResults) && i < 5; i++ {
			r := deduplicatedResults[i]
			logger.Infof(ctx, "[Tool][KnowledgeSearch][Top %d] score=%.3f, type=%s, kb=%s, chunk_id=%s",
				i+1, r.Score, r.QueryType, r.KnowledgeID, r.ID)
		}
	}

	// Enrich image info for search results (lazy-loaded from child image chunks)
	if t.chunkService != nil && len(deduplicatedResults) > 0 {
		byTenant := make(map[uint64][]*types.SearchResult)
		for _, r := range deduplicatedResults {
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

	// Build output
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Formatting output with %d final results", len(deduplicatedResults))
	result, err := t.formatOutput(ctx, deduplicatedResults, kbIDs, queries)
	if err != nil {
		logger.Errorf(ctx, "[Tool][KnowledgeSearch] Failed to format output: %v", err)
		return result, err
	}
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Output: %s", result.Output)
	return result, nil
}

// getKnowledgeBaseTypes fetches knowledge base types for the given IDs
func (t *KnowledgeSearchTool) getKnowledgeBaseTypes(ctx context.Context, kbIDs []string) map[string]string {
	kbTypeMap := make(map[string]string, len(kbIDs))

	for _, kbID := range kbIDs {
		if kbID == "" {
			continue
		}
		if _, exists := kbTypeMap[kbID]; exists {
			continue
		}

		kb, err := t.knowledgeBaseService.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil {
			logger.Warnf(ctx, "[Tool][KnowledgeSearch] Failed to fetch knowledge base %s info: %v", kbID, err)
			continue
		}

		kbTypeMap[kbID] = kb.Type
	}

	return kbTypeMap
}

// concurrentSearchByTargets executes hybrid search using pre-computed search targets.
// Targets sharing the same underlying embedding model (identified by model name + endpoint)
// are grouped so the query embedding is computed once per (model, query) pair, and all
// full-KB targets in a group are combined into a single retrieval call.
func (t *KnowledgeSearchTool) concurrentSearchByTargets(
	ctx context.Context,
	queries []string,
	searchTargets types.SearchTargets,
	topK int,
	vectorThreshold, keywordThreshold float64,
	kbTypeMap map[string]string,
) []*searchResultWithMeta {
	// Batch-fetch KB records for embedding model grouping
	kbIDs := searchTargets.GetAllKnowledgeBaseIDs()
	var kbList []*types.KnowledgeBase
	if kbs, err := t.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(ctx, kbIDs); err == nil {
		kbList = kbs
	}

	// Filter out non-searchable KBs (wiki-only / graph-only). knowledge_search
	// can only serve KBs with vector or keyword indexing; feeding a wiki-only
	// KB into HybridSearch causes spurious "model ID cannot be empty" errors
	// because such KBs have no EmbeddingModelID configured. Such scopes
	// should be queried via wiki_search / graph tools instead.
	//
	// KBs that we couldn't fetch from the repo (not in kbList) are kept so
	// the downstream HybridSearch path can still surface the real error.
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
		if searchableKBs[st.KnowledgeBaseID] {
			filteredTargets = append(filteredTargets, st)
			continue
		}
		if knownKBs[st.KnowledgeBaseID] {
			logger.Infof(ctx, "[Tool][KnowledgeSearch] Skipping non-searchable KB %s (no vector/keyword index, likely wiki/graph-only)", st.KnowledgeBaseID)
			continue
		}
		// KB record unavailable; keep so downstream can surface real errors.
		filteredTargets = append(filteredTargets, st)
	}
	if len(filteredTargets) == 0 {
		logger.Infof(ctx, "[Tool][KnowledgeSearch] No searchable KBs in scope (all wiki/graph-only); skipping retrieval")
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

	for _, query := range queries {
		q := query
		for modelKey, targets := range groups {
			wg.Add(1)
			go func(q string, modelKey string, targets []*types.SearchTarget) {
				defer wg.Done()

				// Compute embedding once for this (model, query) pair
				var queryEmbedding []float32
				if modelKey != "" {
					emb, err := t.knowledgeBaseService.GetQueryEmbedding(ctx, targets[0].KnowledgeBaseID, q)
					if err != nil {
						logger.Warnf(ctx, "[Tool][KnowledgeSearch] Failed to pre-compute embedding for model %s: %v", modelKey, err)
					} else {
						queryEmbedding = emb
					}
				}

				// Separate full-KB targets (combinable) from specific-knowledge targets
				var fullKBIDs []string
				var knowledgeTargets []*types.SearchTarget
				for _, st := range targets {
					if st.Type == types.SearchTargetTypeKnowledgeBase {
						fullKBIDs = append(fullKBIDs, st.KnowledgeBaseID)
					} else {
						knowledgeTargets = append(knowledgeTargets, st)
					}
				}

				var innerWg sync.WaitGroup

				// Combined retrieval for all full-KB targets in this group
				if len(fullKBIDs) > 0 {
					innerWg.Add(1)
					go func() {
						defer innerWg.Done()
						searchParams := types.SearchParams{
							QueryText:        q,
							QueryEmbedding:   queryEmbedding,
							KnowledgeBaseIDs: fullKBIDs,
							MatchCount:       topK,
							VectorThreshold:  vectorThreshold,
							KeywordThreshold: keywordThreshold,
						}
						kbResults, err := t.knowledgeBaseService.HybridSearch(ctx, fullKBIDs[0], searchParams)
						if err != nil {
							logger.Warnf(ctx, "[Tool][KnowledgeSearch] Combined search failed for KBs %v: %v", fullKBIDs, err)
							return
						}
						mu.Lock()
						for _, r := range kbResults {
							allResults = append(allResults, &searchResultWithMeta{
								SearchResult:      r,
								SourceQuery:       q,
								QueryType:         "hybrid",
								KnowledgeBaseID:   r.KnowledgeBaseID,
								KnowledgeBaseType: kbTypeMap[r.KnowledgeBaseID],
							})
						}
						mu.Unlock()
					}()
				}

				// Individual retrieval for specific-knowledge targets
				for _, target := range knowledgeTargets {
					st := target
					innerWg.Add(1)
					go func() {
						defer innerWg.Done()
						searchParams := types.SearchParams{
							QueryText:        q,
							QueryEmbedding:   queryEmbedding,
							MatchCount:       topK,
							VectorThreshold:  vectorThreshold,
							KeywordThreshold: keywordThreshold,
							KnowledgeIDs:     st.KnowledgeIDs,
						}
						kbResults, err := t.knowledgeBaseService.HybridSearch(ctx, st.KnowledgeBaseID, searchParams)
						if err != nil {
							logger.Warnf(ctx, "[Tool][KnowledgeSearch] Failed to search KB %s: %v", st.KnowledgeBaseID, err)
							return
						}
						mu.Lock()
						for _, r := range kbResults {
							allResults = append(allResults, &searchResultWithMeta{
								SearchResult:      r,
								SourceQuery:       q,
								QueryType:         "hybrid",
								KnowledgeBaseID:   r.KnowledgeBaseID,
								KnowledgeBaseType: kbTypeMap[r.KnowledgeBaseID],
							})
						}
						mu.Unlock()
					}()
				}

				innerWg.Wait()
			}(q, modelKey, targets)
		}
	}
	wg.Wait()
	return allResults
}

// rerankResults applies reranking to search results using LLM prompt scoring or rerank model
func (t *KnowledgeSearchTool) rerankResults(
	ctx context.Context,
	query string,
	results []*searchResultWithMeta,
) ([]*searchResultWithMeta, error) {
	// Separate FAQ and normal results.
	// FAQ results keep original scores and bypass reranking model.
	faqResults := make([]*searchResultWithMeta, 0)
	rerankCandidates := make([]*searchResultWithMeta, 0, len(results))

	for _, result := range results {
		// Skip reranking for FAQ results (they are explicitly matched Q&A pairs)
		if result.KnowledgeBaseType == types.KnowledgeBaseTypeFAQ {
			faqResults = append(faqResults, result)
		} else {
			rerankCandidates = append(rerankCandidates, result)
		}
	}

	// If there are no candidates to rerank, return original list (already all FAQ)
	if len(rerankCandidates) == 0 {
		return results, nil
	}

	var (
		rerankedCandidates []*searchResultWithMeta
		err                error
	)

	// Apply reranking only to candidates
	// Try rerankModel first, fallback to chatModel if rerankModel fails or returns no results
	if t.rerankModel != nil {
		rerankedCandidates, err = t.rerankWithModel(ctx, query, rerankCandidates)
		// If rerankModel fails or returns no results, fallback to chatModel
		if err != nil || len(rerankedCandidates) == 0 {
			if err != nil {
				logger.Warnf(ctx, "[Tool][KnowledgeSearch] Rerank model failed, falling back to chat model: %v", err)
			} else {
				logger.Warnf(ctx, "[Tool][KnowledgeSearch] Rerank model returned no results, falling back to chat model")
			}
			// Reset error to allow fallback
			err = nil
			// Try chatModel if available
			if t.chatModel != nil {
				rerankedCandidates, err = t.rerankWithLLM(ctx, query, rerankCandidates)
			} else {
				// No fallback available, use original results
				rerankedCandidates = rerankCandidates
			}
		}
	} else if t.chatModel != nil {
		// No rerankModel, use chatModel directly
		rerankedCandidates, err = t.rerankWithLLM(ctx, query, rerankCandidates)
	} else {
		// No reranking available, use original results
		rerankedCandidates = rerankCandidates
	}

	if err != nil {
		return nil, err
	}

	// Apply composite scoring to reranked results
	logger.Debugf(ctx, "[Tool][KnowledgeSearch] Applying composite scoring")

	// Store base scores before composite scoring
	for _, result := range rerankedCandidates {
		baseScore := result.Score
		// Apply composite score
		result.Score = t.compositeScore(result, result.Score, baseScore)
	}

	// Combine FAQ results (with original order) and reranked candidates
	combined := make([]*searchResultWithMeta, 0, len(results))
	combined = append(combined, faqResults...)
	combined = append(combined, rerankedCandidates...)

	// Sort by score (descending) to keep consistent output order
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	return combined, nil
}

func (t *KnowledgeSearchTool) getFAQMetadata(
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

// rerankWithLLM uses LLM prompt to score and rerank search results
// Uses batch processing to handle large result sets efficiently
func (t *KnowledgeSearchTool) rerankWithLLM(
	ctx context.Context,
	query string,
	results []*searchResultWithMeta,
) ([]*searchResultWithMeta, error) {
	logger.Infof(ctx, "[Tool][KnowledgeSearch] Using LLM for reranking %d results", len(results))

	if len(results) == 0 {
		return results, nil
	}

	// Batch size: process 15 results at a time to balance quality and token usage
	// This prevents token overflow and improves processing efficiency
	const batchSize = 15
	const maxContentLength = 800 // Maximum characters per passage to avoid excessive tokens

	// Process in batches
	allScores := make([]float64, len(results))
	allReranked := make([]*searchResultWithMeta, 0, len(results))

	for batchStart := 0; batchStart < len(results); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(results) {
			batchEnd = len(results)
		}

		batch := results[batchStart:batchEnd]
		logger.Debugf(ctx, "[Tool][KnowledgeSearch] Processing rerank batch %d-%d of %d results",
			batchStart+1, batchEnd, len(results))

		// Build prompt with query and batch passages
		var passagesBuilder strings.Builder
		for i, result := range batch {
			// Get enriched passage (content + image info)
			enrichedContent := t.getEnrichedPassage(ctx, result.SearchResult)
			// Truncate content if too long to save tokens
			content := enrichedContent
			if len([]rune(content)) > maxContentLength {
				runes := []rune(content)
				content = string(runes[:maxContentLength]) + "..."
			}
			// Use clear separators to distinguish each passage
			if i > 0 {
				passagesBuilder.WriteString("\n")
			}
			passagesBuilder.WriteString("─────────────────────────────────────────────────────────────\n")
			passagesBuilder.WriteString(fmt.Sprintf("Passage %d:\n", i+1))
			passagesBuilder.WriteString("─────────────────────────────────────────────────────────────\n")
			passagesBuilder.WriteString(content + "\n")
		}

		// Optimized prompt focused on retrieval matching and reranking
		prompt := fmt.Sprintf(
			`You are a search result reranking expert. Your task is to evaluate how well each retrieved passage matches the user's search query and information need.

User Query: %s

Your task: Rerank these search results by evaluating their retrieval relevance - how well each passage answers or relates to the query.

Scoring Criteria (0.0 to 1.0):
- 1.0 (0.9-1.0): Directly answers the query, contains key information needed, highly relevant
- 0.8 (0.7-0.8): Strongly related, provides substantial relevant information
- 0.6 (0.5-0.6): Moderately related, contains some relevant information but may be incomplete
- 0.4 (0.3-0.4): Weakly related, minimal relevance to the query
- 0.2 (0.1-0.2): Barely related, mostly irrelevant
- 0.0 (0.0): Completely irrelevant, no relation to the query

Evaluation Factors:
1. Query-Answer Match: Does the passage directly address what the user is asking?
2. Information Completeness: Does it provide sufficient information to answer the query?
3. Semantic Relevance: Does the content semantically relate to the query intent?
4. Key Term Coverage: Does it cover important terms/concepts from the query?
5. Information Accuracy: Is the information accurate and trustworthy?

Retrieved Passages:
%s

IMPORTANT: Return exactly %d scores, one per line, in this exact format:
Passage 1: X.XX
Passage 2: X.XX
Passage 3: X.XX
...
Passage %d: X.XX

Output only the scores, no explanations or additional text.`,
			query,
			passagesBuilder.String(),
			len(batch),
			len(batch),
		)

		messages := []chat.Message{
			{
				Role:    "system",
				Content: "You are a professional search result reranking expert specializing in information retrieval. You evaluate how well retrieved passages match user queries in search scenarios. Focus on retrieval relevance: whether the passage answers the query, provides needed information, and matches the user's information need. Always respond with scores only, no explanations.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		}

		// Calculate appropriate max tokens based on batch size
		// Each score line is ~15 tokens, add buffer for safety
		maxTokens := len(batch)*20 + 100

		response, err := t.chatModel.Chat(ctx, messages, &chat.ChatOptions{
			Temperature: 0.1, // Low temperature for consistent scoring
			MaxTokens:   maxTokens,
		})
		if err != nil {
			logger.Warnf(ctx, "[Tool][KnowledgeSearch] LLM rerank batch %d-%d failed: %v, using original scores",
				batchStart+1, batchEnd, err)
			// Use original scores for this batch on error
			for i := batchStart; i < batchEnd; i++ {
				allScores[i] = results[i].Score
			}
			continue
		}

		logger.Infof(ctx, "[Tool][KnowledgeSearch] LLM rerank batch %d-%d response: %s",
			batchStart+1, batchEnd, response.Content)

		// Parse scores from response
		batchScores, err := t.parseScoresFromResponse(response.Content, len(batch))
		if err != nil {
			logger.Warnf(
				ctx,
				"[Tool][KnowledgeSearch] Failed to parse LLM scores for batch %d-%d: %v, using original scores",
				batchStart+1,
				batchEnd,
				err,
			)
			// Use original scores for this batch on parsing error
			for i := batchStart; i < batchEnd; i++ {
				allScores[i] = results[i].Score
			}
			continue
		}

		// Store scores for this batch
		for i, score := range batchScores {
			if batchStart+i < len(allScores) {
				allScores[batchStart+i] = score
			}
		}
	}

	// Create reranked results with new scores
	for i, result := range results {
		newResult := *result
		if i < len(allScores) {
			newResult.Score = allScores[i]
		}
		allReranked = append(allReranked, &newResult)
	}

	// Sort by new scores (descending)
	sort.Slice(allReranked, func(i, j int) bool {
		return allReranked[i].Score > allReranked[j].Score
	})

	logger.Infof(ctx, "[Tool][KnowledgeSearch] LLM reranked %d results from %d original results (processed in batches)",
		len(allReranked), len(results))
	return allReranked, nil
}

// parseScoresFromResponse parses scores from LLM response text
func (t *KnowledgeSearchTool) parseScoresFromResponse(responseText string, expectedCount int) ([]float64, error) {
	lines := strings.Split(strings.TrimSpace(responseText), "\n")
	scores := make([]float64, 0, expectedCount)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Try to extract score from various formats:
		// "Passage 1: 0.85"
		// "1: 0.85"
		// "0.85"
		// etc.
		parts := strings.Split(line, ":")
		var scoreStr string
		if len(parts) >= 2 {
			scoreStr = strings.TrimSpace(parts[len(parts)-1])
		} else {
			scoreStr = strings.TrimSpace(line)
		}

		// Remove any non-numeric characters except decimal point
		scoreStr = strings.TrimFunc(scoreStr, func(r rune) bool {
			return (r < '0' || r > '9') && r != '.'
		})

		if scoreStr == "" {
			continue
		}

		score, err := strconv.ParseFloat(scoreStr, 64)
		if err != nil {
			continue // Skip invalid scores
		}

		// Clamp score to [0.0, 1.0]
		if score < 0.0 {
			score = 0.0
		}
		if score > 1.0 {
			score = 1.0
		}

		scores = append(scores, score)
	}

	if len(scores) == 0 {
		return nil, fmt.Errorf("no valid scores found in response")
	}

	// If we got fewer scores than expected, pad with last score or 0.5
	for len(scores) < expectedCount {
		if len(scores) > 0 {
			scores = append(scores, scores[len(scores)-1])
		} else {
			scores = append(scores, 0.5)
		}
	}

	// Truncate if we got more scores than expected
	if len(scores) > expectedCount {
		scores = scores[:expectedCount]
	}

	return scores, nil
}

// rerankWithModel uses the rerank model for reranking (fallback)
func (t *KnowledgeSearchTool) rerankWithModel(
	ctx context.Context,
	query string,
	results []*searchResultWithMeta,
) ([]*searchResultWithMeta, error) {
	// Prepare passages for reranking (with enriched content including image info)
	passages := make([]string, len(results))
	for i, result := range results {
		passages[i] = t.getEnrichedPassage(ctx, result.SearchResult)
	}

	// Call rerank model
	rerankResp, err := t.rerankModel.Rerank(ctx, query, passages)
	if err != nil {
		return nil, fmt.Errorf("rerank call failed: %w", err)
	}

	// Map reranked results back with new scores
	reranked := make([]*searchResultWithMeta, 0, len(rerankResp))
	for _, rr := range rerankResp {
		if rr.Index >= 0 && rr.Index < len(results) {
			// Create new result with reranked score
			newResult := *results[rr.Index]
			newResult.Score = rr.RelevanceScore
			reranked = append(reranked, &newResult)
		}
	}

	logger.Infof(
		ctx,
		"[Tool][KnowledgeSearch] Reranked %d results from %d original results",
		len(reranked),
		len(results),
	)
	return reranked, nil
}

// deduplicateResults removes duplicate chunks, keeping the highest score
// Uses multiple keys (ID, parent chunk ID, knowledge+index) and content signature for deduplication
func (t *KnowledgeSearchTool) deduplicateResults(results []*searchResultWithMeta) []*searchResultWithMeta {
	seen := make(map[string]bool)
	contentSig := make(map[string]bool)
	uniqueResults := make([]*searchResultWithMeta, 0)

	for _, r := range results {
		// Build multiple keys for deduplication
		keys := []string{r.ID}
		if r.ParentChunkID != "" {
			keys = append(keys, "parent:"+r.ParentChunkID)
		}
		if r.KnowledgeID != "" {
			keys = append(keys, fmt.Sprintf("kb:%s#%d", r.KnowledgeID, r.ChunkIndex))
		}

		// Check if any key is already seen
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

		// Check content signature for near-duplicate content
		sig := t.buildContentSignature(r.Content)
		if sig != "" {
			if contentSig[sig] {
				continue
			}
			contentSig[sig] = true
		}

		// Mark all keys as seen
		for _, k := range keys {
			seen[k] = true
		}

		uniqueResults = append(uniqueResults, r)
	}

	// If we have duplicates by ID but different scores, keep the highest score
	// This handles cases where the same chunk appears multiple times with different scores
	seenByID := make(map[string]*searchResultWithMeta)
	for _, r := range uniqueResults {
		if existing, ok := seenByID[r.ID]; ok {
			// Keep the result with higher score
			if r.Score > existing.Score {
				seenByID[r.ID] = r
			}
		} else {
			seenByID[r.ID] = r
		}
	}

	// Convert back to slice
	deduplicated := make([]*searchResultWithMeta, 0, len(seenByID))
	for _, r := range seenByID {
		deduplicated = append(deduplicated, r)
	}

	return deduplicated
}

// buildContentSignature creates a normalized signature for content to detect near-duplicates
func (t *KnowledgeSearchTool) buildContentSignature(content string) string {
	return searchutil.BuildContentSignature(content)
}

// formatOutput formats the search results for display
func (t *KnowledgeSearchTool) formatOutput(
	ctx context.Context,
	results []*searchResultWithMeta,
	kbsToSearch []string,
	queries []string,
) (*types.ToolResult, error) {
	if len(results) == 0 {
		data := map[string]interface{}{
			"knowledge_base_ids": kbsToSearch,
			"results":            []interface{}{},
			"count":              0,
		}
		if len(queries) > 0 {
			data["queries"] = queries
		}
		output := fmt.Sprintf("No relevant content found in %d knowledge base(s).\n\n", len(kbsToSearch))
		output += "=== ⚠️ CRITICAL - Next Steps ===\n"
		output += "- ❌ DO NOT use training data or general knowledge to answer\n"
		output += "- ✅ If web_search is enabled: You MUST use web_search to find information\n"
		output += "- ✅ If web_search is disabled: State 'I couldn't find relevant information in the knowledge base'\n"
		output += "- NEVER fabricate or infer answers - ONLY use retrieved content\n"

		return &types.ToolResult{
			Success: true,
			Output:  output,
			Data:    data,
		}, nil
	}

	// Count results by KB
	kbCounts := make(map[string]int)
	for _, r := range results {
		kbCounts[r.KnowledgeID]++
	}

	// Format individual results as XML. Tag names are kept in sync with
	// wiki_search (`<search_results>`, per-entry element, `<query>`) so that
	// agents and downstream consumers see a single consistent shape across
	// all retrieval tools.
	var ob strings.Builder
	ob.WriteString(fmt.Sprintf("<search_results count=\"%d\">\n", len(results)))
	for _, q := range queries {
		ob.WriteString(fmt.Sprintf("<query>%s</query>\n", xmlEscape(q)))
	}

	formattedResults := make([]map[string]interface{}, 0, len(results))

	faqMetadataCache := make(map[string]*types.FAQChunkMetadata)

	knowledgeChunkMap := make(map[string]map[int]bool)
	knowledgeTotalMap := make(map[string]int64)
	knowledgeTitleMap := make(map[string]string)

	for i, result := range results {
		var faqMeta *types.FAQChunkMetadata
		if result.KnowledgeBaseType == types.KnowledgeBaseTypeFAQ {
			meta, err := t.getFAQMetadata(ctx, result.ID, faqMetadataCache)
			if err != nil {
				logger.Warnf(ctx, "[Tool][KnowledgeSearch] Failed to load FAQ metadata for chunk %s: %v", result.ID, err)
			} else {
				faqMeta = meta
			}
		}

		if knowledgeChunkMap[result.KnowledgeID] == nil {
			knowledgeChunkMap[result.KnowledgeID] = make(map[int]bool)
		}
		knowledgeChunkMap[result.KnowledgeID][result.ChunkIndex] = true
		knowledgeTitleMap[result.KnowledgeID] = result.KnowledgeTitle

		// Cache total chunk count per knowledge
		if _, exists := knowledgeTotalMap[result.KnowledgeID]; !exists {
			effectiveTenantID := t.searchTargets.GetTenantIDForKB(result.KnowledgeBaseID)
			if effectiveTenantID == 0 {
				logger.Warnf(ctx, "[Tool][KnowledgeSearch] KB %s not found in searchTargets, skipping chunk count", result.KnowledgeBaseID)
				knowledgeTotalMap[result.KnowledgeID] = 0
			} else {
				// Use the same chunk-type filter as list_knowledge_chunks so the
				// total reported here matches what list_knowledge_chunks can page
				// over. Mismatched filters previously let LLMs compute offsets
				// against an inflated/deflated total and page past the end.
				_, total, err := t.chunkService.GetRepository().ListPagedChunksByKnowledgeID(ctx,
					effectiveTenantID, result.KnowledgeID,
					&types.Pagination{Page: 1, PageSize: 1},
					[]types.ChunkType{types.ChunkTypeText, types.ChunkTypeFAQ}, "", "", "", "", "",
				)
				if err != nil {
					logger.Warnf(ctx, "[Tool][KnowledgeSearch] Failed to get total chunks for knowledge %s: %v", result.KnowledgeID, err)
					knowledgeTotalMap[result.KnowledgeID] = 0
				} else {
					knowledgeTotalMap[result.KnowledgeID] = total
				}
			}
		}

		t.seenMu.Lock()
		seen := t.seenChunks[result.ID]
		t.seenChunks[result.ID] = true
		t.seenMu.Unlock()

		if seen {
			// Compact rendering for chunks we already returned in a previous
			// knowledge_search call during this session. The model has the
			// content in context already, so re-emitting it only burns tokens.
			ob.WriteString(fmt.Sprintf(
				"<chunk rank=\"%d\" chunk_id=\"%s\" chunk_index=\"%d\" knowledge_id=\"%s\" knowledge_base_id=\"%s\" knowledge_title=\"%s\" score=\"%.3f\" source_query=\"%s\" already_seen=\"true\">\n",
				i+1,
				xmlEscape(result.ID),
				result.ChunkIndex,
				xmlEscape(result.KnowledgeID),
				xmlEscape(result.KnowledgeBaseID),
				xmlEscape(result.KnowledgeTitle),
				result.Score,
				xmlEscape(result.SourceQuery),
			))
			ob.WriteString("<note>(content omitted, already returned in a previous knowledge_search call this session)</note>\n")
			ob.WriteString("</chunk>\n")
		} else {
			ob.WriteString(fmt.Sprintf(
				"<chunk rank=\"%d\" chunk_id=\"%s\" chunk_index=\"%d\" knowledge_id=\"%s\" knowledge_base_id=\"%s\" knowledge_title=\"%s\" score=\"%.3f\" source_query=\"%s\">\n",
				i+1,
				xmlEscape(result.ID),
				result.ChunkIndex,
				xmlEscape(result.KnowledgeID),
				xmlEscape(result.KnowledgeBaseID),
				xmlEscape(result.KnowledgeTitle),
				result.Score,
				xmlEscape(result.SourceQuery),
			))
			if snippet := extractSnippetForQueries(result.Content, queries); snippet != "" {
				ob.WriteString(fmt.Sprintf("<match_snippet>%s</match_snippet>\n", xmlEscape(snippet)))
			}
			ob.WriteString(fmt.Sprintf("<content>%s</content>\n", result.Content))

			if result.ImageInfo != "" {
				var imageInfos []types.ImageInfo
				if err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos); err == nil && len(imageInfos) > 0 {
					for _, img := range imageInfos {
						ob.WriteString(fmt.Sprintf("<image url=\"%s\">\n", xmlEscape(img.URL)))
						if img.Caption != "" {
							ob.WriteString(fmt.Sprintf("<image_caption>%s</image_caption>\n", xmlEscape(img.Caption)))
						}
						if img.OCRText != "" {
							ob.WriteString(fmt.Sprintf("<image_ocr>%s</image_ocr>\n", xmlEscape(img.OCRText)))
						}
						ob.WriteString("</image>\n")
					}
				}
			}

			if faqMeta != nil {
				ob.WriteString("<faq>\n")
				if faqMeta.StandardQuestion != "" {
					ob.WriteString(fmt.Sprintf("<question>%s</question>\n", xmlEscape(faqMeta.StandardQuestion)))
				}
				if len(faqMeta.SimilarQuestions) > 0 {
					for _, sq := range faqMeta.SimilarQuestions {
						ob.WriteString(fmt.Sprintf("<similar_question>%s</similar_question>\n", xmlEscape(sq)))
					}
				}
				if len(faqMeta.Answers) > 0 {
					for _, ans := range faqMeta.Answers {
						ob.WriteString(fmt.Sprintf("<answer>%s</answer>\n", xmlEscape(ans)))
					}
				}
				ob.WriteString("</faq>\n")
			}

			ob.WriteString("</chunk>\n")
		}

		formattedResults = append(formattedResults, map[string]interface{}{
			"result_index":        i + 1,
			"chunk_id":            result.ID,
			"content":             result.Content,
			"knowledge_id":        result.KnowledgeID,
			"knowledge_title":     result.KnowledgeTitle,
			"match_type":          result.MatchType,
			"source_query":        result.SourceQuery,
			"query_type":          result.QueryType,
			"knowledge_base_type": result.KnowledgeBaseType,
		})

		last := formattedResults[len(formattedResults)-1]

		if result.ImageInfo != "" {
			var imageInfos []types.ImageInfo
			if err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos); err == nil && len(imageInfos) > 0 {
				imageList := make([]map[string]string, 0, len(imageInfos))
				for _, img := range imageInfos {
					imgData := make(map[string]string)
					if img.URL != "" {
						imgData["url"] = img.URL
					}
					if img.Caption != "" {
						imgData["caption"] = img.Caption
					}
					if img.OCRText != "" {
						imgData["ocr_text"] = img.OCRText
					}
					if len(imgData) > 0 {
						imageList = append(imageList, imgData)
					}
				}
				if len(imageList) > 0 {
					last["images"] = imageList
				}
			}
		}

		if faqMeta != nil {
			if faqMeta.StandardQuestion != "" {
				last["faq_standard_question"] = faqMeta.StandardQuestion
			}
			if len(faqMeta.SimilarQuestions) > 0 {
				last["faq_similar_questions"] = faqMeta.SimilarQuestions
			}
			if len(faqMeta.Answers) > 0 {
				last["faq_answers"] = faqMeta.Answers
			}
		}
	}

	// Retrieval statistics
	ob.WriteString("<retrieval_statistics>\n")
	for knowledgeID, retrievedChunks := range knowledgeChunkMap {
		totalChunks := knowledgeTotalMap[knowledgeID]
		retrievedCount := len(retrievedChunks)
		title := knowledgeTitleMap[knowledgeID]
		if totalChunks > 0 {
			remaining := totalChunks - int64(retrievedCount)
			percentage := float64(retrievedCount) / float64(totalChunks) * 100
			ob.WriteString(fmt.Sprintf("<document_stat knowledge_id=\"%s\" title=\"%s\" total_chunks=\"%d\" retrieved=\"%d\" remaining=\"%d\" coverage=\"%.1f%%\" />\n",
				xmlEscape(knowledgeID), xmlEscape(title), totalChunks, retrievedCount, remaining, percentage))
		}
	}
	ob.WriteString("</retrieval_statistics>\n")
	ob.WriteString("</search_results>")

	output := ob.String()

	data := map[string]interface{}{
		"knowledge_base_ids": kbsToSearch,
		"results":            formattedResults,
		"count":              len(formattedResults),
		"kb_counts":          kbCounts,
		"display_type":       "search_results",
	}

	if len(queries) > 0 {
		data["queries"] = queries
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data:    data,
	}, nil
}

// chunkRange represents a continuous range of chunk indices
type chunkRange struct {
	start int
	end   int
}

// getEnrichedPassage 合并Content和ImageInfo的文本内容
func (t *KnowledgeSearchTool) getEnrichedPassage(ctx context.Context, result *types.SearchResult) string {
	if result.ImageInfo == "" {
		return result.Content
	}

	// 解析ImageInfo
	var imageInfos []types.ImageInfo
	err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos)
	if err != nil {
		logger.Warnf(ctx, "[Tool][KnowledgeSearch] Failed to parse image info: %v", err)
		return result.Content
	}

	if len(imageInfos) == 0 {
		return result.Content
	}

	// 提取所有图片的描述和OCR文本
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

	// 组合内容和图片信息
	combinedText := result.Content
	if combinedText != "" {
		combinedText += "\n\n"
	}
	combinedText += strings.Join(imageTexts, "\n")

	logger.Debugf(ctx, "[Tool][KnowledgeSearch] Enriched passage: content_len=%d, image_texts=%d",
		len(result.Content), len(imageTexts))

	return combinedText
}

// compositeScore calculates a composite score considering multiple factors
func (t *KnowledgeSearchTool) compositeScore(
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
		// Calculate position ratio and apply small boost for earlier positions
		positionRatio := 1.0 - float64(result.StartAt)/float64(result.EndAt+1)
		positionPrior += t.clampFloat(positionRatio, -0.05, 0.05)
	}

	// Composite formula: weighted combination of model score, base score, and source weight
	composite := 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight
	composite *= positionPrior

	// Clamp to [0, 1]
	if composite < 0 {
		composite = 0
	}
	if composite > 1 {
		composite = 1
	}

	return composite
}

// clampFloat clamps a float value to the specified range
func (t *KnowledgeSearchTool) clampFloat(v, minV, maxV float64) float64 {
	return searchutil.ClampFloat(v, minV, maxV)
}

// applyMMR applies Maximal Marginal Relevance algorithm to reduce redundancy
func (t *KnowledgeSearchTool) applyMMR(
	ctx context.Context,
	results []*searchResultWithMeta,
	k int,
	lambda float64,
) []*searchResultWithMeta {
	if k <= 0 || len(results) == 0 {
		return nil
	}

	logger.Infof(ctx, "[Tool][KnowledgeSearch] Applying MMR: lambda=%.2f, k=%d, candidates=%d",
		lambda, k, len(results))

	selected := make([]*searchResultWithMeta, 0, k)
	candidates := make([]*searchResultWithMeta, len(results))
	copy(candidates, results)

	// Pre-compute token sets for all candidates
	tokenSets := make([]map[string]struct{}, len(candidates))
	for i, r := range candidates {
		tokenSets[i] = t.tokenizeSimple(t.getEnrichedPassage(ctx, r.SearchResult))
	}

	// MMR selection loop
	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0

		for i, r := range candidates {
			relevance := r.Score
			redundancy := 0.0

			// Calculate maximum redundancy with already selected results
			for _, s := range selected {
				selectedTokens := t.tokenizeSimple(t.getEnrichedPassage(ctx, s.SearchResult))
				redundancy = math.Max(redundancy, t.jaccard(tokenSets[i], selectedTokens))
			}

			// MMR score: balance relevance and diversity
			mmr := lambda*relevance - (1.0-lambda)*redundancy
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}

		// Add best candidate to selected and remove from candidates
		selected = append(selected, candidates[bestIdx])
		candidates = append(candidates[:bestIdx], candidates[bestIdx+1:]...)
		// Remove corresponding token set
		tokenSets = append(tokenSets[:bestIdx], tokenSets[bestIdx+1:]...)
	}

	// Compute average redundancy among selected results
	avgRed := 0.0
	if len(selected) > 1 {
		pairs := 0
		for i := 0; i < len(selected); i++ {
			for j := i + 1; j < len(selected); j++ {
				si := t.tokenizeSimple(t.getEnrichedPassage(ctx, selected[i].SearchResult))
				sj := t.tokenizeSimple(t.getEnrichedPassage(ctx, selected[j].SearchResult))
				avgRed += t.jaccard(si, sj)
				pairs++
			}
		}
		if pairs > 0 {
			avgRed /= float64(pairs)
		}
	}

	logger.Infof(ctx, "[Tool][KnowledgeSearch] MMR completed: selected=%d, avg_redundancy=%.4f",
		len(selected), avgRed)

	return selected
}

// tokenizeSimple tokenizes text into a set of words (simple whitespace-based)
func (t *KnowledgeSearchTool) tokenizeSimple(text string) map[string]struct{} {
	return searchutil.TokenizeSimple(text)
}

// extractSnippetForQueries tries to produce a short contextual snippet around
// the first occurrence of any token extracted from the provided queries.
// When no token matches (common for fully paraphrased semantic queries) it
// falls back to the leading 160 runes of content so callers always get
// something to scan. The snippet is single-lined and bounded in length to
// keep the rendered XML compact.
func extractSnippetForQueries(content string, queries []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	tokens := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, q := range queries {
		for _, tok := range strings.FieldsFunc(q, func(r rune) bool {
			// Split on whitespace and common punctuation; keep CJK as whole tokens.
			switch r {
			case ' ', '\t', '\n', '\r', ',', '.', ';', ':', '?', '!',
				'(', ')', '[', ']', '{', '}', '"', '\'':
				return true
			}
			return false
		}) {
			tok = strings.ToLower(strings.TrimSpace(tok))
			// Skip trivially-short stopwords. Three rune floor covers most
			// English function words without chopping CJK bigrams.
			if len([]rune(tok)) < 2 {
				continue
			}
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			tokens = append(tokens, tok)
		}
	}

	lowered := strings.ToLower(content)
	earliest := -1
	earliestEnd := -1
	for _, tok := range tokens {
		idx := strings.Index(lowered, tok)
		if idx < 0 {
			continue
		}
		end := idx + len(tok)
		if earliest < 0 || idx < earliest {
			earliest = idx
			earliestEnd = end
		}
	}

	const contextRunes = 60
	if earliest < 0 {
		runes := []rune(content)
		if len(runes) > contextRunes*2 {
			return strings.TrimSpace(string(runes[:contextRunes*2])) + " ..."
		}
		return content
	}

	matchStr := content[earliest:earliestEnd]
	before := content[:earliest]
	after := content[earliestEnd:]

	beforeRunes := []rune(before)
	if len(beforeRunes) > contextRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-contextRunes:]
	}
	afterRunes := []rune(after)
	if len(afterRunes) > contextRunes {
		afterRunes = afterRunes[:contextRunes]
	}

	snippet := string(beforeRunes) + matchStr + string(afterRunes)
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}
	return "... " + strings.TrimSpace(snippet) + " ..."
}

// jaccard calculates Jaccard similarity between two token sets
func (t *KnowledgeSearchTool) jaccard(a, b map[string]struct{}) float64 {
	return searchutil.Jaccard(a, b)
}
