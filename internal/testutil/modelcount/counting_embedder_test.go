package modelcount

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCountingEmbedderRecordsEmbedAndBatchEmbedRequests(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			ModelID:    "embedding-test",
			ModelName:  "test-embedding-model",
			Dimensions: 4,
		},
	)

	singleVector, err := model.Embed(
		context.Background(),
		"hello",
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]float32{1, 2, 3, 4},
		singleVector,
	)

	batchVectors, err := model.BatchEmbed(
		context.Background(),
		[]string{
			"first",
			"second",
		},
	)
	require.NoError(t, err)
	require.Len(t, batchVectors, 2)
	require.Equal(
		t,
		[]float32{1, 2, 3, 4},
		batchVectors[0],
	)
	require.Equal(
		t,
		[]float32{2, 3, 4, 5},
		batchVectors[1],
	)

	snapshot := model.Snapshot()

	require.Equal(t, 2, snapshot.RequestCount)
	require.Equal(
		t,
		[]int{1, 2},
		snapshot.BatchSizes,
	)
	require.Equal(t, 3, snapshot.TotalInputItems)
	require.Equal(
		t,
		len("hello")+len("first")+len("second"),
		snapshot.TotalInputChars,
	)
	require.Equal(t, 4, snapshot.Dimensions)
	require.Len(t, snapshot.Calls, 2)

	require.Equal(t, 1, snapshot.Calls[0].BatchSize)
	require.Equal(
		t,
		len("hello"),
		snapshot.Calls[0].InputChars,
	)

	require.Equal(t, 2, snapshot.Calls[1].BatchSize)
	require.Equal(
		t,
		len("first")+len("second"),
		snapshot.Calls[1].InputChars,
	)

	require.Equal(
		t,
		"embedding-test",
		model.GetModelID(),
	)
	require.Equal(
		t,
		"test-embedding-model",
		model.GetModelName(),
	)
	require.Equal(t, 4, model.GetDimensions())
}

func TestCountingEmbedderRecordsIngestionOperation(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions: 4,
		},
	)

	ctx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationEmbeddingChunk,
	)

	vectors, err := model.BatchEmbed(
		ctx,
		[]string{
			"first chunk",
			"second chunk",
		},
	)
	require.NoError(t, err)
	require.Len(t, vectors, 2)

	snapshot := model.Snapshot()

	require.Equal(t, 1, snapshot.RequestCount)
	require.Len(t, snapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperationEmbeddingChunk,
		snapshot.Calls[0].Operation,
	)
	require.Equal(
		t,
		2,
		snapshot.Calls[0].BatchSize,
	)
	require.Equal(
		t,
		len("first chunk")+len("second chunk"),
		snapshot.Calls[0].InputChars,
	)
}

func TestCountingEmbedderDoesNotClassifyMissingOperation(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions: 4,
		},
	)

	_, err := model.BatchEmbed(
		context.Background(),
		[]string{"unclassified input"},
	)
	require.NoError(t, err)

	snapshot := model.Snapshot()

	require.Equal(t, 1, snapshot.RequestCount)
	require.Len(t, snapshot.Calls, 1)
	require.Equal(
		t,
		types.IngestionOperation(""),
		snapshot.Calls[0].Operation,
	)
}

func TestCountingEmbedderUsesDefaultConfiguration(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{},
	)

	require.Equal(
		t,
		"counting-embedder",
		model.GetModelID(),
	)
	require.Equal(
		t,
		"counting-embedder",
		model.GetModelName(),
	)
	require.Equal(t, 3, model.GetDimensions())

	vectors, err := model.BatchEmbed(
		context.Background(),
		[]string{"text"},
	)
	require.NoError(t, err)
	require.Len(t, vectors, 1)
	require.Len(t, vectors[0], 3)

	snapshot := model.Snapshot()

	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, []int{1}, snapshot.BatchSizes)
	require.Equal(t, 1, snapshot.TotalInputItems)
	require.Equal(t, len("text"), snapshot.TotalInputChars)
	require.Equal(t, 3, snapshot.Dimensions)
}

func TestCountingEmbedderRecordsRequestThatReturnsError(
	t *testing.T,
) {
	expectedError := errors.New(
		"embedding provider failed",
	)

	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions:   4,
			DefaultError: expectedError,
		},
	)

	vectors, err := model.BatchEmbed(
		context.Background(),
		[]string{
			"first",
			"second",
		},
	)

	require.Nil(t, vectors)
	require.ErrorIs(t, err, expectedError)

	snapshot := model.Snapshot()

	// A failed provider call is still a real model request.
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, []int{2}, snapshot.BatchSizes)
	require.Equal(t, 2, snapshot.TotalInputItems)
	require.Equal(
		t,
		len("first")+len("second"),
		snapshot.TotalInputChars,
	)
	require.Len(t, snapshot.Calls, 1)
	require.Equal(t, 2, snapshot.Calls[0].BatchSize)
}

func TestCountingEmbedderFailsConfiguredRequest(
	t *testing.T,
) {
	expectedError := errors.New(
		"second embedding request failed",
	)

	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions:    2,
			FailOnRequest: 2,
			FailError:     expectedError,
		},
	)

	firstVectors, err := model.BatchEmbed(
		context.Background(),
		[]string{"first"},
	)
	require.NoError(t, err)
	require.Len(t, firstVectors, 1)

	secondVectors, err := model.BatchEmbed(
		context.Background(),
		[]string{
			"second",
			"third",
		},
	)
	require.Nil(t, secondVectors)
	require.ErrorIs(t, err, expectedError)

	thirdVectors, err := model.BatchEmbed(
		context.Background(),
		[]string{"fourth"},
	)
	require.NoError(t, err)
	require.Len(t, thirdVectors, 1)

	snapshot := model.Snapshot()

	require.Equal(t, 3, snapshot.RequestCount)
	require.Equal(
		t,
		[]int{1, 2, 1},
		snapshot.BatchSizes,
	)
	require.Equal(t, 4, snapshot.TotalInputItems)
	require.Len(t, snapshot.Calls, 3)
}

func TestCountingEmbedderBatchEmbedWithPoolFallsBackToOneRequest(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions: 3,
		},
	)

	vectors, err := model.BatchEmbedWithPool(
		context.Background(),
		model,
		[]string{
			"first",
			"second",
			"third",
		},
	)
	require.NoError(t, err)
	require.Len(t, vectors, 3)

	snapshot := model.Snapshot()

	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(
		t,
		[]int{3},
		snapshot.BatchSizes,
	)
	require.Equal(t, 3, snapshot.TotalInputItems)
}

func TestCountingEmbedderBatchEmbedWithPoolUsesSelfWhenModelIsNil(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions: 3,
		},
	)

	vectors, err := model.BatchEmbedWithPool(
		context.Background(),
		nil,
		[]string{
			"first",
			"second",
		},
	)
	require.NoError(t, err)
	require.Len(t, vectors, 2)

	snapshot := model.Snapshot()

	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(
		t,
		[]int{2},
		snapshot.BatchSizes,
	)
	require.Equal(t, 2, snapshot.TotalInputItems)
}

func TestCountingEmbedderSnapshotIsImmutable(
	t *testing.T,
) {
	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions: 3,
		},
	)

	_, err := model.BatchEmbed(
		context.Background(),
		[]string{
			"first",
			"second",
		},
	)
	require.NoError(t, err)

	firstSnapshot := model.Snapshot()
	require.Len(t, firstSnapshot.BatchSizes, 1)
	require.Len(t, firstSnapshot.Calls, 1)

	// Mutating one snapshot must not modify the model's internal state.
	firstSnapshot.BatchSizes[0] = 999
	firstSnapshot.Calls[0].BatchSize = 999
	firstSnapshot.Calls[0].InputChars = 999

	secondSnapshot := model.Snapshot()

	require.Equal(
		t,
		[]int{2},
		secondSnapshot.BatchSizes,
	)
	require.Equal(
		t,
		2,
		secondSnapshot.Calls[0].BatchSize,
	)
	require.Equal(
		t,
		len("first")+len("second"),
		secondSnapshot.Calls[0].InputChars,
	)
}

func TestCountingEmbedderConcurrentRequests(
	t *testing.T,
) {
	const goroutineCount = 32

	model := NewCountingEmbedder(
		CountingEmbedderOptions{
			Dimensions: 4,
		},
	)

	var waitGroup sync.WaitGroup
	errorChannel := make(
		chan error,
		goroutineCount,
	)

	for i := 0; i < goroutineCount; i++ {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			vectors, err := model.BatchEmbed(
				context.Background(),
				[]string{
					"left",
					"right",
				},
			)
			if err != nil {
				errorChannel <- err
				return
			}

			if len(vectors) != 2 {
				errorChannel <- errors.New(
					"unexpected vector count",
				)
				return
			}

			for _, vector := range vectors {
				if len(vector) != 4 {
					errorChannel <- errors.New(
						"unexpected vector dimensions",
					)
					return
				}
			}
		}()
	}

	waitGroup.Wait()
	close(errorChannel)

	// Assertions are deliberately performed in the main test goroutine.
	for err := range errorChannel {
		require.NoError(t, err)
	}

	snapshot := model.Snapshot()

	require.Equal(
		t,
		goroutineCount,
		snapshot.RequestCount,
	)
	require.Len(
		t,
		snapshot.BatchSizes,
		goroutineCount,
	)
	require.Len(
		t,
		snapshot.Calls,
		goroutineCount,
	)
	require.Equal(
		t,
		goroutineCount*2,
		snapshot.TotalInputItems,
	)
	require.Equal(
		t,
		goroutineCount*(len("left")+len("right")),
		snapshot.TotalInputChars,
	)
	require.Equal(t, 4, snapshot.Dimensions)

	for _, batchSize := range snapshot.BatchSizes {
		require.Equal(t, 2, batchSize)
	}
}
