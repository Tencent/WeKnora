package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Retrieve dispatches to vectorRetrieve or keywordsRetrieve based on
// params.RetrieverType. Returns exactly one *RetrieveResult — the
// service-layer multi-store dispatcher depends on this one-bundle-per-
// driver protocol. Score in result is RAW [0, 1] passthrough (the
// OpenSearch k-NN plugin's SpaceType.COSINESIMIL.scoreTranslation has
// already mapped to (1 + cos) / 2 before the score reaches us).
//
// Dim resolution order:
//  1. params.AdditionalParams["dim"] if present and positive
//  2. len(params.Embedding) if positive
//  3. cross-dim multi-index search for keyword-only paths via
//     "<base>_*" wildcard pattern
//
// Vector retrieve always requires a positive dim (it's intrinsic to k-NN).
func (r *Repository) Retrieve(
	ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	dim, multiIndex := resolveDim(params)

	switch params.RetrieverType {
	case types.VectorRetrieverType:
		if dim == 0 {
			return nil, fmt.Errorf(
				"opensearch: vector retrieve requires embedding or AdditionalParams[\"dim\"]: %w",
				ErrDimensionMismatch)
		}
		if err := r.ensureReady(ctx, dim); err != nil {
			return nil, err
		}
		return r.vectorRetrieve(ctx, params, dim)
	case types.KeywordsRetrieverType:
		if multiIndex {
			// BM25-only path without dim — search across "<base>_*"
			// wildcard pattern. Slower than a single-index search but
			// correct for admin / keyword-only callers.
			return r.keywordsRetrieve(ctx, params, r.baseIndex+"_*")
		}
		if err := r.ensureReady(ctx, dim); err != nil {
			return nil, err
		}
		return r.keywordsRetrieve(ctx, params, r.indexAlias(dim))
	default:
		return nil, fmt.Errorf("opensearch: unsupported retriever type %q", params.RetrieverType)
	}
}

// resolveDim returns (dim, multiIndex). multiIndex=true means dim is
// unknown and the caller should use a wildcard alias pattern (keyword
// path only — vector retrieve cannot be dim-less).
func resolveDim(p types.RetrieveParams) (int, bool) {
	if p.AdditionalParams != nil {
		if v, ok := p.AdditionalParams["dim"].(int); ok && v > 0 {
			return v, false
		}
	}
	if len(p.Embedding) > 0 {
		return len(p.Embedding), false
	}
	return 0, true // unknown — multi-index fallback for keyword
}

func (r *Repository) vectorRetrieve(
	ctx context.Context, p types.RetrieveParams, dim int,
) ([]*types.RetrieveResult, error) {
	body, err := buildKNNQuery(p.Embedding, effectiveTopK(ctx, p), p.Threshold, fromParams(p))
	if err != nil {
		return nil, err
	}
	hits, err := r.search(ctx, r.indexAlias(dim), body)
	if err != nil {
		return nil, err
	}
	return wrapResults(ctx, hits, types.VectorRetrieverType, types.MatchTypeEmbedding), nil
}

func (r *Repository) keywordsRetrieve(
	ctx context.Context, p types.RetrieveParams, indexPattern string,
) ([]*types.RetrieveResult, error) {
	body, err := buildKeywordQuery(p.Query, effectiveTopK(ctx, p), p.Threshold, fromParams(p))
	if err != nil {
		return nil, err
	}
	hits, err := r.search(ctx, indexPattern, body)
	if err != nil {
		return nil, err
	}
	return wrapResults(ctx, hits, types.KeywordsRetrieverType, types.MatchTypeKeywords), nil
}

// effectiveTopK returns the caller's TopK or 10 if unset. The 10 is a
// defensive default — every real caller sets TopK, so a TopK=10 in
// production logs indicates a caller bug. Caps at 10000 to stay below
// OpenSearch's index.max_result_window default.
func effectiveTopK(ctx context.Context, p types.RetrieveParams) int {
	if p.TopK <= 0 {
		logger.GetLogger(ctx).Warnf("[OpenSearch] Retrieve called with TopK<=0; defaulting to 10 (caller bug?)")
		return 10
	}
	if p.TopK > 10000 {
		return 10000
	}
	return p.TopK
}

func (r *Repository) search(ctx context.Context, indexPattern string, body []byte) ([]hit, error) {
	req := osapi.SearchReq{Indices: []string{indexPattern}, Body: bytes.NewReader(body)}
	resp, err := r.client.Search(ctx, &req)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("opensearch: index %s missing: %w", indexPattern, ErrIndexNotFound)
		}
		return nil, wrapTransport(err)
	}
	defer func() {
		if resp != nil {
			drainAndClose(resp.Inspect().Response.Body)
		}
	}()
	// Cap response body at 16MB (search results bounded by max_result_window * doc_size).
	return parseSearchHits(io.LimitReader(resp.Inspect().Response.Body, 16<<20))
}

// hit is the structural shape we pull from each OpenSearch search hit.
// Field-by-field decode (vs map[string]any) keeps the JSON shape
// pinned at compile time.
type hit struct {
	ID     string  `json:"_id"`   // equals chunk_id per the indexing invariant
	Score  float64 `json:"_score"`
	Source struct {
		Content         string `json:"content"`
		ChunkID         string `json:"chunk_id"`
		KnowledgeID     string `json:"knowledge_id"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
		SourceID        string `json:"source_id"`
		SourceType      int    `json:"source_type"` // integer, not stringified
		TagID           string `json:"tag_id"`
		IsEnabled       bool   `json:"is_enabled"`
	} `json:"_source"`
}

func parseSearchHits(body io.Reader) ([]hit, error) {
	var resp struct {
		Hits struct {
			Hits []hit `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("opensearch: parse search response: %w", ErrTransport)
	}
	return resp.Hits.Hits, nil
}

// wrapResults converts OpenSearch hits to the upstream IndexWithScore
// shape. Always returns exactly one *RetrieveResult — the service-layer
// multi-store dispatcher depends on this "one engine result per driver"
// contract.
//
// Defensive: warns if hit._id != _source.chunk_id (we set _id=chunk_id
// on every write, so a mismatch means external tooling has been writing
// into the index) but does not fail the query — the result is still
// usable for ranking, just suspicious.
func wrapResults(ctx context.Context, hits []hit, rt types.RetrieverType, mt types.MatchType) []*types.RetrieveResult {
	log := logger.GetLogger(ctx)
	out := make([]*types.IndexWithScore, 0, len(hits))
	for _, h := range hits {
		if h.ID != h.Source.ChunkID {
			log.Warnf("[OpenSearch] hit._id=%q != _source.chunk_id=%q (D12 invariant violation)",
				h.ID, h.Source.ChunkID)
		}
		out = append(out, &types.IndexWithScore{
			ID:              h.ID,
			ChunkID:         h.Source.ChunkID,
			KnowledgeID:     h.Source.KnowledgeID,
			KnowledgeBaseID: h.Source.KnowledgeBaseID,
			SourceID:        h.Source.SourceID,
			SourceType:      types.SourceType(h.Source.SourceType),
			TagID:           h.Source.TagID,
			Content:         h.Source.Content,
			Score:           h.Score,
			MatchType:       mt,
			IsEnabled:       h.Source.IsEnabled,
		})
	}
	return []*types.RetrieveResult{{
		Results:             out,
		RetrieverEngineType: types.OpenSearchRetrieverEngineType,
		RetrieverType:       rt,
	}}
}
