package chatpipline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/searchutil"
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
	pipelineInfo(ctx, "Rerank", "input", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"candidate_cnt": len(chatManage.SearchResult),
		"rerank_model":  chatManage.RerankModelID,
		"rerank_thresh": chatManage.RerankThreshold,
		"rewrite_query": chatManage.RewriteQuery,
	})
	if len(chatManage.SearchResult) == 0 {
		pipelineInfo(ctx, "Rerank", "skip", map[string]interface{}{
			"reason": "empty_search_result",
		})
		return next()
	}
	if chatManage.RerankModelID == "" {
		pipelineWarn(ctx, "Rerank", "skip", map[string]interface{}{
			"reason": "empty_model_id",
		})
		return next()
	}

	// Get rerank model from service
	rerankModel, err := p.modelService.GetRerankModel(ctx, chatManage.RerankModelID)
	if err != nil {
		pipelineError(ctx, "Rerank", "get_model", map[string]interface{}{
			"model_id": chatManage.RerankModelID,
			"error":    err.Error(),
		})
		return ErrGetRerankModel.WithError(err)
	}

	// Prepare passages for reranking (excluding DirectLoad results)
	var passages []string
	var candidatesToRerank []*types.SearchResult
	var directLoadResults []*types.SearchResult

	for _, result := range chatManage.SearchResult {
		if result.MatchType == types.MatchTypeDirectLoad {
			directLoadResults = append(directLoadResults, result)
			pipelineInfo(ctx, "Rerank", "direct_load_skip", map[string]interface{}{
				"chunk_id": result.ID,
			})
			continue
		}
		// Content와 ImageInfo의 텍스트 내용을 병합합니다.
		passage := getEnrichedPassage(ctx, result)
		passages = append(passages, passage)
		candidatesToRerank = append(candidatesToRerank, result)
	}

	pipelineInfo(ctx, "Rerank", "build_passages", map[string]interface{}{
		"total_cnt":     len(chatManage.SearchResult),
		"candidate_cnt": len(candidatesToRerank),
		"direct_cnt":    len(directLoadResults),
	})

	var rerankResp []rerank.RankResult

	// Only call rerank model if there are candidates
	if len(candidatesToRerank) > 0 {
		// Single rerank call with RewriteQuery, use threshold degradation if no results
		originalThreshold := chatManage.RerankThreshold
		rerankResp = p.rerank(ctx, chatManage, rerankModel, chatManage.RewriteQuery, passages, candidatesToRerank)

		// If no results and threshold is high enough, try with lower threshold
		if len(rerankResp) == 0 && originalThreshold > 0.3 {
			degradedThreshold := originalThreshold * 0.7
			if degradedThreshold < 0.3 {
				degradedThreshold = 0.3
			}
			pipelineInfo(ctx, "Rerank", "threshold_degrade", map[string]interface{}{
				"original": originalThreshold,
				"degraded": degradedThreshold,
			})
			chatManage.RerankThreshold = degradedThreshold
			rerankResp = p.rerank(ctx, chatManage, rerankModel, chatManage.RewriteQuery, passages, candidatesToRerank)
			// Restore original threshold
			chatManage.RerankThreshold = originalThreshold
		}
	}

	pipelineInfo(ctx, "Rerank", "model_response", map[string]interface{}{
		"result_cnt": len(rerankResp),
	})

	// Log input scores before reranking for debugging
	for i, sr := range chatManage.SearchResult {
		pipelineInfo(ctx, "Rerank", "input_score", map[string]interface{}{
			"index":      i,
			"chunk_id":   sr.ID,
			"score":      fmt.Sprintf("%.4f", sr.Score),
			"match_type": sr.MatchType,
		})
	}

	for i := range chatManage.SearchResult {
		chatManage.SearchResult[i].Metadata = ensureMetadata(chatManage.SearchResult[i].Metadata)
	}
	reranked := make([]*types.SearchResult, 0, len(rerankResp)+len(directLoadResults))

	// Process reranked results
	for _, rr := range rerankResp {
		if rr.Index >= len(candidatesToRerank) {
			continue
		}
		sr := candidatesToRerank[rr.Index]
		base := sr.Score
		sr.Metadata["base_score"] = fmt.Sprintf("%.4f", base)
		modelScore := rr.RelevanceScore
		sr.Score = compositeScore(sr, modelScore, base)

		// Apply FAQ score boost if enabled
		if chatManage.FAQPriorityEnabled && chatManage.FAQScoreBoost > 1.0 &&
			sr.ChunkType == string(types.ChunkTypeFAQ) {
			originalScore := sr.Score
			sr.Score = math.Min(sr.Score*chatManage.FAQScoreBoost, 1.0)
			sr.Metadata["faq_boosted"] = "true"
			sr.Metadata["faq_original_score"] = fmt.Sprintf("%.4f", originalScore)
			pipelineInfo(ctx, "Rerank", "faq_boost", map[string]interface{}{
				"chunk_id":       sr.ID,
				"original_score": fmt.Sprintf("%.4f", originalScore),
				"boosted_score":  fmt.Sprintf("%.4f", sr.Score),
				"boost_factor":   chatManage.FAQScoreBoost,
			})
		}

		pipelineInfo(ctx, "Rerank", "composite_calc", map[string]interface{}{
			"chunk_id":    sr.ID,
			"base_score":  fmt.Sprintf("%.4f", base),
			"model_score": fmt.Sprintf("%.4f", modelScore),
			"final_score": fmt.Sprintf("%.4f", sr.Score),
			"match_type":  sr.MatchType,
		})
		reranked = append(reranked, sr)
	}

	// Process direct load results (bypass rerank model, assume high relevance)
	for _, sr := range directLoadResults {
		base := sr.Score
		sr.Metadata["base_score"] = fmt.Sprintf("%.4f", base)
		// Assign high model score for direct load items
		modelScore := 1.0
		sr.Score = compositeScore(sr, modelScore, base)
		pipelineInfo(ctx, "Rerank", "composite_calc_direct", map[string]interface{}{
			"chunk_id":    sr.ID,
			"base_score":  fmt.Sprintf("%.4f", base),
			"model_score": fmt.Sprintf("%.4f", modelScore),
			"final_score": fmt.Sprintf("%.4f", sr.Score),
			"match_type":  sr.MatchType,
		})
		reranked = append(reranked, sr)
	}
	final := applyMMR(ctx, reranked, chatManage, min(len(reranked), max(1, chatManage.RerankTopK)), 0.7)
	chatManage.RerankResult = final

	// Log composite top scores and MMR selection summary
	topN := min(3, len(reranked))
	for i := 0; i < topN; i++ {
		pipelineInfo(ctx, "Rerank", "composite_top", map[string]interface{}{
			"rank":        i + 1,
			"chunk_id":    reranked[i].ID,
			"base_score":  reranked[i].Metadata["base_score"],
			"final_score": fmt.Sprintf("%.4f", reranked[i].Score),
		})
	}

	if len(chatManage.RerankResult) == 0 {
		pipelineWarn(ctx, "Rerank", "output", map[string]interface{}{
			"filtered_cnt": 0,
		})
		return ErrSearchNothing
	}

	pipelineInfo(ctx, "Rerank", "output", map[string]interface{}{
		"filtered_cnt": len(chatManage.RerankResult),
	})
	return next()
}

// rerank performs the actual reranking operation with given query and passages
func (p *PluginRerank) rerank(ctx context.Context,
	chatManage *types.ChatManage, rerankModel rerank.Reranker, query string, passages []string,
	candidates []*types.SearchResult,
) []rerank.RankResult {
	pipelineInfo(ctx, "Rerank", "model_call", map[string]interface{}{
		"query_variant": query,
		"passages":      len(passages),
	})
	rerankResp, err := rerankModel.Rerank(ctx, query, passages)
	if err != nil {
		pipelineError(ctx, "Rerank", "model_call", map[string]interface{}{
			"query_variant": query,
			"error":         err.Error(),
		})
		return nil
	}

	// Log top scores for debugging
	pipelineInfo(ctx, "Rerank", "threshold", map[string]interface{}{
		"threshold": chatManage.RerankThreshold,
	})
	for i := range min(5, len(rerankResp)) {
		if rerankResp[i].Index < len(candidates) {
			pipelineInfo(ctx, "Rerank", "top_score", map[string]interface{}{
				"rank":       i + 1,
				"score":      rerankResp[i].RelevanceScore,
				"chunk_id":   candidates[rerankResp[i].Index].ID,
				"match_type": candidates[rerankResp[i].Index].MatchType,
				"chunk_type": candidates[rerankResp[i].Index].ChunkType,
				"content":    candidates[rerankResp[i].Index].Content,
			})
		}
	}

	// Filter results based on threshold with special handling for history matches
	rankFilter := []rerank.RankResult{}
	for _, result := range rerankResp {
		if result.Index >= len(candidates) {
			continue
		}
		th := chatManage.RerankThreshold
		matchType := candidates[result.Index].MatchType
		if matchType == types.MatchTypeHistory {
			th = math.Max(th-0.1, 0.5) // Lower threshold for history matches
		}
		if result.RelevanceScore > th {
			rankFilter = append(rankFilter, result)
		}
	}
	return rankFilter
}

// ensureMetadata ensures the metadata is not nil
func ensureMetadata(m map[string]string) map[string]string {
	if m == nil {
		return make(map[string]string)
	}
	return m
}

// compositeScore calculates the composite score for a search result
func compositeScore(sr *types.SearchResult, modelScore, baseScore float64) float64 {
	sourceWeight := 1.0
	switch strings.ToLower(sr.KnowledgeSource) {
	case "web_search":
		sourceWeight = 0.95
	default:
		sourceWeight = 1.0
	}
	positionPrior := 1.0
	if sr.StartAt >= 0 {
		positionPrior += searchutil.ClampFloat(1.0-float64(sr.StartAt)/float64(sr.EndAt+1), -0.05, 0.05)
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

// applyMMR applies the MMR algorithm to the search results with pre-computed token sets
func applyMMR(
	ctx context.Context,
	results []*types.SearchResult,
	chatManage *types.ChatManage,
	k int,
	lambda float64,
) []*types.SearchResult {
	if k <= 0 || len(results) == 0 {
		return nil
	}
	pipelineInfo(ctx, "Rerank", "mmr_start", map[string]interface{}{
		"lambda":     lambda,
		"k":          k,
		"candidates": len(results),
	})

	// Pre-compute all token sets upfront (optimization)
	allTokenSets := make([]map[string]struct{}, len(results))
	for i, r := range results {
		allTokenSets[i] = searchutil.TokenizeSimple(getEnrichedPassage(ctx, r))
	}

	selected := make([]*types.SearchResult, 0, k)
	selectedTokenSets := make([]map[string]struct{}, 0, k)
	selectedIndices := make(map[int]struct{})

	for len(selected) < k && len(selectedIndices) < len(results) {
		bestIdx := -1
		bestScore := -1.0

		for i, r := range results {
			if _, isSelected := selectedIndices[i]; isSelected {
				continue
			}

			relevance := r.Score
			redundancy := 0.0

			// Use pre-computed token sets for redundancy calculation
			for _, selTokens := range selectedTokenSets {
				sim := searchutil.Jaccard(allTokenSets[i], selTokens)
				if sim > redundancy {
					redundancy = sim
				}
			}

			mmr := lambda*relevance - (1.0-lambda)*redundancy
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}

		selected = append(selected, results[bestIdx])
		selectedTokenSets = append(selectedTokenSets, allTokenSets[bestIdx])
		selectedIndices[bestIdx] = struct{}{}
	}

	// Compute average redundancy among selected using pre-computed token sets
	avgRed := 0.0
	if len(selected) > 1 {
		pairs := 0
		for i := 0; i < len(selectedTokenSets); i++ {
			for j := i + 1; j < len(selectedTokenSets); j++ {
				avgRed += searchutil.Jaccard(selectedTokenSets[i], selectedTokenSets[j])
				pairs++
			}
		}
		if pairs > 0 {
			avgRed /= float64(pairs)
		}
	}
	pipelineInfo(ctx, "Rerank", "mmr_done", map[string]interface{}{
		"selected":       len(selected),
		"avg_redundancy": fmt.Sprintf("%.4f", avgRed),
	})
	return selected
}

// getEnrichedPassage Content, ImageInfo 및 GeneratedQuestions의 텍스트 내용을 병합합니다.
func getEnrichedPassage(ctx context.Context, result *types.SearchResult) string {
	combinedText := result.Content
	var enrichments []string

	// ImageInfo 파싱
	if result.ImageInfo != "" {
		var imageInfos []types.ImageInfo
		err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos)
		if err != nil {
			pipelineWarn(ctx, "Rerank", "image_info_parse", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			// 모든 이미지의 설명 및 OCR 텍스트 추출
			for _, img := range imageInfos {
				if img.Caption != "" {
					enrichments = append(enrichments, fmt.Sprintf("이미지 설명: %s", img.Caption))
				}
				if img.OCRText != "" {
					enrichments = append(enrichments, fmt.Sprintf("이미지 텍스트: %s", img.OCRText))
				}
			}
		}
	}

	// ChunkMetadata의 GeneratedQuestions 파싱
	if len(result.ChunkMetadata) > 0 {
		var docMeta types.DocumentChunkMetadata
		err := json.Unmarshal(result.ChunkMetadata, &docMeta)
		if err != nil {
			pipelineWarn(ctx, "Rerank", "chunk_metadata_parse", map[string]interface{}{
				"error": err.Error(),
			})
		} else if questionStrings := docMeta.GetQuestionStrings(); len(questionStrings) > 0 {
			enrichments = append(enrichments, fmt.Sprintf("관련 질문: %s", strings.Join(questionStrings, "; ")))
		}
	}

	if len(enrichments) == 0 {
		return combinedText
	}

	// 내용과 강화된 정보 결합
	if combinedText != "" {
		combinedText += "\n\n"
	}
	combinedText += strings.Join(enrichments, "\n")

	pipelineInfo(ctx, "Rerank", "passage_enrich", map[string]interface{}{
		"content_len":    len(result.Content),
		"enrichment":     strings.Join(enrichments, "\n"),
		"enrichment_len": len(strings.Join(enrichments, "\n")),
	})

	return combinedText
}
