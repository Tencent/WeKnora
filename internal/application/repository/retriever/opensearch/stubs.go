package opensearch

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// This file ships every behavioural method of the OpenSearch driver as a
// stub returning ErrFeatureNotEnabled (or, for EstimateStorageSize, a
// conservative lower-bound). The package is a hollow, interface-compliant
// shell at this commit — every real code path is gated dead code (no
// registry / factory / env path mentions this driver), and any accidental
// invocation surfaces loudly.
//
// Follow-up commits land the real implementations: the read/write methods
// (Save / BatchSave / DeleteBy* / Retrieve) migrate to dedicated files
// (crud.go / retrieve.go + supporting query.go); the async / batch paths
// (CopyIndices, BatchUpdate*) and the real EstimateStorageSize arrive
// alongside the activation switch.

// Save is replaced by a real bulk-driven single-doc path in a later commit.
func (r *Repository) Save(
	_ context.Context, _ *types.IndexInfo, _ map[string]any,
) error {
	return ErrFeatureNotEnabled
}

// BatchSave is replaced by a real dim-aware pre-marshal + per-item-error
// inspection path in a later commit.
func (r *Repository) BatchSave(
	_ context.Context, _ []*types.IndexInfo, _ map[string]any,
) error {
	return ErrFeatureNotEnabled
}

// DeleteByChunkIDList is replaced by a capped sync _delete_by_query path
// in a later commit.
func (r *Repository) DeleteByChunkIDList(
	_ context.Context, _ []string, _ int, _ string,
) error {
	return ErrFeatureNotEnabled
}

// DeleteBySourceIDList is replaced by a capped sync _delete_by_query path
// in a later commit.
func (r *Repository) DeleteBySourceIDList(
	_ context.Context, _ []string, _ int, _ string,
) error {
	return ErrFeatureNotEnabled
}

// DeleteByKnowledgeIDList is replaced by a capped sync _delete_by_query
// path in a later commit.
func (r *Repository) DeleteByKnowledgeIDList(
	_ context.Context, _ []string, _ int, _ string,
) error {
	return ErrFeatureNotEnabled
}

// Retrieve is replaced by a dim-resolve + per-dim alias k-NN / BM25 path
// in a later commit.
func (r *Repository) Retrieve(
	_ context.Context, _ types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	return nil, ErrFeatureNotEnabled
}

// CopyIndices: the async _reindex path with task polling for >10K-doc
// batches arrives in a later change. Until then this is unreachable in
// production (no registry registration path activates the OpenSearch
// driver), but the stub fails closed if reached.
func (r *Repository) CopyIndices(
	_ context.Context,
	_ string, // sourceKnowledgeBaseID
	_ map[string]string, // sourceToTargetKBIDMap
	_ map[string]string, // sourceToTargetChunkIDMap
	_ string, // targetKnowledgeBaseID
	_ int, // dimension
	_ string, // knowledgeType
) error {
	return ErrFeatureNotEnabled
}

// BatchUpdateChunkEnabledStatus: the _update_by_query path arrives in a
// later change.
func (r *Repository) BatchUpdateChunkEnabledStatus(
	_ context.Context, _ map[string]bool,
) error {
	return ErrFeatureNotEnabled
}

// BatchUpdateChunkTagID: the _update_by_query path arrives in a later
// change.
func (r *Repository) BatchUpdateChunkTagID(
	_ context.Context, _ map[string]string,
) error {
	return ErrFeatureNotEnabled
}

// EstimateStorageSize: the real implementation that reads cluster `_stats`
// for the per-dim alias arrives in a later change. For now we return a
// conservative lower-bound estimate using the HNSW memory formula so the
// upstream KB-delete guard fails-closed (treats non-empty KBs as "may free
// non-trivial storage, force confirmation") rather than failing open.
//
// Formula: N * (1024 content bytes + 4*dimGuess float + 128 HNSW M=16 overhead)
// dimGuess = 768 (common embedding size; the real implementation reads the
// actual dim from the cluster).
func (r *Repository) EstimateStorageSize(
	_ context.Context,
	indexInfoList []*types.IndexInfo,
	_ map[string]any,
) int64 {
	if len(indexInfoList) == 0 {
		return 0
	}
	const (
		contentBytes = 1024 // average chunk content
		embDimGuess  = 768  // common embedding size
		hnswOverhead = 128  // M=16 → 8*M = 128 bytes/vector
	)
	return int64(len(indexInfoList)) * int64(contentBytes+4*embDimGuess+hnswOverhead)
}

// swapToVersion is a stub for the future rolling-reindex swap path.
// Calling it is illegal at this stage — alias is fixed at "_v1" and only
// a follow-up change ships the swap orchestration. The surface exists so
// the future API contract is reviewable now.
//
// Unexported because it is not part of the public
// RetrieveEngineRepository interface — exposing it would require an
// interface widening in internal/types/interfaces.
func (r *Repository) swapToVersion(_ context.Context, _ int, _ int) error {
	return ErrFeatureNotEnabled
}
