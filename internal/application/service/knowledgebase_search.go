package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

// GetQueryEmbedding computes the query embedding using the embedding model
// associated with the given knowledge base. Callers can pre-compute and reuse
// the result across multiple KBs that share the same embedding model to avoid
// redundant embedding API calls.
func (s *knowledgeBaseService) GetQueryEmbedding(ctx context.Context, kbID string, queryText string) ([]float32, error) {
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}

	currentTenantID := types.MustTenantIDFromContext(ctx)
	var embeddingModel embedding.Embedder

	if kb.TenantID != currentTenantID {
		embeddingModel, err = s.modelService.GetEmbeddingModelForTenant(ctx, kb.EmbeddingModelID, kb.TenantID)
	} else {
		embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	}
	if err != nil {
		logger.Errorf(ctx, "GetQueryEmbedding: failed to get embedding model %s: %v", kb.EmbeddingModelID, err)
		return nil, err
	}

	return embeddingModel.Embed(ctx, queryText)
}

// ResolveEmbeddingModelKeys resolves embedding model IDs to their actual model
// identity key (name + endpoint). KBs using the same underlying model across
// different tenants will share the same key, enabling optimal grouping.
func (s *knowledgeBaseService) ResolveEmbeddingModelKeys(ctx context.Context, kbs []*types.KnowledgeBase) map[string]string {
	type modelRef struct {
		ModelID  string
		TenantID uint64
	}

	// Deduplicate model references
	uniqueRefs := make(map[modelRef]struct{})
	kbRefs := make(map[string]modelRef, len(kbs))
	for _, kb := range kbs {
		ref := modelRef{ModelID: kb.EmbeddingModelID, TenantID: kb.TenantID}
		uniqueRefs[ref] = struct{}{}
		kbRefs[kb.ID] = ref
	}

	// Resolve each unique (modelID, tenantID) to a model identity key
	resolvedKeys := make(map[modelRef]string, len(uniqueRefs))
	for ref := range uniqueRefs {
		tenantCtx := context.WithValue(ctx, types.TenantIDContextKey, ref.TenantID)
		model, err := s.modelService.GetModelByID(tenantCtx, ref.ModelID)
		if err != nil || model == nil {
			logger.Warnf(ctx, "ResolveEmbeddingModelKeys: cannot resolve model %s for tenant %d: %v", ref.ModelID, ref.TenantID, err)
			resolvedKeys[ref] = ref.ModelID
			continue
		}
		resolvedKeys[ref] = model.Name + "|" + model.Parameters.BaseURL
	}

	result := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		result[kb.ID] = resolvedKeys[kbRefs[kb.ID]]
	}
	return result
}

// HybridSearch performs hybrid search, including vector retrieval and keyword retrieval.
//
// id is the "primary" knowledge base ID used to resolve the embedding model and
// determine the KB type (e.g. FAQ). When params.KnowledgeBaseIDs is set, those
// IDs are used for the actual retrieval scope instead of id alone, allowing a
// single call to span multiple KBs that share the same embedding model. In that
// case id should be any one of those KBs (typically the first) so that its
// embedding model and type configuration are used for the search.
func (s *knowledgeBaseService) HybridSearch(ctx context.Context,
	id string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	// Determine the set of KB IDs to search
	searchKBIDs := params.KnowledgeBaseIDs
	if len(searchKBIDs) == 0 {
		searchKBIDs = []string{id}
	}

	logger.Infof(ctx, "Hybrid search parameters, knowledge base IDs: %v, query text: %s", searchKBIDs, params.QueryText)

	tenantInfo, _ := types.TenantInfoFromContext(ctx)

	// Create a composite retrieval engine with tenant's configured retrievers
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Errorf(ctx, "Failed to create retrieval engine: %v", err)
		return nil, err
	}

	kb, err := s.repo.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": id,
		})
		return nil, err
	}

	// Use 5x over-retrieval to ensure sufficient candidates for RRF fusion and reranking.
	// Scale proportionally when searching multiple KBs to maintain per-KB recall quality.
	matchCount := max(params.MatchCount*5, 50) * len(searchKBIDs)
	if matchCount > 500 {
		matchCount = 500
	}

	// Build retrieval parameters for vector and keyword engines
	retrieveParams, err := s.buildRetrievalParams(ctx, retrieveEngine, kb, params, searchKBIDs, matchCount)
	if err != nil {
		return nil, err
	}
	if len(retrieveParams) == 0 {
		// No retrievable pipelines for this KB (e.g. a wiki-only or graph-only
		// KB that has neither vector nor keyword indexing). Return empty
		// results rather than erroring so callers that combine multiple KB
		// scopes (agent knowledge_search tool, chat pipeline, etc.) degrade
		// gracefully when one of the scopes is non-searchable.
		logger.Infof(ctx, "No retrievable indexing pipelines for KB %s (vector=%v, keyword=%v), returning empty results",
			kb.ID, kb.IsVectorEnabled(), kb.IsKeywordEnabled())
		return nil, nil
	}

	// Execute retrieval using the configured engines.
	// A dedicated span captures the actual vector/keyword DB round-trip
	// — this is the time previously visible in Langfuse only as the gap
	// between embedding generations and the rerank call.
	logger.Infof(ctx, "Starting retrieval, parameter count: %d", len(retrieveParams))
	retrieverTypes := make([]string, 0, len(retrieveParams))
	for _, rp := range retrieveParams {
		retrieverTypes = append(retrieverTypes, string(rp.RetrieverType))
	}
	retrieveCtx, retrieveSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "retrieve",
		Input: map[string]interface{}{
			"kb_ids":      searchKBIDs,
			"match_count": matchCount,
			"retrievers":  retrieverTypes,
		},
		Metadata: map[string]interface{}{
			"param_count": len(retrieveParams),
		},
	})
	retrieveResults, err := retrieveEngine.Retrieve(retrieveCtx, retrieveParams)
	retrieveSpan.Finish(map[string]interface{}{
		"result_count": len(retrieveResults),
	}, nil, err)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_ids": searchKBIDs,
			"query_text":         params.QueryText,
		})
		return nil, err
	}

	// Separate and fuse retrieval results
	vectorResults, keywordResults := classifyRetrievalResults(ctx, retrieveResults)
	if len(vectorResults) == 0 && len(keywordResults) == 0 {
		logger.Info(ctx, "No search results found")
		return nil, nil
	}
	logger.Infof(ctx, "Result count before fusion: vector=%d, keyword=%d", len(vectorResults), len(keywordResults))

	var retrievalCfg *types.RetrievalConfig
	if tenantInfo != nil {
		retrievalCfg = tenantInfo.RetrievalConfig
	}
	deduplicatedChunks := fuseOrDeduplicate(ctx, vectorResults, keywordResults, retrievalCfg)

	kb.EnsureDefaults()

	// FAQ-specific post-processing: iterative retrieval or negative question filtering
	deduplicatedChunks = s.applyFAQPostProcessing(ctx, kb, deduplicatedChunks, vectorResults, retrieveEngine, retrieveParams, params, matchCount)

	// Limit to MatchCount
	if len(deduplicatedChunks) > params.MatchCount {
		deduplicatedChunks = deduplicatedChunks[:params.MatchCount]
	}

	return s.processSearchResults(ctx, deduplicatedChunks, params.SkipContextEnrichment)
}

// buildRetrievalParams constructs the vector and keyword retrieval parameters
// based on the knowledge base type, engine capabilities, and search params.
func (s *knowledgeBaseService) buildRetrievalParams(
	ctx context.Context,
	retrieveEngine *retriever.CompositeRetrieveEngine,
	kb *types.KnowledgeBase,
	params types.SearchParams,
	searchKBIDs []string,
	matchCount int,
) ([]types.RetrieveParams, error) {
	currentTenantID := types.MustTenantIDFromContext(ctx)
	var retrieveParams []types.RetrieveParams

	// Respect the KB's IndexingStrategy: a KB that does not have vector
	// indexing enabled (e.g. wiki-only or graph-only KBs) has no embeddings
	// to retrieve from, and typically has no EmbeddingModelID configured
	// either. Skipping vector retrieval for such KBs avoids spurious
	// "model ID cannot be empty" errors when an agent's retrieval scope
	// happens to include them (e.g. KBSelectionMode=all picking up a
	// wiki-only KB).
	vectorIndexed := kb.IsVectorEnabled() && kb.EmbeddingModelID != ""

	// Add vector retrieval params if supported
	if retrieveEngine.SupportRetriever(types.VectorRetrieverType) && !params.DisableVectorMatch && vectorIndexed {
		logger.Info(ctx, "Vector retrieval supported, preparing vector retrieval parameters")

		var queryEmbedding []float32

		if len(params.QueryEmbedding) > 0 {
			queryEmbedding = params.QueryEmbedding
			logger.Infof(ctx, "Using pre-computed query embedding, vector length: %d", len(queryEmbedding))
		} else {
			logger.Infof(ctx, "Getting embedding model, model ID: %s", kb.EmbeddingModelID)

			// Check if this is a cross-tenant shared knowledge base
			// For shared KB, we must use the source tenant's embedding model to ensure vector compatibility
			var embeddingModel embedding.Embedder
			var err error
			if kb.TenantID != currentTenantID {
				logger.Infof(ctx, "Cross-tenant knowledge base detected, using source tenant's embedding model. KB tenant: %d, current tenant: %d", kb.TenantID, currentTenantID)
				embeddingModel, err = s.modelService.GetEmbeddingModelForTenant(ctx, kb.EmbeddingModelID, kb.TenantID)
			} else {
				embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
			}

			if err != nil {
				logger.Errorf(ctx, "Failed to get embedding model, model ID: %s, error: %v", kb.EmbeddingModelID, err)
				return nil, err
			}
			logger.Infof(ctx, "Embedding model retrieved: %v", embeddingModel)

			logger.Info(ctx, "Starting to generate query embedding")
			queryEmbedding, err = embeddingModel.Embed(ctx, params.QueryText)
			if err != nil {
				logger.Errorf(ctx, "Failed to embed query text, query text: %s, error: %v", params.QueryText, err)
				return nil, err
			}
			logger.Infof(ctx, "Query embedding generated successfully, embedding vector length: %d", len(queryEmbedding))
		}

		vectorParams := types.RetrieveParams{
			Query:            params.QueryText,
			Embedding:        queryEmbedding,
			KnowledgeBaseIDs: searchKBIDs,
			TopK:             matchCount,
			Threshold:        params.VectorThreshold,
			RetrieverType:    types.VectorRetrieverType,
			KnowledgeIDs:     params.KnowledgeIDs,
			TagIDs:           params.TagIDs,
		}

		// For FAQ knowledge base, use FAQ index
		if kb.Type == types.KnowledgeBaseTypeFAQ {
			vectorParams.KnowledgeType = types.KnowledgeTypeFAQ
		}

		retrieveParams = append(retrieveParams, vectorParams)
		logger.Info(ctx, "Vector retrieval parameters setup completed")
	}

	// Add keyword retrieval params if supported, KB has keyword indexing, and not FAQ
	if retrieveEngine.SupportRetriever(types.KeywordsRetrieverType) && !params.DisableKeywordsMatch &&
		kb.IsKeywordEnabled() && kb.Type != types.KnowledgeBaseTypeFAQ {
		logger.Info(ctx, "Keyword retrieval supported, preparing keyword retrieval parameters")
		retrieveParams = append(retrieveParams, types.RetrieveParams{
			Query:            params.QueryText,
			KnowledgeBaseIDs: searchKBIDs,
			TopK:             matchCount,
			Threshold:        params.KeywordThreshold,
			RetrieverType:    types.KeywordsRetrieverType,
			KnowledgeIDs:     params.KnowledgeIDs,
			TagIDs:           params.TagIDs,
		})
		logger.Info(ctx, "Keyword retrieval parameters setup completed")
	}

	return retrieveParams, nil
}
