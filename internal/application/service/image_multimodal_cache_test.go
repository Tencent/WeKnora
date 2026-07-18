package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingVLM struct {
	calls     int
	modelID   string
	modelName string
	result    string
	err       error
}

func (v *countingVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	v.calls++
	if v.err != nil {
		return "", v.err
	}
	return v.result, nil
}

func (v *countingVLM) GetModelName() string { return v.modelName }
func (v *countingVLM) GetModelID() string   { return v.modelID }

func TestCachedVLMPredictReusesSuccessfulResult(t *testing.T) {
	resetProcessVLMCacheForTest()

	model := &countingVLM{modelID: "vlm-1", modelName: "vision", result: "ocr text"}
	imageBytes := []byte("image-bytes")
	imageHash := stableBytesHash(imageBytes)

	first, err := cachedVLMPredict(context.Background(), 7, model, imageBytes, imageHash, "prompt", "ocr", "v1")
	require.NoError(t, err)
	require.Equal(t, "ocr text", first)

	second, err := cachedVLMPredict(context.Background(), 7, model, imageBytes, imageHash, "prompt", "ocr", "v1")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, model.calls)
}

func TestCachedVLMPredictMissesOnPromptKindVersionModelOrImage(t *testing.T) {
	resetProcessVLMCacheForTest()

	model := &countingVLM{modelID: "vlm-1", modelName: "vision", result: "ok"}
	imageBytes := []byte("image-bytes")
	imageHash := stableBytesHash(imageBytes)

	_, err := cachedVLMPredict(context.Background(), 7, model, imageBytes, imageHash, "prompt", "ocr", "v1")
	require.NoError(t, err)
	_, err = cachedVLMPredict(context.Background(), 7, model, imageBytes, imageHash, "prompt", "caption", "v1")
	require.NoError(t, err)
	_, err = cachedVLMPredict(context.Background(), 7, model, imageBytes, imageHash, "prompt", "ocr", "v2")
	require.NoError(t, err)

	otherModel := &countingVLM{modelID: "vlm-2", modelName: "vision", result: "ok"}
	_, err = cachedVLMPredict(context.Background(), 7, otherModel, imageBytes, imageHash, "prompt", "ocr", "v1")
	require.NoError(t, err)

	otherImageBytes := []byte("other-image")
	_, err = cachedVLMPredict(context.Background(), 7, model, otherImageBytes, stableBytesHash(otherImageBytes), "prompt", "ocr", "v1")
	require.NoError(t, err)

	require.Equal(t, 4, model.calls)
	require.Equal(t, 1, otherModel.calls)
}

func TestCachedVLMPredictDoesNotCacheFailedResult(t *testing.T) {
	resetProcessVLMCacheForTest()

	errBoom := errors.New("boom")
	failing := &countingVLM{modelID: "vlm-1", modelName: "vision", err: errBoom}
	imageBytes := []byte("image-bytes")
	imageHash := stableBytesHash(imageBytes)

	_, err := cachedVLMPredict(context.Background(), 7, failing, imageBytes, imageHash, "prompt", "ocr", "v1")
	require.ErrorIs(t, err, errBoom)

	retry := &countingVLM{modelID: "vlm-1", modelName: "vision", result: "ocr text"}
	got, err := cachedVLMPredict(context.Background(), 7, retry, imageBytes, imageHash, "prompt", "ocr", "v1")
	require.NoError(t, err)
	require.Equal(t, "ocr text", got)
	require.Equal(t, 1, retry.calls)
}

func resetProcessVLMCacheForTest() {
	processVLMCache.Lock()
	defer processVLMCache.Unlock()
	processVLMCache.data = make(map[vlmCacheKey]string)
}
