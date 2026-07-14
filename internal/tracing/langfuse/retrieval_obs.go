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
		"total_hits":   0,
		"vector_hits":  0,
		"keyword_hits": 0,
		"group_count":  len(results),
		"by_retriever": []map[string]interface{}{},
		"top_hits":     []map[string]interface{}{},
	}
	if len(results) == 0 {
		return out
	}

	byRetriever := make([]map[string]interface{}, 0, len(results))
	var all []*types.IndexWithScore
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
		all = append(all, rr.Results...)
	}
	out["by_retriever"] = byRetriever
	out["top_hits"] = summarizeIndexHits(all, defaultHitPreviewLimit)
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
			"index":             i,
			"chunk_id":          sr.ID,
			"knowledge_id":      sr.KnowledgeID,
			"knowledge_title":   sr.KnowledgeTitle,
			"retrieval_score":   fmt.Sprintf("%.4f", sr.Score),
			"match_type":        sr.MatchType,
			"preview":           TruncateRunes(passages[i], 160),
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
