package service

import (
	"context"
	"fmt"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/reranking"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// resolveRerankModelID picks the rerank model for a retrieval API request:
// the requested ID, else the tenant RetrievalConfig, else the tenant's first
// rerank model. It returns the ID and its source (types.RerankModelSource*),
// or an empty ID when the tenant has no rerank model.
//
// A requested ID must name an active rerank model of the tenant; anything
// else is the caller's mistake and fails with a bad request instead of
// silently falling back to another model.
func resolveRerankModelID(
	ctx context.Context,
	modelService interfaces.ModelService,
	requested string,
	rc *types.RetrievalConfig,
) (string, string, error) {
	if requested != "" {
		model, err := modelService.GetModelByID(ctx, requested)
		if err != nil || model == nil {
			return "", "", apperrors.NewBadRequestError(
				fmt.Sprintf("rerank model %q not found or not active", requested))
		}
		if model.Type != types.ModelTypeRerank {
			return "", "", apperrors.NewBadRequestError(
				fmt.Sprintf("model %q is a %s model, not a rerank model", requested, model.Type))
		}
		return model.ID, types.RerankModelSourceRequest, nil
	}
	if rc != nil && rc.RerankModelID != "" {
		return rc.RerankModelID, types.RerankModelSourceTenant, nil
	}
	models, err := modelService.ListModels(ctx)
	if err != nil {
		// Auto-detection is best effort: without a model the search still
		// answers, in retrieval order, and the diagnostics say why.
		logger.Warnf(ctx, "Rerank model auto-detection failed: %v", err)
		return "", "", nil
	}
	for _, model := range models {
		if model != nil && model.Type == types.ModelTypeRerank {
			return model.ID, types.RerankModelSourceAuto, nil
		}
	}
	return "", "", nil
}

// HybridSearchWithRerank runs HybridSearch and, when params.Rerank asks for
// it, reranks the fused candidates before cutting to the requested count.
//
// The rerank candidate pool is the top max(top_k, DefaultRetrievalTopK)
// fused chunks, so a small match_count still gives the model a meaningful
// pool to choose from. Context enrichment (parent / nearby / relation
// chunks) runs on the reranked rows only.
func (s *knowledgeBaseService) HybridSearchWithRerank(ctx context.Context,
	id string,
	params types.SearchParams,
) (*types.RetrievalResult, error) {
	if !params.Rerank.IsEnabled() {
		results, err := s.HybridSearch(ctx, id, params)
		if err != nil {
			return nil, err
		}
		out := &types.RetrievalResult{Results: results}
		if params.Rerank != nil {
			out.Meta.Rerank = &types.RerankDiagnostics{
				Outcome:        types.RerankOutcomeDisabled,
				CandidateCount: len(results),
				ResultCount:    len(results),
			}
		}
		return out, nil
	}

	opts := params.Rerank
	params.MatchCount = normalizedMatchCount(params.MatchCount)
	topK := opts.TopK
	if topK <= 0 {
		topK = params.MatchCount
	}
	topK = min(topK, maxRetrievalPoolSize)

	var rc *types.RetrievalConfig
	if tenantInfo, ok := types.TenantInfoFromContext(ctx); ok && tenantInfo != nil {
		rc = tenantInfo.RetrievalConfig
	}
	// Rerank models belong to the caller. A shared KB request executes in
	// the owner's tenant, so resolve models back in the caller's.
	modelCtx := ctx
	if caller := types.CallerFromContext(ctx); caller.TenantID != 0 {
		modelCtx = types.WithExecutionTenant(ctx, caller.TenantID)
	}
	// Resolve before retrieving so a bad model_id costs nothing.
	modelID, modelSource, err := resolveRerankModelID(modelCtx, s.modelService, opts.ModelID, rc)
	if err != nil {
		return nil, err
	}
	threshold := rc.GetEffectiveRerankThreshold()
	if opts.Threshold != nil {
		threshold = *opts.Threshold
	}
	diag := &types.RerankDiagnostics{
		ModelID:            modelID,
		ModelSource:        modelSource,
		Threshold:          threshold,
		EffectiveThreshold: threshold,
	}
	out := &types.RetrievalResult{Meta: types.RetrievalMeta{Rerank: diag}}

	chunks, err := s.hybridSearchCandidates(ctx, id, params)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		diag.Outcome = types.RerankOutcomeNoCandidates
		return out, nil
	}
	pool := chunks[:min(len(chunks), max(topK, types.DefaultRetrievalTopK))]
	candidates, err := s.processSearchResults(ctx, pool, true)
	if err != nil {
		return nil, err
	}
	diag.CandidateCount = len(candidates)

	final := s.rerankCandidates(modelCtx, modelID, params.QueryText, candidates, threshold, topK, diag)
	diag.ResultCount = len(final)
	logger.Infof(ctx, "Hybrid search rerank: kb=%s model=%s(%s) outcome=%s candidates=%d results=%d",
		secutils.SanitizeForLog(id), modelID, modelSource, diag.Outcome, len(candidates), len(final))

	if params.SkipContextEnrichment || len(final) == 0 {
		out.Results = final
		return out, nil
	}
	out.Results, err = s.enrichRerankedResults(ctx, pool, final)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// rerankCandidates reranks candidates with modelID and returns at most topK
// rows. When the model is missing or fails, it records why in diag and
// returns the retrieval order instead.
func (s *knowledgeBaseService) rerankCandidates(
	ctx context.Context,
	modelID, query string,
	candidates []*types.SearchResult,
	threshold float64,
	topK int,
	diag *types.RerankDiagnostics,
) []*types.SearchResult {
	retrievalOrder := candidates[:min(len(candidates), topK)]
	if len(candidates) == 0 {
		diag.Outcome = types.RerankOutcomeNoCandidates
		return nil
	}
	if modelID == "" {
		diag.Outcome = types.RerankOutcomeNoModel
		return retrievalOrder
	}
	model, err := s.modelService.GetRerankModel(ctx, modelID)
	if err != nil {
		logger.Warnf(ctx, "Rerank model %s unavailable, keeping retrieval order: %v", modelID, err)
		diag.Outcome = types.RerankOutcomeModelUnavailable
		diag.Error = err.Error()
		return retrievalOrder
	}

	res := reranking.Rerank(ctx, model, query, candidates, reranking.Options{
		Threshold:        threshold,
		TopK:             topK,
		FallbackMinScore: reranking.DefaultFallbackMinScore,
	})
	modelSource := diag.ModelSource
	*diag = res.Diagnostics
	diag.ModelID, diag.ModelSource = modelID, modelSource
	if diag.Outcome == types.RerankOutcomeModelError {
		return retrievalOrder
	}
	return res.Results
}

// enrichRerankedResults adds the parent / nearby / relation context chunks
// of the reranked rows. processSearchResults rebuilds the primary rows from
// storage, so they are swapped back for the reranked rows, which carry the
// rerank scores and metadata.
func (s *knowledgeBaseService) enrichRerankedResults(
	ctx context.Context,
	pool []*types.IndexWithScore,
	reranked []*types.SearchResult,
) ([]*types.SearchResult, error) {
	byChunkID := make(map[string]*types.IndexWithScore, len(pool))
	for _, c := range pool {
		byChunkID[c.ChunkID] = c
	}
	rerankedByID := make(map[string]*types.SearchResult, len(reranked))
	primary := make([]*types.IndexWithScore, 0, len(reranked))
	for _, r := range reranked {
		c, ok := byChunkID[r.ID]
		if !ok {
			continue
		}
		scored := *c
		scored.Score = r.Score
		primary = append(primary, &scored)
		rerankedByID[r.ID] = r
	}

	enriched, err := s.processSearchResults(ctx, primary, false)
	if err != nil {
		return nil, err
	}
	for i, r := range enriched {
		if rr, ok := rerankedByID[r.ID]; ok {
			enriched[i] = rr
		}
	}
	return enriched, nil
}
