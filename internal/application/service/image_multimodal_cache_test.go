package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVLMCacheSecondPassSkipsCompute(t *testing.T) {
	ctx := context.Background()
	svc := &ImageMultimodalService{contentCacheRepo: newFakeContentCacheRepo()}
	calls := 0
	compute := func() (string, error) {
		calls++
		return "caption", nil
	}

	first, hit, err := svc.getOrComputeVLMResult(
		ctx, 1, types.ContentCacheKindImageCaption, vlmCacheModeCaption, "vlm-a", []byte("image"), "prompt", compute,
	)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "caption", first)

	second, hit, err := svc.getOrComputeVLMResult(
		ctx, 1, types.ContentCacheKindImageCaption, vlmCacheModeCaption, "vlm-a", []byte("image"), "prompt", compute,
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, "caption", second)
	assert.Equal(t, 1, calls)
}

func TestVLMCacheOCRAndCaptionAreIndependent(t *testing.T) {
	ctx := context.Background()
	svc := &ImageMultimodalService{contentCacheRepo: newFakeContentCacheRepo()}
	ocrCalls := 0
	captionCalls := 0

	_, _, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image"), "prompt", func() (string, error) {
			ocrCalls++
			return "ocr", nil
		})
	require.NoError(t, err)
	_, _, err = svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageCaption, vlmCacheModeCaption,
		"vlm-a", []byte("image"), "prompt", func() (string, error) {
			captionCalls++
			return "caption", nil
		})
	require.NoError(t, err)

	assert.Equal(t, 1, ocrCalls)
	assert.Equal(t, 1, captionCalls)
}

func TestVLMCacheInvalidatesOnImageModelAndPrompt(t *testing.T) {
	ctx := context.Background()
	svc := &ImageMultimodalService{contentCacheRepo: newFakeContentCacheRepo()}
	calls := 0
	compute := func() (string, error) {
		calls++
		return "ocr", nil
	}

	_, _, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image-a"), "prompt-a", compute)
	require.NoError(t, err)
	_, _, err = svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image-b"), "prompt-a", compute)
	require.NoError(t, err)
	_, _, err = svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-b", []byte("image-a"), "prompt-a", compute)
	require.NoError(t, err)
	_, _, err = svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image-a"), "prompt-b", compute)
	require.NoError(t, err)

	assert.Equal(t, 4, calls)
}

func TestVLMCacheDoesNotWriteErrors(t *testing.T) {
	ctx := context.Background()
	repo := newFakeContentCacheRepo()
	svc := &ImageMultimodalService{contentCacheRepo: repo}
	calls := 0

	_, _, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image"), "prompt", func() (string, error) {
			calls++
			return "", errors.New("vlm failed")
		})
	require.Error(t, err)
	assert.Empty(t, repo.entries)

	got, hit, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image"), "prompt", func() (string, error) {
			calls++
			return "ocr", nil
		})
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "ocr", got)
	assert.Equal(t, 2, calls)
}

func TestVLMCacheStoresEmptySuccess(t *testing.T) {
	ctx := context.Background()
	svc := &ImageMultimodalService{contentCacheRepo: newFakeContentCacheRepo()}
	calls := 0
	compute := func() (string, error) {
		calls++
		return "", nil
	}

	_, hit, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageCaption, vlmCacheModeCaption,
		"vlm-a", []byte("image"), "prompt", compute)
	require.NoError(t, err)
	assert.False(t, hit)

	got, hit, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageCaption, vlmCacheModeCaption,
		"vlm-a", []byte("image"), "prompt", compute)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Empty(t, got)
	assert.Equal(t, 1, calls)
}

func TestVLMCacheFallsBackOnCacheErrors(t *testing.T) {
	ctx := context.Background()
	repo := newFakeContentCacheRepo()
	repo.getErr = errors.New("cache read failed")
	repo.upsertErr = errors.New("cache write failed")
	svc := &ImageMultimodalService{contentCacheRepo: repo}
	calls := 0

	got, hit, err := svc.getOrComputeVLMResult(ctx, 1, types.ContentCacheKindImageOCR, vlmCacheModeOCR,
		"vlm-a", []byte("image"), "prompt", func() (string, error) {
			calls++
			return "ocr", nil
		})

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "ocr", got)
	assert.Equal(t, 1, calls)
}
