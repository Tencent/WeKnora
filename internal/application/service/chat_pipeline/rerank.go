package chatpipeline

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/reranking"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginRerank implements reranking functionality for chat pipeline
type PluginRerank struct {
	modelService interfaces.ModelService // Service to access rerank models
}

// NewPluginRerank creates a new rerank plugin instance
func NewPluginRerank(eventManager *EventManager, modelService interfaces.ModelService) *PluginRerank {
	res := &PluginRerank{
		modelService: modelService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginRerank) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent handles reranking events in the chat pipeline
func (p *PluginRerank) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if !chatManage.NeedsRetrieval() {
		return next()
	}
	pipelineInfo(ctx, "Rerank", "input", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"candidate_cnt": len(chatManage.SearchResult),
		"rerank_model":  chatManage.RerankModelID,
		"rerank_thresh": chatManage.RerankThreshold,
		"rewrite_query": chatManage.RewriteQuery,
	})
	diag := &types.RerankDiagnostics{
		ModelID:            chatManage.RerankModelID,
		Threshold:          chatManage.RerankThreshold,
		EffectiveThreshold: chatManage.RerankThreshold,
		CandidateCount:     len(chatManage.SearchResult),
		ResultCount:        len(chatManage.SearchResult),
	}
	chatManage.RerankDiagnostics = diag
	if len(chatManage.SearchResult) == 0 {
		diag.Outcome = types.RerankOutcomeNoCandidates
		pipelineInfo(ctx, "Rerank", "skip", map[string]interface{}{
			"reason": "empty_search_result",
		})
		return next()
	}
	if chatManage.RerankModelID == "" {
		diag.Outcome = types.RerankOutcomeNoModel
		pipelineWarn(ctx, "Rerank", "skip", map[string]interface{}{
			"reason": "empty_model_id",
		})
		return next()
	}

	// Get rerank model from service
	rerankModel, err := p.modelService.GetRerankModel(ctx, chatManage.RerankModelID)
	if err != nil {
		diag.Outcome = types.RerankOutcomeModelUnavailable
		diag.Error = err.Error()
		pipelineError(ctx, "Rerank", "get_model", map[string]interface{}{
			"model_id": chatManage.RerankModelID,
			"error":    err.Error(),
		})
		return ErrGetRerankModel.WithError(err)
	}

	rerankCtx, rerankSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "rerank",
		Input: map[string]interface{}{
			"query":           chatManage.RewriteQuery,
			"candidate_count": len(chatManage.SearchResult),
			"rerank_model_id": chatManage.RerankModelID,
			"threshold":       chatManage.RerankThreshold,
			"rerank_top_k":    chatManage.RerankTopK,
			"faq_priority":    chatManage.FAQPriorityEnabled,
			"faq_score_boost": chatManage.FAQScoreBoost,
		},
		Metadata: map[string]interface{}{
			"session_id": chatManage.SessionID,
		},
	})
	ctx = rerankCtx

	logRerankInputScoreSample(ctx, chatManage.SearchResult)

	opts := reranking.Options{
		Threshold:        chatManage.RerankThreshold,
		TopK:             max(1, chatManage.RerankTopK),
		FallbackMinScore: reranking.FallbackMinScore(chatManage.SearchTargets.HasRecallThresholdOverride()),
	}
	if chatManage.FAQPriorityEnabled {
		opts.FAQScoreBoost = chatManage.FAQScoreBoost
	}
	res := reranking.Rerank(ctx, rerankModel, chatManage.RewriteQuery, chatManage.SearchResult, opts)
	*diag = res.Diagnostics
	diag.ModelID = chatManage.RerankModelID

	if diag.Outcome == types.RerankOutcomeModelError {
		// Rerank API failed — fall back to the retrieval results so the
		// pipeline can still return something useful to the caller.
		pipelineWarn(ctx, "Rerank", "api_error_fallback", map[string]interface{}{
			"error":         diag.Error,
			"candidate_cnt": diag.CandidateCount,
		})
		chatManage.SearchResult = res.Results
		rerankSpan.Finish(map[string]interface{}{
			"stage":           "api_error_fallback",
			"candidate_count": diag.CandidateCount,
			"error":           diag.Error,
		}, nil, nil)
		return next()
	}

	chatManage.RerankResult = res.Results
	for i := 0; i < min(3, len(res.Scored)); i++ {
		pipelineInfo(ctx, "Rerank", "composite_top", map[string]interface{}{
			"rank":        i + 1,
			"chunk_id":    res.Scored[i].ID,
			"base_score":  res.Scored[i].Metadata["base_score"],
			"final_score": fmt.Sprintf("%.4f", res.Scored[i].Score),
		})
	}
	rerankSpan.Finish(buildRerankSpanOutput(res, chatManage), nil, nil)

	if len(chatManage.RerankResult) == 0 {
		pipelineWarn(ctx, "Rerank", "output", map[string]interface{}{
			"filtered_cnt": 0,
			"outcome":      diag.Outcome,
			"top_score":    diag.TopScore,
			"threshold":    diag.EffectiveThreshold,
		})
		return ErrSearchNothing
	}
	pipelineInfo(ctx, "Rerank", "output", map[string]interface{}{
		"filtered_cnt": len(chatManage.RerankResult),
		"outcome":      diag.Outcome,
	})
	return next()
}

func buildRerankSpanOutput(res *reranking.Result, chatManage *types.ChatManage) map[string]interface{} {
	candidates, passages, modelScores := res.Candidates, res.Passages, res.ModelScores
	modelRows := make([]map[string]interface{}, 0, len(modelScores))
	for i, rr := range modelScores {
		row := map[string]interface{}{
			"rank":        i + 1,
			"index":       rr.Index,
			"model_score": rr.RelevanceScore,
		}
		if rr.Index >= 0 && rr.Index < len(candidates) {
			row["chunk_id"] = candidates[rr.Index].ID
			row["knowledge_id"] = candidates[rr.Index].KnowledgeID
			row["knowledge_title"] = candidates[rr.Index].KnowledgeTitle
			row["match_type"] = candidates[rr.Index].MatchType
			row["retrieval_score"] = candidates[rr.Index].Score
			if rr.Index < len(passages) {
				row["preview"] = langfuse.TruncateRunes(passages[rr.Index], 160)
			}
		}
		modelRows = append(modelRows, row)
	}

	out := map[string]interface{}{
		"candidate_count":     len(candidates),
		"model_result_count":  len(modelScores),
		"composite_count":     len(res.Scored),
		"final_count":         len(res.Results),
		"threshold":           chatManage.RerankThreshold,
		"effective_threshold": res.Diagnostics.EffectiveThreshold,
		"rerank_top_k":        chatManage.RerankTopK,
		"outcome":             res.Diagnostics.Outcome,
		"threshold_degraded":  res.Diagnostics.Outcome == types.RerankOutcomeThresholdDegraded,
		"passages_preview":    langfuse.SummarizePassagePreviews(candidates, passages, 25),
		"model_scores":        langfuse.SummarizeRankScores(modelRows, 50),
		"composite_results":   langfuse.SummarizeSearchResults(res.Scored, 25),
		"final_results":       langfuse.SummarizeSearchResults(res.Results, 25),
	}
	if len(modelScores) > 50 {
		out["model_scores_truncated"] = len(modelScores) - 50
	}
	return out
}

func logRerankInputScoreSample(ctx context.Context, results []*types.SearchResult) {
	const maxLogRows = 8
	limit := min(maxLogRows, len(results))
	for i := 0; i < limit; i++ {
		sr := results[i]
		pipelineInfo(ctx, "Rerank", "input_score", map[string]interface{}{
			"index":      i,
			"chunk_id":   sr.ID,
			"score":      fmt.Sprintf("%.4f", sr.Score),
			"match_type": sr.MatchType,
		})
	}
	if len(results) > limit {
		pipelineInfo(ctx, "Rerank", "input_score_summary", map[string]interface{}{
			"total":     len(results),
			"logged":    limit,
			"truncated": len(results) - limit,
		})
	}
}
