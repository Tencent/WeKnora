package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
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
	// Determine the set of KB IDs to search.
	searchKBIDs := params.KnowledgeBaseIDs
	if len(searchKBIDs) == 0 {
		searchKBIDs = []string{id}
	}

	// QueryText is user-controlled; sanitize before logging to prevent
	// CR/LF/tab log injection. Matches the handler-layer sanitization at
	// handler/knowledgebase.go.
	logger.Infof(ctx, "Hybrid search parameters, knowledge base IDs: %v, query text: %s",
		searchKBIDs, secutils.SanitizeForLog(params.QueryText))

	tenantInfo, _ := types.TenantInfoFromContext(ctx)
	requestTenantID := types.MustTenantIDFromContext(ctx)

	// Batch-load every KB in scope. Required for store grouping,
	// embedding-model consistency validation, and FAQ type detection.
	// GetKnowledgeBaseByIDs is intentionally tenant-agnostic at the
	// repository layer so that Organization-shared KBs (owned by a
	// different tenant) can be loaded here; authorization for each
	// returned row is enforced explicitly below.
	kbs, err := s.repo.GetKnowledgeBaseByIDs(ctx, searchKBIDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_ids": searchKBIDs,
		})
		return nil, err
	}
	if len(kbs) == 0 {
		return nil, apperrors.NewNotFoundError("knowledge base not found")
	}

	// Authorize every KB the caller asked for. Same-tenant KBs are
	// always accessible; foreign-tenant KBs (Organization-shared) must
	// pass an explicit per-KB permission check. Without this guard, a
	// caller could pass arbitrary KB UUIDs in params.KnowledgeBaseIDs
	// and reach foreign tenants' bound vector stores via the per-group
	// engine resolution downstream.
	if err := s.authorizeKBAccess(ctx, kbs, requestTenantID); err != nil {
		return nil, err
	}

	// Explicit embedding-model consistency check. Multi-KB searches that
	// span different embedding spaces would otherwise silently produce
	// meaningless cross-model scores. Same-model wiki/graph KBs are
	// tolerated — see validateSameEmbeddingModel for the carve-out.
	if err := s.validateSameEmbeddingModel(ctx, kbs); err != nil {
		return nil, err
	}

	// Resolve the primary KB — embedding model + FAQ type come from this
	// one. Miss → 404 (no kbs[0] fallback; a silent pivot to an arbitrary
	// KB would hide caller bugs and reveal foreign KB metadata).
	kb := pickPrimary(kbs, id)
	if kb == nil {
		return nil, apperrors.NewNotFoundError("knowledge base not found")
	}

	// Over-retrieval (existing rule, preserved): 5x per-KB matchCount,
	// floor of 50, capped at 500 across the whole search.
	matchCount := max(params.MatchCount*5, 50) * len(searchKBIDs)
	if matchCount > 500 {
		matchCount = 500
	}

	// Compute the query embedding once before fan-out and propagate via
	// params.QueryEmbedding. Without this, each storeGroup's
	// buildRetrievalParams would re-embed the same query text — for N
	// stores that means N API calls of identical input.
	//
	// Skip when params already carries an embedding (e.g. the agent
	// pre-computed it) or when the primary KB has no vector indexing
	// configured.
	if len(params.QueryEmbedding) == 0 &&
		kb.IsVectorEnabled() && kb.EmbeddingModelID != "" &&
		!params.DisableVectorMatch {
		emb, embErr := s.GetQueryEmbedding(ctx, kb.ID, params.QueryText)
		if embErr != nil {
			return nil, embErr
		}
		params.QueryEmbedding = emb
	}

	// Group KBs by (storeID, owner tenant), resolve the bound engine for
	// each group, and build the per-group base RetrieveParams once.
	groups, err := s.resolveStoreGroups(ctx, kb, kbs, params, matchCount)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 || allBaseParamsEmpty(groups) {
		// Wiki-only / graph-only fan-out: every KB is non-retrievable.
		// Preserve the existing "return empty rather than error" contract
		// so agent tools that combine multiple KB scopes degrade gracefully.
		logger.Infof(ctx, "No retrievable indexing pipelines across %d KBs", len(kbs))
		return nil, nil
	}

	// Execute retrieval with fan-out + score normalization (multi-store
	// only) and a langfuse span around the entire retrieve step.
	logger.Infof(ctx, "Starting multi-store retrieval, group count: %d", len(groups))
	retrieveCtx, retrieveSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "retrieve",
		Input: map[string]interface{}{
			"kb_ids":      searchKBIDs,
			"group_count": len(groups),
			"match_count": matchCount,
		},
	})
	retrieveResults, err := s.retrieveFromStores(retrieveCtx, groups, retriever.EngineAwareNormalizer{})
	retrieveSpan.Finish(map[string]interface{}{
		"result_count": totalHits(retrieveResults),
	}, nil, err)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_ids": searchKBIDs,
			"query_text":         params.QueryText,
		})
		return nil, err
	}

	// Separate and fuse retrieval results.
	vectorResults, keywordResults := classifyRetrievalResults(ctx, retrieveResults)
	if len(vectorResults) == 0 && len(keywordResults) == 0 {
		logger.Info(ctx, "No search results found")
		return nil, nil
	}
	logger.Infof(ctx, "Result count before fusion: vector=%d, keyword=%d",
		len(vectorResults), len(keywordResults))

	var retrievalCfg *types.RetrievalConfig
	if tenantInfo != nil {
		retrievalCfg = tenantInfo.RetrievalConfig
	}
	deduplicatedChunks := fuseOrDeduplicate(ctx, vectorResults, keywordResults, retrievalCfg)

	kb.EnsureDefaults()

	// FAQ-specific post-processing now operates on storeGroups so the
	// iterative TopK growth applies uniformly across the fan-out. An
	// AppError from inside the iterative fan-out path (e.g. a per-group
	// timeout surfaced as ErrVectorStoreUnavailable) must surface to the
	// caller rather than be silently converted to a truncated chunk list.
	deduplicatedChunks, err = s.applyFAQPostProcessing(
		ctx, kb, deduplicatedChunks, vectorResults, groups, params, matchCount)
	if err != nil {
		return nil, err
	}

	if len(deduplicatedChunks) > params.MatchCount {
		deduplicatedChunks = deduplicatedChunks[:params.MatchCount]
	}

	return s.processSearchResults(ctx, deduplicatedChunks, params.SkipContextEnrichment)
}

// pickPrimary returns the KB whose ID matches id, or nil if id is not in
// scope. Callers map a nil result to NotFound; there is intentionally no
// kbs[0] fallback because it would mask caller bugs and could leak an
// unintended KB's embedding-model identity into the search path.
//
// The primary KB drives the embedding model and FAQ-type decisions for
// buildRetrievalParams. If the caller selects a wiki-only / graph-only
// KB as primary, the multi-KB search is implicitly demoted to
// keyword-only retrieval — vector retrieval is skipped because
// primary.IsVectorEnabled() is false. Callers that mix vector-enabled
// and non-vector KBs should pass a vector-enabled KB as id.
func pickPrimary(kbs []*types.KnowledgeBase, id string) *types.KnowledgeBase {
	for _, kb := range kbs {
		if kb.ID == id {
			return kb
		}
	}
	return nil
}

// allBaseParamsEmpty reports whether every store group has an empty
// BaseParams slice. True only when every KB in scope is wiki-only or
// graph-only with neither vector nor keyword indexing — HybridSearch then
// returns nil so callers that combine searchable + non-searchable KBs
// (agent tools, chat pipeline) degrade gracefully.
func allBaseParamsEmpty(groups []*storeGroup) bool {
	for _, g := range groups {
		if len(g.BaseParams) > 0 {
			return false
		}
	}
	return true
}

// totalHits counts the IndexWithScore entries across a slice of retrieve
// results. Used only for langfuse span metadata.
func totalHits(rrs []*types.RetrieveResult) int {
	n := 0
	for _, r := range rrs {
		n += len(r.Results)
	}
	return n
}

// buildRetrievalParams constructs the vector and keyword retrieval parameters
// for one store group, based on each member KB's type, the engine's
// capabilities, and the search params.
//
// The FAQ-vs-document index decision is a PER-KB property (kb.Type), not a
// property of the multi-KB search's primary KB. groupKBs therefore drives the
// routing: KBs in the group are split into a FAQ subset (retrieved from the
// FAQ vector index) and a document subset (retrieved from the default vector
// index plus the keyword index). Deciding this from the primary KB alone would
// route every KB in scope into the primary's index — e.g. a FAQ primary would
// drag a document-type KB into the FAQ index and skip its keyword recall.
//
// primary is used only to resolve the shared query embedding model; every KB
// in a multi-KB search is guaranteed to share a single embedding model by
// validateSameEmbeddingModel upstream.
func (s *knowledgeBaseService) buildRetrievalParams(
	ctx context.Context,
	retrieveEngine *retriever.CompositeRetrieveEngine,
	primary *types.KnowledgeBase,
	groupKBs []*types.KnowledgeBase,
	params types.SearchParams,
	matchCount int,
) ([]types.RetrieveParams, error) {
	currentTenantID := types.MustTenantIDFromContext(ctx)
	var retrieveParams []types.RetrieveParams

	// Partition the group's KBs by index routing. A KB that does not have
	// vector indexing enabled (e.g. wiki-only or graph-only KBs) has no
	// embeddings to retrieve from, and typically has no EmbeddingModelID
	// configured either; such KBs are skipped for vector retrieval to avoid
	// spurious "model ID cannot be empty" errors when an agent's retrieval
	// scope happens to include them (e.g. KBSelectionMode=all picking up a
	// wiki-only KB).
	var faqVectorKBIDs, docVectorKBIDs, docKeywordKBIDs []string
	for _, kb := range groupKBs {
		if kb.IsVectorEnabled() && kb.EmbeddingModelID != "" {
			if kb.Type == types.KnowledgeBaseTypeFAQ {
				faqVectorKBIDs = append(faqVectorKBIDs, kb.ID)
			} else {
				docVectorKBIDs = append(docVectorKBIDs, kb.ID)
			}
		}
		// FAQ KBs are retrieved exclusively via the FAQ vector index and
		// have no keyword index; only document-type KBs participate in
		// keyword retrieval.
		if kb.IsKeywordEnabled() && kb.Type != types.KnowledgeBaseTypeFAQ {
			docKeywordKBIDs = append(docKeywordKBIDs, kb.ID)
		}
	}

	// Add vector retrieval params if supported
	if retrieveEngine.SupportRetriever(types.VectorRetrieverType) && !params.DisableVectorMatch &&
		(len(faqVectorKBIDs) > 0 || len(docVectorKBIDs) > 0) {
		logger.Info(ctx, "Vector retrieval supported, preparing vector retrieval parameters")

		queryEmbedding, err := s.resolveQueryEmbedding(ctx, primary, params, currentTenantID)
		if err != nil {
			return nil, err
		}

		appendVectorParams := func(kbIDs []string, knowledgeType string) {
			retrieveParams = append(retrieveParams, types.RetrieveParams{
				Query:            params.QueryText,
				Embedding:        queryEmbedding,
				KnowledgeBaseIDs: kbIDs,
				TopK:             matchCount,
				Threshold:        params.VectorThreshold,
				RetrieverType:    types.VectorRetrieverType,
				KnowledgeIDs:     params.KnowledgeIDs,
				TagIDs:           params.TagIDs,
				KnowledgeType:    knowledgeType,
			})
		}

		// Document KBs use the default vector index; FAQ KBs use the FAQ
		// index. A group containing both types yields one retrieval param
		// per index so each KB is queried against the index it was written to.
		if len(docVectorKBIDs) > 0 {
			appendVectorParams(docVectorKBIDs, "")
		}
		if len(faqVectorKBIDs) > 0 {
			appendVectorParams(faqVectorKBIDs, types.KnowledgeTypeFAQ)
		}
		logger.Info(ctx, "Vector retrieval parameters setup completed")
	}

	// Add keyword retrieval params if supported and any document KB has
	// keyword indexing enabled.
	if retrieveEngine.SupportRetriever(types.KeywordsRetrieverType) && !params.DisableKeywordsMatch &&
		len(docKeywordKBIDs) > 0 {
		logger.Info(ctx, "Keyword retrieval supported, preparing keyword retrieval parameters")
		retrieveParams = append(retrieveParams, types.RetrieveParams{
			Query:            params.QueryText,
			KnowledgeBaseIDs: docKeywordKBIDs,
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

// resolveQueryEmbedding returns the query embedding for a store group. It
// reuses params.QueryEmbedding when the caller pre-computed it (the common
// path — HybridSearch embeds once before fan-out), otherwise it embeds the
// query text using the embedding model of the supplied KB. For cross-tenant
// shared KBs the source tenant's embedding model is used so the produced
// vector is compatible with the index it will be searched against.
func (s *knowledgeBaseService) resolveQueryEmbedding(
	ctx context.Context,
	kb *types.KnowledgeBase,
	params types.SearchParams,
	currentTenantID uint64,
) ([]float32, error) {
	if len(params.QueryEmbedding) > 0 {
		logger.Infof(ctx, "Using pre-computed query embedding, vector length: %d", len(params.QueryEmbedding))
		return params.QueryEmbedding, nil
	}

	logger.Infof(ctx, "Getting embedding model, model ID: %s", kb.EmbeddingModelID)

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
	queryEmbedding, err := embeddingModel.Embed(ctx, params.QueryText)
	if err != nil {
		logger.Errorf(ctx, "Failed to embed query text, query text: %s, error: %v", params.QueryText, err)
		return nil, err
	}
	logger.Infof(ctx, "Query embedding generated successfully, embedding vector length: %d", len(queryEmbedding))
	return queryEmbedding, nil
}
