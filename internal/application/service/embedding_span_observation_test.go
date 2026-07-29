package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingObservation_SpanMatchesCountingEmbedder(
	t *testing.T,
) {
	const (
		knowledgeID    = "knowledge-embedding-test"
		vectorsWritten = 12
		storageBytes   = int64(192)
	)

	ctx := context.Background()

	tracker, db := setupSpanTrackerTest(t)

	_, attempt, err := tracker.OpenAttempt(
		ctx,
		knowledgeID,
		"",
	)
	require.NoError(t, err)
	require.Positive(t, attempt)

	embeddingSpan := tracker.BeginStage(
		ctx,
		knowledgeID,
		attempt,
		types.StageEmbedding,
		types.JSONMap{
			"operation": string(
				types.IngestionOperationEmbeddingChunk,
			),
			"chunks_to_embed": vectorsWritten,
			"model_id":        "embedding-test",
			"cache_status": string(
				types.IngestionCacheStatusNotSupported,
			),
		},
	)
	require.NotNil(t, embeddingSpan)

	t.Setenv("BATCH_EMBED_SIZE", "5")

	pool, err := ants.NewPool(4)
	require.NoError(t, err)
	t.Cleanup(pool.Release)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "embedding-test",
			ModelName:  "test-embedding-model",
			Dimensions: 4,
			Pooler: embedding.NewBatchEmbedder(
				pool,
			),
		},
	)

	observedEmbedder := newIngestionObservedEmbedder(
		countingEmbedder,
		types.IngestionOperationEmbeddingChunk,
	)

	texts := make([]string, 0, vectorsWritten)
	expectedInputChars := 0

	for i := 0; i < vectorsWritten; i++ {
		text := fmt.Sprintf(
			"stable embedding input %02d",
			i,
		)

		texts = append(texts, text)
		expectedInputChars += len(text)
	}

	vectors, err := observedEmbedder.BatchEmbedWithPool(
		ctx,
		observedEmbedder,
		texts,
	)
	require.NoError(t, err)
	require.Len(t, vectors, vectorsWritten)

	observation := observedEmbedder.Snapshot()
	modelSnapshot := countingEmbedder.Snapshot()

	// First verify that the production observation wrapper saw the same actual
	// model-interface calls as CountingEmbedder.
	require.Equal(
		t,
		modelSnapshot.RequestCount,
		observation.RequestCount,
	)
	require.Equal(
		t,
		modelSnapshot.TotalInputItems,
		observation.TotalItems,
	)
	require.Equal(
		t,
		modelSnapshot.TotalInputChars,
		observation.InputChars,
	)

	require.Equal(t, 3, observation.RequestCount)
	require.Equal(t, 3, observation.BatchCount)
	require.Equal(t, 12, observation.TotalItems)
	require.Equal(
		t,
		expectedInputChars,
		observation.InputChars,
	)

	require.ElementsMatch(
		t,
		[]int{5, 5, 2},
		modelSnapshot.BatchSizes,
	)

	for _, call := range modelSnapshot.Calls {
		require.Equal(
			t,
			types.IngestionOperationEmbeddingChunk,
			call.Operation,
		)
	}

	output := embeddingStageOutput(
		types.IngestionOperationEmbeddingChunk,
		countingEmbedder,
		observation,
		vectorsWritten,
		storageBytes,
		true,
	)

	tracker.EndSpan(
		ctx,
		embeddingSpan,
		output,
	)

	var storedSpan types.KnowledgeProcessingSpan
	require.NoError(
		t,
		db.Where(
			"knowledge_id = ? AND attempt = ? AND name = ?",
			knowledgeID,
			attempt,
			types.StageEmbedding,
		).Take(&storedSpan).Error,
	)

	require.Equal(
		t,
		types.SpanStatusDone,
		storedSpan.Status,
	)
	require.NotNil(t, storedSpan.Output)

	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingChunk),
		storedSpan.Output["operation"],
	)
	require.Equal(
		t,
		types.StageEmbedding,
		storedSpan.Output["stage"],
	)
	require.Equal(
		t,
		"embedding-test",
		storedSpan.Output["model_id"],
	)
	require.Equal(
		t,
		"embedding",
		storedSpan.Output["model_type"],
	)
	require.Equal(
		t,
		string(types.IngestionCacheStatusNotSupported),
		storedSpan.Output["cache_status"],
	)
	require.Equal(
		t,
		true,
		storedSpan.Output["success"],
	)

	require.EqualValues(
		t,
		1,
		storedSpan.Output["operation_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.RequestCount,
		storedSpan.Output["request_count"],
	)
	require.EqualValues(
		t,
		observation.BatchCount,
		storedSpan.Output["batch_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		storedSpan.Output["total_items"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		storedSpan.Output["computed_items"],
	)
	require.EqualValues(
		t,
		0,
		storedSpan.Output["reused_items"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputChars,
		storedSpan.Output["input_chars"],
	)
	require.EqualValues(
		t,
		vectorsWritten,
		storedSpan.Output["vectors_written"],
	)
	require.EqualValues(
		t,
		storageBytes,
		storedSpan.Output["storage_bytes"],
	)
	require.EqualValues(
		t,
		countingEmbedder.GetDimensions(),
		storedSpan.Output["dimensions"],
	)
}

func TestEmbeddingObservation_FailedSpanPreservesRequestCounts(
	t *testing.T,
) {
	const knowledgeID = "knowledge-embedding-failure-test"

	ctx := context.Background()

	tracker, db := setupSpanTrackerTest(t)

	_, attempt, err := tracker.OpenAttempt(
		ctx,
		knowledgeID,
		"",
	)
	require.NoError(t, err)
	require.Positive(t, attempt)

	embeddingSpan := tracker.BeginStage(
		ctx,
		knowledgeID,
		attempt,
		types.StageEmbedding,
		types.JSONMap{
			"operation": string(
				types.IngestionOperationEmbeddingChunk,
			),
			"chunks_to_embed": 2,
			"model_id":        "embedding-test",
			"cache_status": string(
				types.IngestionCacheStatusNotSupported,
			),
		},
	)
	require.NotNil(t, embeddingSpan)

	expectedError := errors.New(
		"embedding provider failed",
	)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:      "embedding-test",
			ModelName:    "test-embedding-model",
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	observedEmbedder := newIngestionObservedEmbedder(
		countingEmbedder,
		types.IngestionOperationEmbeddingChunk,
	)

	vectors, err := observedEmbedder.BatchEmbed(
		ctx,
		[]string{
			"first failed input",
			"second failed input",
		},
	)
	require.Nil(t, vectors)
	require.ErrorIs(t, err, expectedError)

	observation := observedEmbedder.Snapshot()
	modelSnapshot := countingEmbedder.Snapshot()

	require.Equal(t, 1, observation.RequestCount)
	require.Equal(t, 1, observation.BatchCount)
	require.Equal(t, 2, observation.TotalItems)
	require.Equal(
		t,
		len("first failed input")+
			len("second failed input"),
		observation.InputChars,
	)

	require.Equal(
		t,
		modelSnapshot.RequestCount,
		observation.RequestCount,
	)
	require.Equal(
		t,
		modelSnapshot.TotalInputItems,
		observation.TotalItems,
	)
	require.Equal(
		t,
		modelSnapshot.TotalInputChars,
		observation.InputChars,
	)

	const errorCode = "EMBEDDING_PROVIDER_FAILED"

	failureOutput := embeddingStageOutput(
		types.IngestionOperationEmbeddingChunk,
		countingEmbedder,
		observation,
		0,
		0,
		false,
	)
	failureOutput["error_code"] = errorCode

	tracker.FailSpanWithOutput(
		ctx,
		embeddingSpan,
		failureOutput,
		errorCode,
		"batch index failed",
		expectedError,
	)

	var storedSpan types.KnowledgeProcessingSpan
	require.NoError(
		t,
		db.Where(
			"knowledge_id = ? AND attempt = ? AND name = ?",
			knowledgeID,
			attempt,
			types.StageEmbedding,
		).Take(&storedSpan).Error,
	)

	require.Equal(
		t,
		types.SpanStatusFailed,
		storedSpan.Status,
	)
	require.Equal(
		t,
		errorCode,
		storedSpan.ErrorCode,
	)
	require.Equal(
		t,
		"batch index failed",
		storedSpan.ErrorMessage,
	)
	require.Contains(
		t,
		storedSpan.ErrorDetail,
		"embedding provider failed",
	)
	require.NotNil(t, storedSpan.Output)

	require.Equal(
		t,
		string(types.IngestionOperationEmbeddingChunk),
		storedSpan.Output["operation"],
	)
	require.Equal(
		t,
		types.StageEmbedding,
		storedSpan.Output["stage"],
	)
	require.Equal(
		t,
		string(types.IngestionCacheStatusNotSupported),
		storedSpan.Output["cache_status"],
	)
	require.Equal(
		t,
		false,
		storedSpan.Output["success"],
	)
	require.Equal(
		t,
		errorCode,
		storedSpan.Output["error_code"],
	)

	require.EqualValues(
		t,
		1,
		storedSpan.Output["operation_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.RequestCount,
		storedSpan.Output["request_count"],
	)
	require.EqualValues(
		t,
		observation.BatchCount,
		storedSpan.Output["batch_count"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputItems,
		storedSpan.Output["total_items"],
	)
	require.EqualValues(
		t,
		0,
		storedSpan.Output["computed_items"],
	)
	require.EqualValues(
		t,
		0,
		storedSpan.Output["reused_items"],
	)
	require.EqualValues(
		t,
		modelSnapshot.TotalInputChars,
		storedSpan.Output["input_chars"],
	)
	require.EqualValues(
		t,
		0,
		storedSpan.Output["vectors_written"],
	)
	require.EqualValues(
		t,
		0,
		storedSpan.Output["storage_bytes"],
	)
	require.EqualValues(
		t,
		countingEmbedder.GetDimensions(),
		storedSpan.Output["dimensions"],
	)
}
