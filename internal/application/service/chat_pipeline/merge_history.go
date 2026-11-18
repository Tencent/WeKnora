package chatpipeline

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/reranking"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// filterHistoryResults retrieves history references and filters them by
// how much of the current query they cover. Only references whose token
// overlap with the query reaches a threshold are kept, and their scores are
// discounted to reflect that they were not directly retrieved for the
// current query. Results already present in currentResults (by chunk ID)
// are excluded.
//
// Overlap is measured against the smaller token set (in practice the query).
// Jaccard divided by the union, so a query of about 8 terms against a chunk of
// a few hundred never passed 0.05 and the 0.15 threshold kept nothing.
func filterHistoryResults(
	ctx context.Context,
	chatManage *types.ChatManage,
	currentResults []*types.SearchResult,
) []*types.SearchResult {
	const (
		// minSimilarity is the minimum share of query terms (overlap
		// coefficient) a history chunk must contain to be injected.
		minSimilarity = 0.3
		// historyScoreDiscount reduces the original score of history results
		// to rank them below freshly-retrieved results of similar relevance.
		historyScoreDiscount = 0.6
		// maxHistoryResults caps the number of history results injected to
		// avoid overwhelming the context with stale references.
		maxHistoryResults = 3
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
		contentTokens := searchutil.TokenizeSimple(r.Content)
		sim := searchutil.TokenOverlapRatio(queryTokens, contentTokens)
		if sim < minSimilarity {
			pipelineInfo(ctx, "Merge", "history_filter_drop", map[string]interface{}{
				"chunk_id":   r.ID,
				"similarity": sim,
			})
			continue
		}
		r.MatchType = types.MatchTypeHistory
		r.Score = r.Score * historyScoreDiscount
		if r.Metadata == nil {
			r.Metadata = make(map[string]string)
		}
		r.Metadata["history_similarity"] = strings.TrimRight(strings.TrimRight(
			fmt.Sprintf("%.4f", sim), "0"), ".")
		filtered = append(filtered, r)

		pipelineInfo(ctx, "Merge", "history_filter_keep", map[string]interface{}{
			"chunk_id":   r.ID,
			"similarity": sim,
			"new_score":  r.Score,
		})

		if len(filtered) >= maxHistoryResults {
			break
		}
	}
	return filtered
}

// rerankHistoryResults scores the token-filtered history candidates with the
// session's rerank model and keeps only those whose relevance score reaches
// the rerank threshold — the same bar the fresh candidates had to clear at
// the CHUNK_RERANK stage. History injection runs in CHUNK_MERGE, after
// PluginRerank, so before this gate a chunk that merely shared generic
// domain words with the query (TokenOverlapRatio >= 0.3) entered the context
// without any relevance judgment (#5613).
//
// When no rerank model is configured, or the model cannot be loaded or
// called, the legacy token-overlap filter keeps deciding, mirroring how the
// rerank stage itself degrades.
func (p *PluginMerge) rerankHistoryResults(
	ctx context.Context,
	chatManage *types.ChatManage,
	history []*types.SearchResult,
) []*types.SearchResult {
	if p.modelService == nil || chatManage.RerankModelID == "" || len(history) == 0 {
		return history
	}
	rerankModel, err := p.modelService.GetRerankModel(ctx, chatManage.RerankModelID)
	if err != nil {
		pipelineWarn(ctx, "Merge", "history_rerank_model_fallback", map[string]interface{}{
			"model_id": chatManage.RerankModelID,
			"error":    err.Error(),
		})
		return history
	}
	query := chatManage.RewriteQuery
	if query == "" {
		query = chatManage.Query
	}
	limit := rerank.MaxPassageRunes(rerankModel, query)
	passages := make([]string, len(history))
	for i, r := range history {
		passage := reranking.ModelPassage(ctx, r)
		if limit > 0 && utf8.RuneCountInString(passage) > limit {
			passage = string([]rune(passage)[:limit])
		}
		passages[i] = passage
	}
	scores, err := rerankModel.Rerank(ctx, query, passages)
	if err != nil {
		pipelineWarn(ctx, "Merge", "history_rerank_fallback", map[string]interface{}{
			"error": err.Error(),
		})
		return history
	}
	threshold := chatManage.RerankThreshold
	if threshold <= 0 {
		threshold = reranking.DefaultThreshold
	}
	scored := make(map[int]float64, len(scores))
	for _, s := range scores {
		if s.Index >= 0 && s.Index < len(history) {
			scored[s.Index] = s.RelevanceScore
		}
	}
	kept := make([]*types.SearchResult, 0, len(history))
	for i, r := range history {
		score, ok := scored[i]
		if !ok || score < threshold {
			pipelineInfo(ctx, "Merge", "history_rerank_drop", map[string]interface{}{
				"chunk_id":  r.ID,
				"score":     score,
				"threshold": threshold,
				"scored":    ok,
			})
			continue
		}
		if r.Metadata == nil {
			r.Metadata = make(map[string]string)
		}
		r.Metadata["history_rerank_score"] = strings.TrimRight(strings.TrimRight(
			fmt.Sprintf("%.4f", score), "0"), ".")
		kept = append(kept, r)
	}
	pipelineInfo(ctx, "Merge", "history_rerank", map[string]interface{}{
		"candidates": len(history),
		"kept":       len(kept),
		"threshold":  threshold,
	})
	return kept
}
