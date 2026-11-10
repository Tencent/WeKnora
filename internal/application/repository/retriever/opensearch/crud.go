package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// additionalParams contract (verified against
// elasticsearch/structs.go:ToDBVectorEmbedding and
// keywords_vector_hybrid_indexer.go:concurrentBatchSave on
// upstream/main):
//
//	additionalParams["embedding"]     map[string][]float32  // keyed by IndexInfo.SourceID
//	additionalParams["chunk_enabled"] map[string]bool       // keyed by IndexInfo.ChunkID
//
// Both keys may be absent. Missing "embedding" → keyword-only path
// (vector field omitted from the OpenSearch document, routed to the
// dim-less keywords index). Missing "chunk_enabled" → IndexInfo's own
// IsEnabled field is used as-is.

// Save indexes a single chunk. Idempotent: _id is set to chunk_id so
// re-indexing the same chunk overwrites instead of creating a duplicate.
func (r *Repository) Save(
	ctx context.Context,
	info *types.IndexInfo,
	params map[string]any,
) error {
	emb := lookupEmbedding(params, info.SourceID)
	enabled := lookupChunkEnabled(params, info.ChunkID, info.IsEnabled)
	dim := len(emb)

	var targetIndex string
	if dim > 0 {
		if err := r.ensureReady(ctx, dim); err != nil {
			return err
		}
		targetIndex = r.indexAlias(dim)
	} else {
		// Keyword-only path: no embedding supplied. Route to the
		// dim-less <base>_keywords index.
		if err := r.ensureKeywordsIndex(ctx); err != nil {
			return err
		}
		targetIndex = r.keywordsIndex()
	}

	doc, err := json.Marshal(toDoc(info, emb, enabled))
	if err != nil {
		return fmt.Errorf("opensearch: marshal doc: %w", err)
	}
	req := osapi.IndexReq{
		Index:      targetIndex,
		DocumentID: info.ChunkID, // _id = chunk_id (idempotent)
		Body:       bytes.NewReader(doc),
	}
	resp, err := r.client.Index(ctx, req)
	if err != nil {
		return wrapTransport(err)
	}
	if resp != nil {
		drainAndClose(resp.Inspect().Response.Body)
	}
	return nil
}

// BatchSave NDJSON-bulk-indexes chunks. Inspects per-item errors and
// returns a typed error if ANY item fails.
//
// Dim-aware pre-marshal size cap: estimateBulkBodyBytes predicts NDJSON
// size before allocating; over-cap callers get ErrBatchTooLarge instead
// of a 50MB allocation that's then rejected. The service layer is
// responsible for dim-aware sub-batching above this.
func (r *Repository) BatchSave(
	ctx context.Context,
	infos []*types.IndexInfo,
	params map[string]any,
) error {
	if len(infos) == 0 {
		return nil
	}
	emb, dim, err := extractBatchEmbeddings(params, infos)
	if err != nil {
		return err
	}

	// Pre-marshal size + count check.
	if estimated := estimateBulkBodyBytes(len(infos), dim); estimated > 10*1024*1024 {
		return fmt.Errorf(
			"opensearch: estimated bulk body %dB exceeds 10MB cap (n=%d, dim=%d): %w",
			estimated, len(infos), dim, ErrBatchTooLarge,
		)
	}
	if len(infos) > 1000 {
		return fmt.Errorf("opensearch: bulk n=%d exceeds 1000-doc cap: %w",
			len(infos), ErrBatchTooLarge)
	}

	// Route by dim: dim==0 means the entire batch is keyword-only
	// (concurrentBatchSaveNoEmbedding path), use the dim-less index.
	var alias string
	if dim == 0 {
		if err := r.ensureKeywordsIndex(ctx); err != nil {
			return err
		}
		alias = r.keywordsIndex()
	} else {
		if err := r.ensureReady(ctx, dim); err != nil {
			return err
		}
		alias = r.indexAlias(dim)
	}

	body, err := buildBulkBody(alias, infos, emb, params)
	if err != nil {
		return err
	}
	req := osapi.BulkReq{Body: bytes.NewReader(body)}
	resp, err := r.client.Bulk(ctx, req)
	if err != nil {
		return wrapTransport(err)
	}
	if resp != nil {
		defer drainAndClose(resp.Inspect().Response.Body)
	}
	// Cap response body at 64MB (bulks of 1000 items are ~1-2MB success,
	// can balloon with full per-item errors).
	return inspectBulkResponse(io.LimitReader(resp.Inspect().Response.Body, 64<<20))
}

// estimateBulkBodyBytes returns a rough NDJSON size in bytes. Slight
// overestimate so the cap fires before real allocation. Per-item:
//   - action header `{"index":{"_index":"...","_id":"..."}}\n` ≈ 100B
//   - doc body: dim*5 (float ASCII) + ~700B metadata + content ≈ 1024B
func estimateBulkBodyBytes(n, dim int) int {
	return n * (100 + dim*5 + 1024)
}

// buildBulkBody constructs the NDJSON request body. emb[i] may be nil/
// empty — toDoc handles the keyword-only case by omitting the
// "embedding" field. chunk_enabled lookup also happens per-doc.
func buildBulkBody(alias string, infos []*types.IndexInfo, emb [][]float32, params map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	for i, info := range infos {
		action := map[string]any{
			"index": map[string]any{
				"_index": alias,
				"_id":    info.ChunkID,
			},
		}
		actionJSON, err := json.Marshal(action)
		if err != nil {
			return nil, fmt.Errorf("opensearch: marshal bulk action %d: %w", i, err)
		}
		buf.Write(actionJSON)
		buf.WriteByte('\n')

		enabled := lookupChunkEnabled(params, info.ChunkID, info.IsEnabled)
		docJSON, err := json.Marshal(toDoc(info, emb[i], enabled))
		if err != nil {
			return nil, fmt.Errorf("opensearch: marshal doc %d: %w", i, err)
		}
		buf.Write(docJSON)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// inspectBulkResponse parses OpenSearch bulk response, returns a typed
// error if any item has a per-item .error. Surfaces up to 5 distinct
// error messages + total count for operator diagnosis.
//
// Does NOT include det.Error.Reason in the wrapped public error
// (Reason can contain document content fragments). Only ID + bounded
// enum Type is included. Full per-item Reason goes to log.Debug.
func inspectBulkResponse(body io.Reader) error {
	var r struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			ID     string `json:"_id"`
			Status int    `json:"status"`
			Error  *struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error,omitempty"`
		} `json:"items"`
	}
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return fmt.Errorf("opensearch: parse bulk response: %w", ErrTransport)
	}
	if !r.Errors {
		return nil
	}
	log := logger.GetLogger(context.Background())
	var msgs []string
	total := 0
	for _, item := range r.Items {
		for op, det := range item { // {"index": {...}}
			if det.Error == nil {
				continue
			}
			total++
			// Full reason → debug log only (may contain doc content).
			log.Debugf("[OpenSearch] bulk item err: op=%s id=%s type=%s reason=%s",
				op, det.ID, det.Error.Type, det.Error.Reason)
			if len(msgs) < 5 {
				// Public message: ID (operator-meaningful) + Type (bounded enum).
				msgs = append(msgs, fmt.Sprintf("[%s %s] %s", op, det.ID, det.Error.Type))
			}
		}
	}
	if total == 0 {
		return nil // shouldn't happen if r.Errors=true, but defensive
	}
	return fmt.Errorf("opensearch: bulk partial failure (%d items failed, first 5: %s): %w",
		total, strings.Join(msgs, "; "), ErrTransport)
}

// toDoc projects IndexInfo + embedding to the OpenSearch document
// shape. source_type is `int` (not stringified) — mapping declared as
// `integer` field for stable cross-enum-extension behavior. Omits the
// "embedding" field entirely when emb is empty (keyword-only doc, no
// k-NN graph entry).
func toDoc(info *types.IndexInfo, emb []float32, enabled bool) map[string]any {
	doc := map[string]any{
		"chunk_id":          info.ChunkID,
		"knowledge_id":      info.KnowledgeID,
		"knowledge_base_id": info.KnowledgeBaseID,
		"source_id":         info.SourceID,
		"source_type":       int(info.SourceType),
		"tag_id":            info.TagID,
		"content":           info.Content,
		"is_enabled":        enabled,
		"is_recommended":    info.IsRecommended,
	}
	if len(emb) > 0 {
		doc["embedding"] = emb
	}
	return doc
}

// lookupEmbedding returns the embedding vector for info.SourceID.
// Empty slice (not nil) indicates "no embedding provided" — caller
// MUST check len(emb) before calling ensureReady.
//
// Defense in depth: tolerates the wrong shape rather than crash. The
// service layer is responsible for getting params right; we degrade
// to keyword-only for this doc and log a warning instead of panicking
// downstream.
func lookupEmbedding(params map[string]any, sourceID string) []float32 {
	if params == nil {
		return nil
	}
	raw, ok := params["embedding"]
	if !ok {
		return nil
	}
	embMap, ok := raw.(map[string][]float32)
	if !ok {
		logger.GetLogger(context.Background()).Warnf(
			"[OpenSearch] additionalParams[\"embedding\"] is %T, want map[string][]float32", raw)
		return nil
	}
	return embMap[sourceID] // nil/empty if SourceID not in map
}

// lookupChunkEnabled overlays the chunk_enabled map on top of
// info.IsEnabled. Returns the overlay value if present, else the
// default (info.IsEnabled).
func lookupChunkEnabled(params map[string]any, chunkID string, def bool) bool {
	if params == nil {
		return def
	}
	raw, ok := params["chunk_enabled"]
	if !ok {
		return def
	}
	enMap, ok := raw.(map[string]bool)
	if !ok {
		return def
	}
	if v, present := enMap[chunkID]; present {
		return v
	}
	return def
}

// extractBatchEmbeddings projects the params["embedding"] map onto the
// info list, returning per-info embeddings (some may be empty) and the
// dimension inferred from the first non-empty entry. Returns dim=0 if
// no info has an embedding — caller routes to the keywords-only path.
//
// Defense in depth: all non-empty embeddings MUST share the same
// dimension. Mixed-dim → ErrDimensionMismatch.
func extractBatchEmbeddings(params map[string]any, infos []*types.IndexInfo) ([][]float32, int, error) {
	out := make([][]float32, len(infos))
	dim := 0
	for i, info := range infos {
		emb := lookupEmbedding(params, info.SourceID)
		out[i] = emb
		if len(emb) > 0 {
			if dim == 0 {
				dim = len(emb)
			} else if len(emb) != dim {
				return nil, 0, fmt.Errorf(
					"opensearch: embedding[%s] dim=%d != first non-empty dim=%d: %w",
					info.SourceID, len(emb), dim, ErrDimensionMismatch,
				)
			}
		}
	}
	return out, dim, nil
}

// DeleteByChunkIDList runs _delete_by_query with a terms filter on
// chunk_id. Sync only — large batches (>1000) are rejected with
// ErrBatchTooLarge; the async task path that lifts the cap arrives in
// a later change.
//
// Refresh policy: opensearch-go v4's typed Params.Refresh is *bool, so
// the wire-level "wait_for" value documented in the OpenSearch REST
// API is not directly expressible. Refresh:&true forces immediate
// segment flush (read-your-writes guaranteed, but expensive at scale).
// A follow-up can revisit via a custom raw request if telemetry shows
// the cost is material at production batch frequencies.
func (r *Repository) DeleteByChunkIDList(
	ctx context.Context, chunkIDs []string, dim int, _ string,
) error {
	return r.deleteByList(ctx, chunkIDs, dim, "chunk_id")
}

func (r *Repository) DeleteBySourceIDList(
	ctx context.Context, sourceIDs []string, dim int, _ string,
) error {
	return r.deleteByList(ctx, sourceIDs, dim, "source_id")
}

func (r *Repository) DeleteByKnowledgeIDList(
	ctx context.Context, knowledgeIDs []string, dim int, _ string,
) error {
	return r.deleteByList(ctx, knowledgeIDs, dim, "knowledge_id")
}

// deleteByList factors the common cap / empty / ensureReady / dispatch
// logic out of the three DeleteBy* methods. dim==0 routes to the
// dim-less keywords index.
func (r *Repository) deleteByList(
	ctx context.Context, ids []string, dim int, field string,
) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 1000 {
		return fmt.Errorf("opensearch: %s-delete batch %d > 1000 cap: %w",
			field, len(ids), ErrBatchTooLarge)
	}
	var index string
	if dim == 0 {
		if err := r.ensureKeywordsIndex(ctx); err != nil {
			return err
		}
		index = r.keywordsIndex()
	} else {
		if err := r.ensureReady(ctx, dim); err != nil {
			return err
		}
		index = r.indexAlias(dim)
	}
	return r.deleteByTerms(ctx, index, field, ids)
}

func (r *Repository) deleteByTerms(
	ctx context.Context, index, field string, values []string,
) error {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{
			"terms": map[string]any{field: values},
		},
	})
	if err != nil {
		return fmt.Errorf("opensearch: marshal delete body: %w", err)
	}
	refresh := true
	req := osapi.DocumentDeleteByQueryReq{
		Indices: []string{index},
		Body:    bytes.NewReader(body),
		Params:  osapi.DocumentDeleteByQueryParams{Refresh: &refresh},
	}
	resp, err := r.client.Document.DeleteByQuery(ctx, req)
	if err != nil {
		return wrapTransport(err)
	}
	if resp != nil {
		drainAndClose(resp.Inspect().Response.Body)
	}
	return nil
}
