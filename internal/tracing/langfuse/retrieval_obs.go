package langfuse

import (
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

const defaultHitPreviewLimit = 25

// TruncateRunes shortens s to at most maxRunes runes for trace payloads.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

// SummarizeRetrieveOutput builds Langfuse output for the retrieve span.
func SummarizeRetrieveOutput(results []*types.RetrieveResult) map[string]interface{} {
	out := map[string]interface{}{
		"total_hits":            0,
		"vector_hits":           0,
		"keyword_hits":          0,
		"group_count":           len(results),
		"by_retriever":          []map[string]interface{}{},
		"top_hits":              []map[string]interface{}{},
		"top_hits_by_retriever": []map[string]interface{}{},
		"top_hits_strategy":     "round_robin_by_retriever",
	}
	if len(results) == 0 {
		return out
	}

	byRetriever := make([]map[string]interface{}, 0, len(results))
	for _, rr := range results {
		if rr == nil {
			continue
		}
		count := len(rr.Results)
		out["total_hits"] = out["total_hits"].(int) + count
		if rr.RetrieverType == types.VectorRetrieverType {
			out["vector_hits"] = out["vector_hits"].(int) + count
		} else {
			out["keyword_hits"] = out["keyword_hits"].(int) + count
		}
		byRetriever = append(byRetriever, map[string]interface{}{
			"engine":    string(rr.RetrieverEngineType),
			"retriever": string(rr.RetrieverType),
			"count":     count,
		})
	}
	out["by_retriever"] = byRetriever
	topHits, topHitsByRetriever := summarizeRetrieveHits(results, defaultHitPreviewLimit)
	out["top_hits"] = topHits
	out["top_hits_by_retriever"] = topHitsByRetriever
	return out
}

// SummarizeSearchResults builds a compact ranked preview for rerank spans.
func SummarizeSearchResults(results []*types.SearchResult, limit int) map[string]interface{} {
	if limit <= 0 {
		limit = defaultHitPreviewLimit
	}
	out := map[string]interface{}{
		"count":    len(results),
		"top_hits": []map[string]interface{}{},
	}
	if len(results) == 0 {
		return out
	}

	sorted := append([]*types.SearchResult(nil), results...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Score != sorted[j].Score {
			return sorted[i].Score > sorted[j].Score
		}
		return sorted[i].ID < sorted[j].ID
	})

	hits := make([]map[string]interface{}, 0, minInt(limit, len(sorted)))
	for i, sr := range sorted {
		if i >= limit {
			break
		}
		item := map[string]interface{}{
			"rank":            i + 1,
			"chunk_id":        sr.ID,
			"knowledge_id":    sr.KnowledgeID,
			"knowledge_title": sr.KnowledgeTitle,
			"composite_score": fmt.Sprintf("%.4f", sr.Score),
			"match_type":      sr.MatchType,
			"chunk_type":      sr.ChunkType,
			"preview":         TruncateRunes(sr.Content, 160),
		}
		if sr.Metadata != nil {
			if base, ok := sr.Metadata["base_score"]; ok {
				item["retrieval_score"] = base
			}
			if model, ok := sr.Metadata["model_score"]; ok {
				item["model_score"] = model
			}
			if boosted, ok := sr.Metadata["faq_boosted"]; ok {
				item["faq_boosted"] = boosted
			}
			if orig, ok := sr.Metadata["faq_original_score"]; ok {
				item["faq_original_score"] = orig
			}
		}
		hits = append(hits, item)
	}
	out["top_hits"] = hits
	if len(sorted) > limit {
		out["truncated"] = len(sorted) - limit
	}
	return out
}

// SummarizeRankScores builds rerank model score rows for Langfuse output.
func SummarizeRankScores(
	results []map[string]interface{},
	limit int,
) []map[string]interface{} {
	if limit <= 0 {
		limit = defaultHitPreviewLimit
	}
	if len(results) <= limit {
		return results
	}
	out := make([]map[string]interface{}, limit)
	copy(out, results[:limit])
	return out
}

func summarizeIndexHits(hits []*types.IndexWithScore, limit int) []map[string]interface{} {
	if len(hits) == 0 {
		return nil
	}
	sorted := append([]*types.IndexWithScore(nil), hits...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Score != sorted[j].Score {
			return sorted[i].Score > sorted[j].Score
		}
		return sorted[i].ChunkID < sorted[j].ChunkID
	})

	n := minInt(limit, len(sorted))
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		hit := sorted[i]
		out = append(out, map[string]interface{}{
			"rank":              i + 1,
			"chunk_id":          hit.ChunkID,
			"knowledge_id":      hit.KnowledgeID,
			"knowledge_base_id": hit.KnowledgeBaseID,
			"score":             fmt.Sprintf("%.4f", hit.Score),
			"match_type":        hit.MatchType,
			"preview":           TruncateRunes(hit.Content, 160),
		})
	}
	return out
}

type retrieveHitGroup struct {
	engine    types.RetrieverEngineType
	retriever types.RetrieverType
	hits      []*types.IndexWithScore
}

type retrieveHitRef struct {
	engine    types.RetrieverEngineType
	retriever types.RetrieverType
	hit       *types.IndexWithScore
}

// summarizeRetrieveHits keeps the raw retrieve trace useful when score
// scales differ between retrievers. Vector similarity and BM25 scores are
// not directly comparable, so a global score sort can hide one retriever
// completely. The flat top_hits list is therefore selected round-robin from
// each retriever group, while top_hits_by_retriever preserves each group's
// own score ordering for detailed inspection.
func summarizeRetrieveHits(
	results []*types.RetrieveResult,
	limit int,
) ([]map[string]interface{}, []map[string]interface{}) {
	if limit <= 0 {
		limit = defaultHitPreviewLimit
	}

	groups := make([]retrieveHitGroup, 0, len(results))
	for _, rr := range results {
		if rr == nil || len(rr.Results) == 0 {
			continue
		}
		hits := append([]*types.IndexWithScore(nil), rr.Results...)
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Score != hits[j].Score {
				return hits[i].Score > hits[j].Score
			}
			return hits[i].ChunkID < hits[j].ChunkID
		})
		groups = append(groups, retrieveHitGroup{
			engine:    rr.RetrieverEngineType,
			retriever: rr.RetrieverType,
			hits:      hits,
		})
	}

	byRetriever := make([]map[string]interface{}, 0, len(groups))
	refs := make([]retrieveHitRef, 0, minInt(limit, len(results)))
	for _, group := range groups {
		byRetriever = append(byRetriever, map[string]interface{}{
			"engine":    string(group.engine),
			"retriever": string(group.retriever),
			"count":     len(group.hits),
			"top_hits":  summarizeIndexHits(group.hits, limit),
		})
	}

	for offset := 0; len(refs) < limit; offset++ {
		added := false
		for _, group := range groups {
			if offset >= len(group.hits) {
				continue
			}
			refs = append(refs, retrieveHitRef{
				engine:    group.engine,
				retriever: group.retriever,
				hit:       group.hits[offset],
			})
			added = true
			if len(refs) == limit {
				break
			}
		}
		if !added {
			break
		}
	}

	topHits := make([]map[string]interface{}, 0, len(refs))
	for i, ref := range refs {
		hit := ref.hit
		topHits = append(topHits, map[string]interface{}{
			"rank":              i + 1,
			"chunk_id":          hit.ChunkID,
			"knowledge_id":      hit.KnowledgeID,
			"knowledge_base_id": hit.KnowledgeBaseID,
			"score":             fmt.Sprintf("%.4f", hit.Score),
			"match_type":        hit.MatchType,
			"engine":            string(ref.engine),
			"retriever":         string(ref.retriever),
			"preview":           TruncateRunes(hit.Content, 160),
		})
	}
	return topHits, byRetriever
}

// SummarizePassagePreviews builds rerank passage previews aligned with candidates.
func SummarizePassagePreviews(
	candidates []*types.SearchResult,
	passages []string,
	limit int,
) []map[string]interface{} {
	if limit <= 0 {
		limit = defaultHitPreviewLimit
	}
	n := len(candidates)
	if len(passages) < n {
		n = len(passages)
	}
	if limit < n {
		n = limit
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		sr := candidates[i]
		out = append(out, map[string]interface{}{
			"index":           i,
			"chunk_id":        sr.ID,
			"knowledge_id":    sr.KnowledgeID,
			"knowledge_title": sr.KnowledgeTitle,
			"retrieval_score": fmt.Sprintf("%.4f", sr.Score),
			"match_type":      sr.MatchType,
			"preview":         TruncateRunes(passages[i], 160),
		})
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
