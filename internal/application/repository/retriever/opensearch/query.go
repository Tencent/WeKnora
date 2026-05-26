package opensearch

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// retrieveFilters is the typed shape of OpenSearch query filter
// clauses. It is the ONLY way callers (Retrieve) can constrain a
// query — there is no JSON-fragment escape hatch. This prevents the
// "map[string]interface{} injection" failure mode where a hostile
// chunk_id like {"$or":[...]} would otherwise be inlined as JSON DSL.
//
// All slice fields are interpreted as IN-set membership (OS terms
// query). Empty slice = no constraint on that dimension.
//
// IncludeDisabled controls the implicit is_enabled = true clause:
//   - false (default) — appends is_enabled term, hiding disabled chunks
//   - true             — omits the clause; admin / audit callers can
//     observe disabled chunks. Service layer pins false for user-facing
//     APIs.
type retrieveFilters struct {
	KBIDs               []string
	KnowledgeIDs        []string
	TagIDs              []string
	ExcludeChunkIDs     []string
	ExcludeKnowledgeIDs []string
	IncludeDisabled     bool
}

// fromParams projects RetrieveParams to retrieveFilters, applying the
// multi-KB input from the service-layer dispatcher as terms.
// ExcludeKnowledgeIDs is included so the OpenSearch driver matches the
// behaviour of the Qdrant / pgvector drivers.
func fromParams(p types.RetrieveParams) *retrieveFilters {
	return &retrieveFilters{
		KBIDs:               p.KnowledgeBaseIDs,
		KnowledgeIDs:        p.KnowledgeIDs,
		TagIDs:              p.TagIDs,
		ExcludeChunkIDs:     p.ExcludeChunkIDs,
		ExcludeKnowledgeIDs: p.ExcludeKnowledgeIDs,
		// IncludeDisabled stays false — set explicitly by admin callers
		// only. Driver receives this from a typed field, not from
		// AdditionalParams, so the contract is checked at compile time.
	}
}

// toBoolMust emits the OpenSearch bool.must clause list for this
// filter. Returns a freshly allocated slice each call so callers can
// safely append additional clauses (e.g. the keyword match clause in
// buildKeywordQuery) without aliasing.
func (f *retrieveFilters) toBoolMust() []map[string]any {
	var must []map[string]any
	if len(f.KBIDs) > 0 {
		must = append(must, map[string]any{
			"terms": map[string]any{"knowledge_base_id": f.KBIDs},
		})
	}
	if len(f.KnowledgeIDs) > 0 {
		must = append(must, map[string]any{
			"terms": map[string]any{"knowledge_id": f.KnowledgeIDs},
		})
	}
	if len(f.TagIDs) > 0 {
		must = append(must, map[string]any{
			"terms": map[string]any{"tag_id": f.TagIDs},
		})
	}
	if len(f.ExcludeChunkIDs) > 0 {
		must = append(must, map[string]any{
			"bool": map[string]any{
				"must_not": map[string]any{
					"terms": map[string]any{"chunk_id": f.ExcludeChunkIDs},
				},
			},
		})
	}
	if len(f.ExcludeKnowledgeIDs) > 0 {
		must = append(must, map[string]any{
			"bool": map[string]any{
				"must_not": map[string]any{
					"terms": map[string]any{"knowledge_id": f.ExcludeKnowledgeIDs},
				},
			},
		})
	}
	if !f.IncludeDisabled {
		must = append(must, map[string]any{
			"term": map[string]any{"is_enabled": true},
		})
	}
	return must
}

// buildKNNQuery composes a pure k-NN ANN query with caller filters. No
// hybrid pipeline — native hybrid is out of scope for this PR; fusion
// stays in the service-layer RRF.
//
// Applies `min_score` when threshold > 0. The OpenSearch k-NN plugin's
// SpaceType.COSINESIMIL.scoreTranslation produces (1 + cos) / 2 ∈ [0, 1]
// at the client, so the caller's Threshold value passes through to
// min_score directly without further scaling.
//
// Signature returns ([]byte, error) so callers can propagate
// json.Marshal failures (reachable for NaN / +/-Inf in embedding).
func buildKNNQuery(embedding []float32, topK int, threshold float64, f *retrieveFilters) ([]byte, error) {
	knn := map[string]any{
		"embedding": map[string]any{
			"vector": embedding,
			"k":      topK,
			"filter": map[string]any{
				"bool": map[string]any{"must": f.toBoolMust()},
			},
		},
	}
	body := map[string]any{
		"size":  topK,
		"query": map[string]any{"knn": knn},
	}
	if threshold > 0 {
		body["min_score"] = threshold
	}
	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal knn query: %w", err)
	}
	return out, nil
}

// buildKeywordQuery composes a BM25 match query against the content
// field with caller filters. Score range is OpenSearch BM25 native (no
// upper bound) — the normalizer keyword-passthrough rule treats keyword
// retrievers as rank-based for RRF, so the score scale does not need
// normalization here. min_score still applied if requested.
func buildKeywordQuery(queryText string, topK int, threshold float64, f *retrieveFilters) ([]byte, error) {
	must := f.toBoolMust()
	must = append(must, map[string]any{
		"match": map[string]any{"content": queryText},
	})
	body := map[string]any{
		"size":  topK,
		"query": map[string]any{"bool": map[string]any{"must": must}},
	}
	if threshold > 0 {
		body["min_score"] = threshold
	}
	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal keyword query: %w", err)
	}
	return out, nil
}
