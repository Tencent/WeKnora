package chatpipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

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
		// maxHistoryResults caps the number of history results injected to
		// avoid overwhelming the context with stale references.
		maxHistoryResults = 3
	)

	raw := getSearchResultFromHistory(chatManage)
	if len(raw) == 0 {
		return nil
	}
	raw = filterHistoryResultsByFolderScope(ctx, chatManage, raw)
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
		sim := searchutil.Jaccard(queryTokens, contentTokens)
		if sim < minSimilarity {
			pipelineInfo(ctx, "Merge", "history_filter_drop", map[string]interface{}{
				"chunk_id":   r.ID,
				"similarity": sim,
			})
			continue
		}
		r.MatchType = types.MatchTypeHistory
		r.Score = r.Score * historyScoreDiscount
		r.Metadata = ensureMetadata(r.Metadata)
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

func filterHistoryResultsByFolderScope(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
) []*types.SearchResult {
	if chatManage == nil || len(chatManage.FolderKnowledgeIDs) == 0 || len(results) == 0 {
		return results
	}
	allowedByKB := make(map[string]map[string]struct{}, len(chatManage.FolderKnowledgeIDs))
	for kbID, ids := range chatManage.FolderKnowledgeIDs {
		set := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if id == "" {
				continue
			}
			set[id] = struct{}{}
		}
		allowedByKB[kbID] = set
	}

	filtered := make([]*types.SearchResult, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		if result.KnowledgeID == "" {
			pipelineInfo(ctx, "Merge", "history_folder_drop", map[string]interface{}{
				"chunk_id": result.ID,
				"reason":   "missing_knowledge_id",
			})
			continue
		}
		if result.KnowledgeBaseID == "" {
			pipelineInfo(ctx, "Merge", "history_folder_drop", map[string]interface{}{
				"chunk_id": result.ID,
				"reason":   "missing_knowledge_base_id",
			})
			continue
		}
		allowedSet, restricted := allowedByKB[result.KnowledgeBaseID]
		if !restricted {
			filtered = append(filtered, result)
			continue
		}
		if _, ok := allowedSet[result.KnowledgeID]; ok {
			filtered = append(filtered, result)
			continue
		}
		pipelineInfo(ctx, "Merge", "history_folder_drop", map[string]interface{}{
			"chunk_id":           result.ID,
			"knowledge_id":       result.KnowledgeID,
			"knowledge_base_id":  result.KnowledgeBaseID,
			"folder_scope_empty": len(allowedSet) == 0,
		})
	}
	return filtered
}
