package service

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// embeddingRequestSnapshot is an immutable copy of the embedding requests
// observed by ingestionObservedEmbedder.
type embeddingRequestSnapshot struct {
	RequestCount int
	BatchCount   int
	TotalItems   int
	InputChars   int
}

// ingestionObservedEmbedder records calls entering the embedding model
// interface while delegating all actual work to the wrapped embedder.
//
// It does not cache, skip, retry, or alter embedding results. Its only purpose
// is to provide request and input counts for knowledge-processing spans.
type ingestionObservedEmbedder struct {
	inner     embedding.Embedder
	operation types.IngestionOperation

	mu sync.Mutex

	requestCount int
	batchCount   int
	totalItems   int
	inputChars   int
}

var _ embedding.Embedder = (*ingestionObservedEmbedder)(nil)

// newIngestionObservedEmbedder wraps an existing production embedder with
// ingestion-operation observation.
func newIngestionObservedEmbedder(
	inner embedding.Embedder,
	operation types.IngestionOperation,
) *ingestionObservedEmbedder {
	return &ingestionObservedEmbedder{
		inner:     inner,
		operation: operation,
	}
}

// Embed records one single-text embedding request and delegates it to the
// wrapped production embedder.
func (e *ingestionObservedEmbedder) Embed(
	ctx context.Context,
	text string,
) ([]float32, error) {
	ctx = types.WithIngestionOperation(
		ctx,
		e.operation,
	)

	e.recordRequest([]string{text})

	return e.inner.Embed(ctx, text)
}

// BatchEmbed records one provider-adapter batch request and delegates it to the
// wrapped production embedder.
//
// BatchEmbedWithPool eventually calls this method once for every actual
// sub-batch, so this is where split requests such as 5 + 5 + 2 are counted.
func (e *ingestionObservedEmbedder) BatchEmbed(
	ctx context.Context,
	texts []string,
) ([][]float32, error) {
	ctx = types.WithIngestionOperation(
		ctx,
		e.operation,
	)

	e.recordRequest(texts)

	return e.inner.BatchEmbed(ctx, texts)
}

// BatchEmbedWithPool delegates batching to the wrapped embedder while forcing
// every generated sub-batch back through this wrapper's BatchEmbed method.
//
// The orchestration call itself is not counted as a provider request. Only the
// resulting BatchEmbed calls are counted.
func (e *ingestionObservedEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	ctx = types.WithIngestionOperation(
		ctx,
		e.operation,
	)

	return e.inner.BatchEmbedWithPool(
		ctx,
		e,
		texts,
	)
}

// GetModelName returns the wrapped model name.
func (e *ingestionObservedEmbedder) GetModelName() string {
	return e.inner.GetModelName()
}

// GetDimensions returns the wrapped model dimensions.
func (e *ingestionObservedEmbedder) GetDimensions() int {
	return e.inner.GetDimensions()
}

// GetModelID returns the wrapped model ID.
func (e *ingestionObservedEmbedder) GetModelID() string {
	return e.inner.GetModelID()
}

// Snapshot returns an immutable copy of the recorded counters.
func (e *ingestionObservedEmbedder) Snapshot() embeddingRequestSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	return embeddingRequestSnapshot{
		RequestCount: e.requestCount,
		BatchCount:   e.batchCount,
		TotalItems:   e.totalItems,
		InputChars:   e.inputChars,
	}
}

// recordRequest records one call that entered Embed or BatchEmbed.
//
// Text content is not retained. Only non-sensitive aggregate counts are kept.
func (e *ingestionObservedEmbedder) recordRequest(
	texts []string,
) {
	inputChars := 0
	for _, text := range texts {
		inputChars += len(text)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.requestCount++
	e.batchCount++
	e.totalItems += len(texts)
	e.inputChars += inputChars
}

// embeddingStageOutput converts an embedding request snapshot into the
// structured JSON output stored on the knowledge-processing embedding span.
//
// Production code and SQLite-backed tests share this function so the tests
// verify the exact field mapping used by the ingestion pipeline.
func embeddingStageOutput(
	operation types.IngestionOperation,
	model embedding.Embedder,
	observation embeddingRequestSnapshot,
	vectorsWritten int,
	storageBytes int64,
	success bool,
) types.JSONMap {
	return embeddingOperationOutput(
		operation,
		types.StageEmbedding,
		model,
		observation,
		vectorsWritten,
		storageBytes,
		success,
	)
}

// embeddingOperationOutput builds the shared structured output used by both
// the main embedding stage and embedding work nested under postprocess spans.
func embeddingOperationOutput(
	operation types.IngestionOperation,
	stage string,
	model embedding.Embedder,
	observation embeddingRequestSnapshot,
	vectorsWritten int,
	storageBytes int64,
	success bool,
) types.JSONMap {
	computedItems := 0
	if success {
		computedItems = observation.TotalItems
	}

	output := types.IngestionOperationObservation{
		Operation: operation,
		Stage:     stage,

		ModelID:   model.GetModelID(),
		ModelType: "embedding",

		OperationCount: 1,
		RequestCount:   observation.RequestCount,
		BatchCount:     observation.BatchCount,

		TotalItems:    observation.TotalItems,
		ComputedItems: computedItems,
		ReusedItems:   0,

		InputChars: observation.InputChars,

		CacheStatus: types.IngestionCacheStatusNotSupported,
		Success:     success,
	}.ToJSONMap()

	output["vectors_written"] = vectorsWritten
	output["storage_bytes"] = storageBytes
	output["dimensions"] = model.GetDimensions()

	return output
}

// embeddingBatchIndexer is the narrow part of CompositeRetrieveEngine needed
// by observed postprocess embedding operations.
type embeddingBatchIndexer interface {
	BatchIndex(
		ctx context.Context,
		embedder embedding.Embedder,
		indexInfoList []*types.IndexInfo,
	) error
}

// executeObservedEmbeddingBatch runs one batch through the production
// observation wrapper and returns counters gathered around real model calls.
func executeObservedEmbeddingBatch(
	ctx context.Context,
	operation types.IngestionOperation,
	model embedding.Embedder,
	indexer embeddingBatchIndexer,
	indexInfoList []*types.IndexInfo,
) (embeddingRequestSnapshot, error) {
	observedModel := newIngestionObservedEmbedder(model, operation)
	operationCtx := types.WithIngestionOperation(ctx, operation)
	err := indexer.BatchIndex(operationCtx, observedModel, indexInfoList)
	return observedModel.Snapshot(), err
}

// observeUnspannedEmbeddingBatch records embedding operations that do not yet
// participate in the knowledge-processing attempt tree. FAQ CRUD/import paths
// currently fall into this category, so PR1 emits the same structured fields
// to logs without inventing a parallel span lifecycle.
func observeUnspannedEmbeddingBatch(
	ctx context.Context,
	operation types.IngestionOperation,
	model embedding.Embedder,
	indexer embeddingBatchIndexer,
	indexInfoList []*types.IndexInfo,
	errorCode string,
) (types.JSONMap, error) {
	observation, err := executeObservedEmbeddingBatch(
		ctx,
		operation,
		model,
		indexer,
		indexInfoList,
	)
	output := embeddingOperationOutput(
		operation,
		types.StageEmbedding,
		model,
		observation,
		len(indexInfoList),
		0,
		err == nil,
	)
	output["observation_sink"] = "structured_log"
	if err != nil {
		output["vectors_written"] = 0
		output["error_code"] = errorCode
	}

	logger.GetLogger(ctx).
		WithFields(logger.Fields(output)).
		Info("ingestion embedding observation")

	return output, err
}

// observeUnspannedDirectEmbeddingBatch observes callers that use BatchEmbed
// directly rather than going through a retrieve engine. Wiki taxonomy
// similarity is the current ingestion use case.
func observeUnspannedDirectEmbeddingBatch(
	ctx context.Context,
	operation types.IngestionOperation,
	model embedding.Embedder,
	texts []string,
	errorCode string,
	metadata types.JSONMap,
) ([][]float32, types.JSONMap, error) {
	observedModel := newIngestionObservedEmbedder(model, operation)
	operationCtx := types.WithIngestionOperation(ctx, operation)
	vectors, err := observedModel.BatchEmbed(operationCtx, texts)
	observation := observedModel.Snapshot()

	vectorsWritten := len(vectors)
	if err != nil {
		vectorsWritten = 0
	}
	output := embeddingOperationOutput(
		operation,
		types.StagePostProcess,
		model,
		observation,
		vectorsWritten,
		0,
		err == nil,
	)
	output["observation_sink"] = "structured_log"
	for key, value := range metadata {
		if _, exists := output[key]; !exists {
			output[key] = value
		}
	}
	if err != nil {
		output["error_code"] = errorCode
	}

	logger.GetLogger(ctx).
		WithFields(logger.Fields(output)).
		Info("ingestion embedding observation")

	return vectors, output, err
}

// observePostprocessEmbeddingBatch executes one postprocess embedding
// operation and records it as a child span beneath the owning postprocess
// operation. The model call and indexing behavior are unchanged.
func observePostprocessEmbeddingBatch(
	ctx context.Context,
	tracker SpanTracker,
	parent *Span,
	spanName string,
	operation types.IngestionOperation,
	model embedding.Embedder,
	indexer embeddingBatchIndexer,
	indexInfoList []*types.IndexInfo,
	errorCode string,
) error {
	input := types.JSONMap{
		"operation":     string(operation),
		"items_planned": len(indexInfoList),
		"model_id":      model.GetModelID(),
		"dimensions":    model.GetDimensions(),
		"cache_status": string(
			types.IngestionCacheStatusNotSupported,
		),
	}
	embeddingSpan := tracker.BeginSubSpan(
		ctx,
		parent,
		spanName,
		types.SpanKindGeneration,
		input,
	)

	observation, err := executeObservedEmbeddingBatch(
		ctx,
		operation,
		model,
		indexer,
		indexInfoList,
	)

	output := embeddingOperationOutput(
		operation,
		types.StagePostProcess,
		model,
		observation,
		len(indexInfoList),
		0,
		err == nil,
	)
	if err != nil {
		output["vectors_written"] = 0
		output["error_code"] = errorCode
		tracker.FailSpanWithOutput(
			ctx,
			embeddingSpan,
			output,
			errorCode,
			"postprocess embedding batch failed",
			err,
		)
		return err
	}

	tracker.EndSpan(
		ctx,
		embeddingSpan,
		output,
	)
	return nil
}
