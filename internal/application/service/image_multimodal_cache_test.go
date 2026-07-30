package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/stretchr/testify/require"
)

type countingVLM struct {
	calls atomic.Int32
}

func (v *countingVLM) Predict(_ context.Context, _ [][]byte, prompt string) (string, error) {
	v.calls.Add(1)
	return "result:" + prompt, nil
}

func (*countingVLM) GetModelName() string { return "fake-vlm" }
func (*countingVLM) GetModelID() string   { return "fake-vlm-id" }

func TestImageMultimodalPredictVLMReusesExactRequest(t *testing.T) {
	service := &ImageMultimodalService{inferenceCache: inferencecache.New(nil)}
	model := &countingVLM{}
	ctx := context.Background()
	image := [][]byte{[]byte("same-image")}

	first, hit, err := service.predictVLM(ctx, 7, "ocr", model, image, "same-prompt")
	require.NoError(t, err)
	require.False(t, hit)
	second, hit, err := service.predictVLM(ctx, 7, "ocr", model, image, "same-prompt")
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, first, second)
	require.Equal(t, int32(1), model.calls.Load())

	_, hit, err = service.predictVLM(ctx, 7, "ocr", model, image, "changed-prompt")
	require.NoError(t, err)
	require.False(t, hit)
	require.Equal(t, int32(2), model.calls.Load())
}
