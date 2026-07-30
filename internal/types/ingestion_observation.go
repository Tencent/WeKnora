package types

import "context"

// IngestionOperation identifies one logical expensive-computation operation
// in the knowledge ingestion pipeline.
//
// An operation is more fine-grained than a processing stage. For example, the
// postprocess stage may contain summary, question, wiki, and graph operations.
type IngestionOperation string

const (
	IngestionOperationParseDocument IngestionOperation = "parse.document"

	IngestionOperationMultimodalOCR     IngestionOperation = "multimodal.ocr"
	IngestionOperationMultimodalCaption IngestionOperation = "multimodal.caption"

	IngestionOperationEmbeddingBatch         IngestionOperation = "embedding.batch"
	IngestionOperationEmbeddingChunk         IngestionOperation = "embedding.chunk"
	IngestionOperationEmbeddingSummary       IngestionOperation = "embedding.summary"
	IngestionOperationEmbeddingQuestion      IngestionOperation = "embedding.question"
	IngestionOperationEmbeddingFAQ           IngestionOperation = "embedding.faq"
	IngestionOperationEmbeddingGraphEntity   IngestionOperation = "embedding.graph_entity"
	IngestionOperationEmbeddingGraphRelation IngestionOperation = "embedding.graph_relation"
	IngestionOperationEmbeddingWikiPage      IngestionOperation = "embedding.wiki_page"

	IngestionOperationPostprocessSummary  IngestionOperation = "postprocess.summary"
	IngestionOperationPostprocessQuestion IngestionOperation = "postprocess.question"

	IngestionOperationWikiExtract     IngestionOperation = "wiki.extract"
	IngestionOperationWikiSummary     IngestionOperation = "wiki.summary"
	IngestionOperationWikiClassify    IngestionOperation = "wiki.classify"
	IngestionOperationWikiReduce      IngestionOperation = "wiki.reduce"
	IngestionOperationWikiDeduplicate IngestionOperation = "wiki.deduplicate"
	IngestionOperationWikiIndexIntro  IngestionOperation = "wiki.index_intro"

	IngestionOperationGraphExtractChunk IngestionOperation = "graph.extract_chunk"
)

// IngestionCacheStatus describes the cache outcome of an ingestion operation.
//
// PR1 does not implement a cache. Its production observations should normally
// use IngestionCacheStatusNotSupported and must not report a hit or miss before
// a real cache lookup has taken place.
type IngestionCacheStatus string

const (
	IngestionCacheStatusUnavailable  IngestionCacheStatus = "unavailable"
	IngestionCacheStatusNotSupported IngestionCacheStatus = "not_supported"

	// Reserved for later cache implementation PRs.
	IngestionCacheStatusHit   IngestionCacheStatus = "hit"
	IngestionCacheStatusMiss  IngestionCacheStatus = "miss"
	IngestionCacheStatusError IngestionCacheStatus = "error"
)

// IngestionOperationObservation contains non-sensitive statistics for one
// logical ingestion operation.
//
// OperationCount, RequestCount, and TotalItems describe different quantities:
//
//   - OperationCount is the number of logical business operations.
//   - RequestCount is the number of calls entering a model provider adapter.
//   - TotalItems is the number of input items handled by the operation.
//
// RequestCount does not include retries hidden inside a provider SDK or remote
// gateway because those retries are not visible through WeKnora's interfaces.
type IngestionOperationObservation struct {
	Operation IngestionOperation
	Stage     string

	ModelID   string
	ModelType string
	Provider  string

	OperationCount int
	RequestCount   int
	BatchCount     int

	TotalItems    int
	ComputedItems int
	ReusedItems   int

	InputChars  int
	InputBytes  int64
	OutputChars int
	OutputBytes int64

	CacheStatus IngestionCacheStatus
	Success     bool
	ErrorCode   string

	ChunkID    string
	ChunkIndex *int
	ImageIndex *int

	// These fields are reserved for later reusable-computation PRs. PR1 must
	// not calculate or persist a formal cache key.
	ArtifactKind           string
	InputDigestPrefix      string
	DependencyDigestPrefix string
	ArtifactSchemaVersion  string
	// ArtifactCacheEvent is an optional infrastructure-level detail such as
	// claimed, busy, computed, failed, or lease_takeover. CacheStatus keeps its
	// original PR1 hit/miss/error meaning.
	ArtifactCacheEvent string
}

// ToJSONMap converts the observation into the JSON representation stored in a
// knowledge processing span.
//
// Count fields are always included because zero is meaningful. For example, an
// image read failure should explicitly report request_count=0.
func (o IngestionOperationObservation) ToJSONMap() JSONMap {
	output := JSONMap{
		"operation":       string(o.Operation),
		"operation_count": nonNegativeInt(o.OperationCount),
		"request_count":   nonNegativeInt(o.RequestCount),
		"batch_count":     nonNegativeInt(o.BatchCount),
		"total_items":     nonNegativeInt(o.TotalItems),
		"computed_items":  nonNegativeInt(o.ComputedItems),
		"reused_items":    nonNegativeInt(o.ReusedItems),
		"input_chars":     nonNegativeInt(o.InputChars),
		"input_bytes":     nonNegativeInt64(o.InputBytes),
		"output_chars":    nonNegativeInt(o.OutputChars),
		"output_bytes":    nonNegativeInt64(o.OutputBytes),
		"success":         o.Success,
	}

	if o.Stage != "" {
		output["stage"] = o.Stage
	}
	if o.ModelID != "" {
		output["model_id"] = o.ModelID
	}
	if o.ModelType != "" {
		output["model_type"] = o.ModelType
	}
	if o.Provider != "" {
		output["provider"] = o.Provider
	}
	if o.CacheStatus != "" {
		output["cache_status"] = string(o.CacheStatus)
	}
	if o.ErrorCode != "" {
		output["error_code"] = o.ErrorCode
	}
	if o.ChunkID != "" {
		output["chunk_id"] = o.ChunkID
	}
	if o.ChunkIndex != nil {
		output["chunk_index"] = *o.ChunkIndex
	}
	if o.ImageIndex != nil {
		output["image_index"] = *o.ImageIndex
	}
	if o.ArtifactKind != "" {
		output["artifact_kind"] = o.ArtifactKind
	}
	if o.InputDigestPrefix != "" {
		output["input_digest_prefix"] = o.InputDigestPrefix
	}
	if o.DependencyDigestPrefix != "" {
		output["dependency_digest_prefix"] = o.DependencyDigestPrefix
	}
	if o.ArtifactSchemaVersion != "" {
		output["artifact_schema_version"] = o.ArtifactSchemaVersion
	}
	if o.ArtifactCacheEvent != "" {
		output["artifact_cache_event"] = o.ArtifactCacheEvent
	}

	return output
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

type ingestionOperationContextKey struct{}

// WithIngestionOperation returns a child context carrying the logical
// ingestion operation associated with the next expensive-computation call.
//
// The metadata is internal to the backend and is not exposed through the API.
func WithIngestionOperation(
	ctx context.Context,
	operation IngestionOperation,
) context.Context {
	if operation == "" {
		return ctx
	}
	return context.WithValue(ctx, ingestionOperationContextKey{}, operation)
}

// IngestionOperationFromContext returns the logical ingestion operation stored
// in ctx. An empty value means that the caller did not classify the operation.
func IngestionOperationFromContext(ctx context.Context) IngestionOperation {
	if ctx == nil {
		return ""
	}

	operation, _ := ctx.Value(ingestionOperationContextKey{}).(IngestionOperation)

	return operation
}
