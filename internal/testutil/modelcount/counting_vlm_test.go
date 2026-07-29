package modelcount

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCountingVLMRecordsOCRAndCaptionRequests(t *testing.T) {
	model := NewCountingVLM(CountingVLMOptions{
		OCRResponse:     "recognized text",
		CaptionResponse: "an image caption",
	})

	image := []byte("test-image")

	ocrCtx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationMultimodalOCR,
	)
	ocrResponse, err := model.Predict(
		ocrCtx,
		[][]byte{image},
		"test OCR prompt",
	)
	require.NoError(t, err)
	require.Equal(t, "recognized text", ocrResponse)

	captionCtx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationMultimodalCaption,
	)
	captionResponse, err := model.Predict(
		captionCtx,
		[][]byte{image},
		"test caption prompt",
	)
	require.NoError(t, err)
	require.Equal(t, "an image caption", captionResponse)

	snapshot := model.Snapshot()

	require.Equal(t, 2, snapshot.PredictRequestCount)
	require.Equal(t, 1, snapshot.OCRRequestCount)
	require.Equal(t, 1, snapshot.CaptionRequestCount)
	require.Equal(t, 2, snapshot.InputImageCount)
	require.Equal(t, int64(len(image)*2), snapshot.InputBytes)
	require.Len(t, snapshot.Calls, 2)

	require.Equal(
		t,
		types.IngestionOperationMultimodalOCR,
		snapshot.Calls[0].Operation,
	)
	require.Equal(
		t,
		types.IngestionOperationMultimodalCaption,
		snapshot.Calls[1].Operation,
	)
}

func TestCountingVLMRecordsRequestThatReturnsError(t *testing.T) {
	expectedError := errors.New("OCR provider failed")

	model := NewCountingVLM(CountingVLMOptions{
		OCRError: expectedError,
	})

	ctx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationMultimodalOCR,
	)

	_, err := model.Predict(
		ctx,
		[][]byte{[]byte("image")},
		"test OCR prompt",
	)
	require.ErrorIs(t, err, expectedError)

	snapshot := model.Snapshot()

	// A failed provider call is still a real model request.
	require.Equal(t, 1, snapshot.PredictRequestCount)
	require.Equal(t, 1, snapshot.OCRRequestCount)
	require.Equal(t, 0, snapshot.CaptionRequestCount)
	require.Equal(t, 1, snapshot.InputImageCount)
}

func TestCountingVLMDoesNotClassifyMissingOperation(t *testing.T) {
	model := NewCountingVLM(CountingVLMOptions{
		DefaultResponse: "default response",
	})

	response, err := model.Predict(
		context.Background(),
		[][]byte{[]byte("image")},
		"unclassified prompt",
	)
	require.NoError(t, err)
	require.Equal(t, "default response", response)

	snapshot := model.Snapshot()

	require.Equal(t, 1, snapshot.PredictRequestCount)
	require.Equal(t, 0, snapshot.OCRRequestCount)
	require.Equal(t, 0, snapshot.CaptionRequestCount)
	require.Equal(t, 1, snapshot.InputImageCount)
	require.Equal(
		t,
		types.IngestionOperation(""),
		snapshot.Calls[0].Operation,
	)
}

func TestCountingVLMCanFailOnConfiguredRequest(t *testing.T) {
	expectedError := errors.New("second request failed")

	model := NewCountingVLM(CountingVLMOptions{
		OCRResponse:   "recognized text",
		FailOnRequest: 2,
		FailError:     expectedError,
	})

	ctx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationMultimodalOCR,
	)

	_, err := model.Predict(
		ctx,
		[][]byte{[]byte("first image")},
		"test OCR prompt",
	)
	require.NoError(t, err)

	_, err = model.Predict(
		ctx,
		[][]byte{[]byte("second image")},
		"test OCR prompt",
	)
	require.ErrorIs(t, err, expectedError)

	snapshot := model.Snapshot()

	// Both calls reached Predict, even though the second one failed.
	require.Equal(t, 2, snapshot.PredictRequestCount)
	require.Equal(t, 2, snapshot.OCRRequestCount)
	require.Equal(t, 2, snapshot.InputImageCount)
}

func TestCountingVLMSnapshotIsSafeDuringConcurrentCalls(t *testing.T) {
	const requestCount = 100

	model := NewCountingVLM(CountingVLMOptions{
		OCRResponse: "recognized text",
	})

	ctx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationMultimodalOCR,
	)

	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)

	for range requestCount {
		go func() {
			defer waitGroup.Done()

			_, err := model.Predict(
				ctx,
				[][]byte{[]byte("image")},
				"test OCR prompt",
			)
			require.NoError(t, err)
		}()
	}

	waitGroup.Wait()

	snapshot := model.Snapshot()

	require.Equal(t, requestCount, snapshot.PredictRequestCount)
	require.Equal(t, requestCount, snapshot.OCRRequestCount)
	require.Equal(t, 0, snapshot.CaptionRequestCount)
	require.Equal(t, requestCount, snapshot.InputImageCount)
	require.Len(t, snapshot.Calls, requestCount)
}
