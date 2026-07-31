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
	if chatManage.ExecutionScope != nil {
		raw = filterHistoryByExecutionScope(
			raw,
			chatManage.ExecutionScope,
		)
		if len(raw) == 0 {
			return nil
		}
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
			"similarity": sim,
			"new_score":  r.Score,
		})

		if len(filtered) >= maxHistoryResults {
			break
		}
	}
	return filtered
}

func filterHistoryByExecutionScope(
	results []*types.SearchResult,
	scope *types.KnowledgeScope,
) []*types.SearchResult {
	if scope == nil {
		return results
	}
	wholeKnowledgeBases := make(map[string]struct{})
	knowledgeIDsByKB := make(map[string]map[string]struct{})
	for _, target := range scope.Targets() {
		if target.FolderFilter().Enabled() {
			continue
		}
		if len(target.TagIDs()) > 0 {
			// Historical results do not carry authoritative physical tag
			// membership, so a tag-constrained target cannot be revalidated.
			continue
		}
		targetKnowledgeIDs := target.KnowledgeIDs()
		if len(targetKnowledgeIDs) > 0 {
			if knowledgeIDsByKB[target.KnowledgeBaseID()] == nil {
				knowledgeIDsByKB[target.KnowledgeBaseID()] =
					make(map[string]struct{}, len(targetKnowledgeIDs))
			}
			for _, knowledgeID := range targetKnowledgeIDs {
				knowledgeIDsByKB[target.KnowledgeBaseID()][knowledgeID] =
					struct{}{}
			}
			continue
		}
		if len(target.ScopeTagIDs()) > 0 {
			continue
		}
		wholeKnowledgeBases[target.KnowledgeBaseID()] = struct{}{}
	}

	filtered := make([]*types.SearchResult, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		if knowledgeIDs := knowledgeIDsByKB[result.KnowledgeBaseID]; knowledgeIDs != nil {
			if _, allowed := knowledgeIDs[result.KnowledgeID]; allowed {
				filtered = append(filtered, result)
				continue
			}
		}
		if _, allowed := wholeKnowledgeBases[result.KnowledgeBaseID]; allowed {
			filtered = append(filtered, result)
		}
	}
	return filtered
}
