package chatpipline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
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
		// 合并Content和ImageInfo的文本内容
		passage := getEnrichedPassage(ctx, result)
		// Skip passages that become empty after cleaning
		if strings.TrimSpace(passage) == "" {
			pipelineInfo(ctx, "Rerank", "empty_passage_skip", map[string]interface{}{
				"chunk_id": result.ID,
			})
			continue
		}
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

	logRerankInputScoreSample(ctx, chatManage.SearchResult)

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

		reranked = append(reranked, sr)
	}

	// Process direct load results (bypass rerank model, assume high relevance)
	for _, sr := range directLoadResults {
		base := sr.Score
		sr.Metadata["base_score"] = fmt.Sprintf("%.4f", base)
		// Assign high model score for direct load items
		modelScore := 1.0
		sr.Score = compositeScore(sr, modelScore, base)
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

	// Filter out empty or whitespace-only passages before sending to the API
	var cleanPassages []string
	var cleanCandidates []*types.SearchResult
	for i, p := range passages {
		if strings.TrimSpace(p) != "" {
			cleanPassages = append(cleanPassages, p)
			if i < len(candidates) {
				cleanCandidates = append(cleanCandidates, candidates[i])
			}
		}
	}
	if len(cleanPassages) == 0 {
		pipelineInfo(ctx, "Rerank", "model_call_skip", map[string]interface{}{
			"reason": "all_passages_empty",
		})
		return nil
	}
	passages = cleanPassages
	candidates = cleanCandidates

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
	logged := min(5, len(rerankResp))
	for i := range logged {
		if rerankResp[i].Index < len(candidates) {
			pipelineInfo(ctx, "Rerank", "top_score", map[string]interface{}{
				"rank":        i + 1,
				"score":       rerankResp[i].RelevanceScore,
				"chunk_id":    candidates[rerankResp[i].Index].ID,
				"match_type":  candidates[rerankResp[i].Index].MatchType,
				"chunk_type":  candidates[rerankResp[i].Index].ChunkType,
				"content_len": len(candidates[rerankResp[i].Index].Content),
			})
		}
	}
	if len(rerankResp) > logged {
		pipelineInfo(ctx, "Rerank", "top_score_summary", map[string]interface{}{
			"total":     len(rerankResp),
			"logged":    logged,
			"truncated": len(rerankResp) - logged,
		})
	}

	// Filter results based on threshold
	rankFilter := []rerank.RankResult{}
	for _, result := range rerankResp {
		if result.Index >= len(candidates) {
			continue
		}
		if result.RelevanceScore >= chatManage.RerankThreshold {
			rankFilter = append(rankFilter, result)
		}
	}

	// Fallback: if threshold filtering removed all results but the top candidate
	// still has a reasonable score, keep it as a safety net. Skip fallback entirely
	// when the best score is too low — forcing irrelevant results is worse than
	// returning nothing and letting the caller handle the empty-result case.
	const fallbackMinScore = 0.15
	if len(rankFilter) == 0 && len(rerankResp) > 0 && rerankResp[0].RelevanceScore >= fallbackMinScore {
		rankFilter = rerankResp[:1]
		pipelineInfo(ctx, "Rerank", "fallback_top1", map[string]interface{}{
			"reason":    "all_below_threshold",
			"threshold": chatManage.RerankThreshold,
			"top_score": rerankResp[0].RelevanceScore,
		})
	} else if len(rankFilter) == 0 {
		pipelineInfo(ctx, "Rerank", "fallback_skip", map[string]interface{}{
			"reason":    "top_score_too_low",
			"threshold": chatManage.RerankThreshold,
			"top_score": safeTopScore(rerankResp),
		})
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

func safeTopScore(results []rerank.RankResult) float64 {
	if len(results) == 0 {
		return 0
	}
	return results[0].RelevanceScore
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

// --- Passage cleaning for rerank ---
//
// Rerank models work on semantic text similarity. Markdown formatting, raw URLs,
// image references, table separators, and other structural syntax are noise that
// can dilute the semantic signal. The functions below strip this noise before
// passages are sent to the rerank model.

var (
	// reMarkdownImage matches ![alt](url) — the entire construct is noise.
	// URL group supports one level of balanced parentheses.
	reMarkdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`)
	// reLinkedImage matches [![alt](img_url)](link_url) — unwrap to ![alt](img_url)
	// so that the subsequent reMarkdownImage pass can remove the image.
	reLinkedImage = regexp.MustCompile(
		`\[!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)\]` +
			`\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`,
	)
	// reMarkdownLink matches [text](url) — we keep the text, drop the URL.
	// URL group supports one level of balanced parentheses.
	reMarkdownLink = regexp.MustCompile(`\[([^\]]+)\]\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`)
	// reRawURL matches standalone http(s) URLs.
	reRawURL = regexp.MustCompile(`https?://[^\s)\]>]+`)
	// reCodeBlock matches fenced code blocks (```...```).
	reCodeBlock = regexp.MustCompile("(?s)```(?:\\w*)\n?.*?```")
	// reLatexBlock matches block-level LaTeX ($$...$$).
	reLatexBlock = regexp.MustCompile(`(?s)\$\$.*?\$\$`)
	// reTableSep matches table separator rows like |---|---|.
	reTableSep = regexp.MustCompile(`(?m)^\s*\|[\s:|-]+\|\s*$`)
	// reTableRow matches markdown table data rows like | col1 | col2 |.
	reTableRow = regexp.MustCompile(`(?m)^\s*\|(.+?)\|\s*$`)
	// reHeadingPrefix matches leading # markers in headings.
	reHeadingPrefix = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	// reBlockquote matches leading > markers.
	reBlockquote = regexp.MustCompile(`(?m)^>\s?`)
	// reBoldItalic3 matches ***text*** wrappers (must come before 2 and 1).
	reBoldItalic3 = regexp.MustCompile(`\*{3}(.+?)\*{3}`)
	// reBoldItalic2 matches **text** wrappers.
	reBoldItalic2 = regexp.MustCompile(`\*{2}(.+?)\*{2}`)
	// reBoldItalic1 matches *text* wrappers.
	reBoldItalic1 = regexp.MustCompile(`\*(.+?)\*`)
	// reExcessiveNewlines collapses 3+ consecutive newlines into 2.
	reExcessiveNewlines = regexp.MustCompile(`\n{3,}`)
	// reListMarker matches unordered (- , * ) and ordered (1. ) list prefixes.
	reListMarker = regexp.MustCompile(`(?m)^[\t ]*(?:[-*+]|\d+\.)\s+`)
	// reHTMLTag matches HTML tags like <br>, <div class="...">, etc.
	reHTMLTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// cleanPassageForRerank strips markdown/structural noise from text to produce
// a clean semantic passage for the rerank model. The cleaning is designed to
// preserve all meaningful natural-language content while removing formatting
// that would confuse text-similarity scoring.
func cleanPassageForRerank(text string) string {
	// 1. Remove code blocks (before other patterns to avoid partial matches)
	text = reCodeBlock.ReplaceAllString(text, "")
	// 2. Remove LaTeX block math
	text = reLatexBlock.ReplaceAllString(text, "")
	// 3. Remove HTML tags
	text = reHTMLTag.ReplaceAllString(text, "")
	// 3.5. Unwrap nested [![alt](img_url)](link_url) → ![alt](img_url)
	//      so that the next step removes the full construct cleanly.
	text = reLinkedImage.ReplaceAllString(text, "![$1]($2)")
	// 4. Remove markdown image references entirely
	text = reMarkdownImage.ReplaceAllString(text, "")
	// 5. Convert markdown links to just their display text
	text = reMarkdownLink.ReplaceAllString(text, "$1")
	// 6. Remove standalone raw URLs
	text = reRawURL.ReplaceAllString(text, "")
	// 7. Remove table separator rows
	text = reTableSep.ReplaceAllString(text, "")
	// 7.5. Convert table data rows to plain text (strip | delimiters)
	text = reTableRow.ReplaceAllStringFunc(text, func(match string) string {
		inner := reTableRow.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		cells := strings.Split(inner[1], "|")
		var parts []string
		for _, cell := range cells {
			cell = strings.TrimSpace(cell)
			if cell != "" {
				parts = append(parts, cell)
			}
		}
		return strings.Join(parts, ", ")
	})
	// 8. Strip heading markers but keep heading text
	text = reHeadingPrefix.ReplaceAllString(text, "")
	// 9. Strip blockquote markers
	text = reBlockquote.ReplaceAllString(text, "")
	// 10. Unwrap bold/italic markers, keeping inner text (order: *** before ** before *)
	text = reBoldItalic3.ReplaceAllString(text, "$1")
	text = reBoldItalic2.ReplaceAllString(text, "$1")
	text = reBoldItalic1.ReplaceAllString(text, "$1")
	// 11. Strip list markers
	text = reListMarker.ReplaceAllString(text, "")
	// 12. Collapse excessive newlines
	text = reExcessiveNewlines.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// getEnrichedPassage 合并Content、ImageInfo和GeneratedQuestions的文本内容
func getEnrichedPassage(ctx context.Context, result *types.SearchResult) string {
	combinedText := cleanPassageForRerank(result.Content)
	var enrichments []string

	// 解析ImageInfo
	if result.ImageInfo != "" {
		var imageInfos []types.ImageInfo
		err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos)
		if err != nil {
			pipelineWarn(ctx, "Rerank", "image_info_parse", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			// 提取所有图片的描述和OCR文本
			for _, img := range imageInfos {
				if img.Caption != "" {
					enrichments = append(enrichments, img.Caption)
				}
				if img.OCRText != "" {
					enrichments = append(enrichments, img.OCRText)
				}
			}
		}
	}

	// 解析ChunkMetadata中的GeneratedQuestions
	if len(result.ChunkMetadata) > 0 {
		var docMeta types.DocumentChunkMetadata
		err := json.Unmarshal(result.ChunkMetadata, &docMeta)
		if err != nil {
			pipelineWarn(ctx, "Rerank", "chunk_metadata_parse", map[string]interface{}{
				"error": err.Error(),
			})
		} else if questionStrings := docMeta.GetQuestionStrings(); len(questionStrings) > 0 {
			enrichments = append(enrichments, strings.Join(questionStrings, "; "))
		}
	}

	if len(enrichments) == 0 {
		return combinedText
	}

	// 组合内容和增强信息
	if combinedText != "" {
		combinedText += "\n\n"
	}
	combinedText += strings.Join(enrichments, "\n")

	return combinedText
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
