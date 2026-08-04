package chatpipeline

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const maxHistoryResults = 3

// filterHistoryResults retrieves history references and filters them by
// textual similarity to the current query. Only references that are above
// a Jaccard similarity threshold are kept, and their scores are discounted
// to reflect that they were not directly retrieved for the current query.
// Results already present in currentResults (by chunk ID) are excluded.
func filterHistoryResults(
	ctx context.Context,
	chatManage *types.ChatManage,
	currentResults []*types.SearchResult,
) []*types.SearchResult {
	const (
		// minSimilarity is the minimum Jaccard similarity between the current
		// query and a history chunk's content for it to be injected.
		minSimilarity = 0.15
		// historyScoreDiscount reduces the original score of history results
		// to rank them below freshly-retrieved results of similar relevance.
		historyScoreDiscount = 0.6
	)

	raw := getSearchResultFromHistory(chatManage)
	if len(raw) == 0 {
		return nil
	}

	// Build a set of chunk IDs already in current results for fast dedup
	existingIDs := make(map[string]struct{}, len(currentResults))
	for _, r := range currentResults {
		existingIDs[r.ID] = struct{}{}
	}

	// Use RewriteQuery if available (it's the cleaned-up retrieval query),
	// otherwise fall back to the original query.
	query := chatManage.RewriteQuery
	if query == "" {
		query = chatManage.Query
	}
	queryTokens := searchutil.TokenizeSimple(query)

	var filtered []*types.SearchResult
	for _, r := range raw {
		if _, exists := existingIDs[r.ID]; exists {
			continue
		}
		if r.KnowledgeBaseID != "" && !historyResultInCurrentScope(chatManage, r) {
			pipelineInfo(ctx, "Merge", "history_filter_drop", map[string]interface{}{
				"chunk_id": r.ID,
				"reason":   "outside_current_scope",
			})
			continue
		}
		contentTokens := searchutil.TokenizeSimple(r.Content)
		sim := searchutil.Jaccard(queryTokens, contentTokens)
		if sim < minSimilarity {
			pipelineInfo(ctx, "Merge", "history_filter_drop", map[string]interface{}{
				"chunk_id":   r.ID,
				"similarity": sim,
			})
			continue
		}
		if r.KnowledgeBaseID == "" {
			// External/history-only evidence remains excluded from chunk
			// feedback. A real KB chunk keeps a trackable match type because it
			// is again exposed to the model and can ground the new answer.
			r.MatchType = types.MatchTypeHistory
		} else {
			switch r.MatchType {
			case types.MatchTypeWebSearch, types.MatchTypeDataAnalysis, types.MatchTypeHistory:
				r.MatchType = types.MatchTypeKeywords
			}
		}
		baseScore := historyRetrievalBaseScore(r)
		r.Score = baseScore * historyScoreDiscount
		r.Metadata = ensureMetadata(r.Metadata)
		r.Metadata["history_reference"] = "true"
		r.Metadata["history_similarity"] = strings.TrimRight(strings.TrimRight(
			fmt.Sprintf("%.4f", sim), "0"), ".")
		r.Metadata["history_original_score"] = fmt.Sprintf("%.4f", baseScore)
		r.Metadata["history_discounted_score"] = fmt.Sprintf("%.4f", r.Score)
		filtered = append(filtered, r)

		pipelineInfo(ctx, "Merge", "history_filter_keep", map[string]interface{}{
			"chunk_id":   r.ID,
			"similarity": sim,
			"new_score":  r.Score,
		})

	}
	return filtered
}

func historyResultInCurrentScope(chatManage *types.ChatManage, result *types.SearchResult) bool {
	if chatManage == nil || result == nil || result.KnowledgeBaseID == "" {
		return false
	}
	for _, target := range chatManage.SearchTargets {
		if target == nil || target.KnowledgeBaseID != result.KnowledgeBaseID {
			continue
		}
		isWholeKB := target.Type == types.SearchTargetTypeKnowledgeBase &&
			len(target.KnowledgeIDs) == 0 &&
			len(target.TagIDs) == 0 &&
			len(target.ScopeTagIDs) == 0
		if isWholeKB {
			return true
		}
		for _, knowledgeID := range target.KnowledgeIDs {
			if knowledgeID == result.KnowledgeID {
				return true
			}
		}
	}
	// Older pipeline callers may populate only KnowledgeBaseIDs. Treat those as
	// whole-KB scope, but never let this fallback widen an explicit document or
	// tag-constrained SearchTargets scope.
	if len(chatManage.SearchTargets) == 0 {
		for _, knowledgeBaseID := range chatManage.KnowledgeBaseIDs {
			if knowledgeBaseID == result.KnowledgeBaseID {
				return true
			}
		}
	}
	return false
}

func historyRetrievalBaseScore(result *types.SearchResult) float64 {
	if result == nil {
		return 0
	}
	if value := result.Metadata["recall_weight_original_score"]; value != "" {
		if score, err := strconv.ParseFloat(value, 64); err == nil {
			return score
		}
	}
	return result.Score
}

// refreshHistoryRecallWeights resolves the current database state before
// selecting the limited history candidates. This prevents a persisted old
// weight from affecting a later answer and drops deleted KB chunks.
func (p *PluginMerge) refreshHistoryRecallWeights(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
) []*types.SearchResult {
	if len(results) == 0 {
		return nil
	}

	chunkIDs := make([]string, 0, len(results))
	for _, result := range results {
		if result != nil && result.KnowledgeBaseID != "" {
			chunkIDs = append(chunkIDs, result.ID)
		}
	}

	weights := make(map[string]float64, len(chunkIDs))
	found := make(map[string]struct{}, len(chunkIDs))
	lookupSucceeded := false
	lookupFailed := len(chunkIDs) > 0 && p.chunkRepo == nil
	if len(chunkIDs) > 0 && p.chunkRepo != nil {
		chunks, err := p.chunkRepo.ListChunksByIDOnly(ctx, chunkIDs)
		if err != nil {
			lookupFailed = true
			pipelineWarn(ctx, "Merge", "history_weight_load", map[string]interface{}{
				"session_id": chatManage.SessionID,
				"error":      err.Error(),
			})
		} else {
			lookupSucceeded = true
			for _, chunk := range chunks {
				if chunk == nil {
					continue
				}
				weight := chunk.RecallWeight
				if weight == 0 {
					weight = 1.0
				}
				weights[chunk.ID] = weight
				found[chunk.ID] = struct{}{}
			}
		}
	}

	refreshed := make([]*types.SearchResult, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		if result.KnowledgeBaseID == "" {
			refreshed = append(refreshed, result)
			continue
		}
		if lookupFailed {
			continue
		}

		weight := 1.0
		if lookupSucceeded {
			if _, ok := found[result.ID]; !ok {
				continue
			}
			weight = weights[result.ID]
		}

		result.RecallWeight = weight
		result.Score *= weight
		result.Metadata = ensureMetadata(result.Metadata)
		result.Metadata["recall_weight"] = fmt.Sprintf("%.2f", weight)
		result.Metadata["history_weighted_score"] = fmt.Sprintf("%.4f", result.Score)
		refreshed = append(refreshed, result)
	}

	sort.SliceStable(refreshed, func(i, j int) bool {
		return refreshed[i].Score > refreshed[j].Score
	})
	if len(refreshed) > maxHistoryResults {
		refreshed = refreshed[:maxHistoryResults]
	}
	return refreshed
}
