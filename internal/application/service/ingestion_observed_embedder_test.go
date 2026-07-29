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

func TestIngestionObservedEmbedderRecordsSplitBatchRequests(
	t *testing.T,
) {
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

	texts := make([]string, 0, 12)
	expectedInputChars := 0

	for i := 0; i < 12; i++ {
		text := fmt.Sprintf(
			"stable embedding input %02d",
			i,
		)
		texts = append(texts, text)
		expectedInputChars += len(text)
	}

	vectors, err := observedEmbedder.BatchEmbedWithPool(
		context.Background(),
		observedEmbedder,
		texts,
	)
	require.NoError(t, err)
	require.Len(t, vectors, 12)

	observation := observedEmbedder.Snapshot()

	require.Equal(t, 3, observation.RequestCount)
	require.Equal(t, 3, observation.BatchCount)
	require.Equal(t, 12, observation.TotalItems)
	require.Equal(
		t,
		expectedInputChars,
		observation.InputChars,
	)

	// The production observation wrapper and the test counting model must
	// observe the same actual BatchEmbed calls.
	modelSnapshot := countingEmbedder.Snapshot()

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
}

func TestIngestionObservedEmbedderCountsFailedRequest(
	t *testing.T,
) {
	expectedError := errors.New(
		"embedding provider failed",
	)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	observedEmbedder := newIngestionObservedEmbedder(
		countingEmbedder,
		types.IngestionOperationEmbeddingChunk,
	)

	vectors, err := observedEmbedder.BatchEmbed(
		context.Background(),
		[]string{
			"first",
			"second",
		},
	)

	require.Nil(t, vectors)
	require.ErrorIs(t, err, expectedError)

	observation := observedEmbedder.Snapshot()

	// A request that reached the model interface is still counted even when
	// the provider returns an error.
	require.Equal(t, 1, observation.RequestCount)
	require.Equal(t, 1, observation.BatchCount)
	require.Equal(t, 2, observation.TotalItems)
	require.Equal(
		t,
		len("first")+len("second"),
		observation.InputChars,
	)

	modelSnapshot := countingEmbedder.Snapshot()

	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Len(t, modelSnapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingChunk,
		modelSnapshot.Calls[0].Operation,
	)
}

func TestIngestionObservedEmbedderDelegatesModelMetadata(
	t *testing.T,
) {
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "embedding-model-id",
			ModelName:  "embedding-model-name",
			Dimensions: 8,
		},
	)

	observedEmbedder := newIngestionObservedEmbedder(
		countingEmbedder,
		types.IngestionOperationEmbeddingChunk,
	)

	require.Equal(
		t,
		"embedding-model-id",
		observedEmbedder.GetModelID(),
	)
	require.Equal(
		t,
		"embedding-model-name",
		observedEmbedder.GetModelName(),
	)
	require.Equal(
		t,
		8,
		observedEmbedder.GetDimensions(),
	)
}

func TestIngestionObservedEmbedderRecordsSingleEmbedRequest(
	t *testing.T,
) {
	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			Dimensions: 4,
		},
	)

	observedEmbedder := newIngestionObservedEmbedder(
		countingEmbedder,
		types.IngestionOperationEmbeddingSummary,
	)

	vector, err := observedEmbedder.Embed(
		context.Background(),
		"summary content",
	)
	require.NoError(t, err)
	require.Len(t, vector, 4)

	observation := observedEmbedder.Snapshot()

	require.Equal(t, 1, observation.RequestCount)
	require.Equal(t, 1, observation.BatchCount)
	require.Equal(t, 1, observation.TotalItems)
	require.Equal(
		t,
		len("summary content"),
		observation.InputChars,
	)

	modelSnapshot := countingEmbedder.Snapshot()

	require.Equal(t, 1, modelSnapshot.RequestCount)
	require.Len(t, modelSnapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingSummary,
		modelSnapshot.Calls[0].Operation,
	)
}
