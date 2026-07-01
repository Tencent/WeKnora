package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakeVLMImageResultCacheRepo struct {
	entries     map[string]*types.VLMImageResultCache
	putCalls    int
	getCalls    int
	conflict    bool
	conflictHit *types.VLMImageResultCache
}

func (f *fakeVLMImageResultCacheRepo) GetByKey(
	ctx context.Context,
	tenantID uint64,
	cacheKey string,
) (*types.VLMImageResultCache, error) {
	f.getCalls++
	if f.conflict && f.getCalls > 1 {
		return f.conflictHit, nil
	}
	if f.entries == nil {
		return nil, nil
	}
	return f.entries[cacheKey], nil
}

func (f *fakeVLMImageResultCacheRepo) PutIfAbsent(
	ctx context.Context,
	entry *types.VLMImageResultCache,
) (bool, error) {
	f.putCalls++
	if f.conflict {
		return false, nil
	}
	if f.entries == nil {
		f.entries = map[string]*types.VLMImageResultCache{}
	}
	if _, ok := f.entries[entry.CacheKey]; ok {
		return false, nil
	}
	copyEntry := *entry
	f.entries[entry.CacheKey] = &copyEntry
	return true, nil
}

func TestPredictImageResultWithCache_MissThenHit(t *testing.T) {
	repo := &fakeVLMImageResultCacheRepo{}
	svc := &ImageMultimodalService{vlmCacheRepo: repo}
	predictCalls := 0
	req := vlmImageResultRequest{
		TenantID:                   1,
		ImageBytes:                 []byte("image"),
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeCaption,
		Prompt:                     "caption prompt",
		PromptVersion:              types.VLMCaptionPromptVersion,
		ResultCanonicalizerVersion: types.VLMCaptionCanonicalizerV1,
		Predict: func(context.Context) (string, error) {
			predictCalls++
			return "  cached caption  ", nil
		},
		Canonicalize: strings.TrimSpace,
	}

	got, hit, err := svc.predictImageResultWithCache(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("first call should miss")
	}
	if got != "cached caption" {
		t.Fatalf("unexpected canonicalized content: %q", got)
	}
	if predictCalls != 1 || repo.putCalls != 1 {
		t.Fatalf("unexpected calls: predict=%d put=%d", predictCalls, repo.putCalls)
	}

	got, hit, err = svc.predictImageResultWithCache(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("second call should hit cache")
	}
	if got != "cached caption" {
		t.Fatalf("unexpected cached content: %q", got)
	}
	if predictCalls != 1 {
		t.Fatalf("cache hit should not call VLM again, calls=%d", predictCalls)
	}
}

func TestPredictImageResultWithCache_ConflictUsesExistingResult(t *testing.T) {
	repo := &fakeVLMImageResultCacheRepo{
		conflict: true,
		conflictHit: &types.VLMImageResultCache{
			Content: "existing canonical result",
		},
	}
	svc := &ImageMultimodalService{vlmCacheRepo: repo}

	got, hit, err := svc.predictImageResultWithCache(context.Background(), vlmImageResultRequest{
		TenantID:                   1,
		ImageBytes:                 []byte("image"),
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeOCR,
		Prompt:                     "ocr prompt",
		PromptVersion:              types.VLMOCRPromptVersion,
		ResultCanonicalizerVersion: types.VLMOCRCanonicalizerV1,
		Predict: func(context.Context) (string, error) {
			return "current result", nil
		},
		Canonicalize: strings.TrimSpace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("conflict read should be treated as cache hit")
	}
	if got != "existing canonical result" {
		t.Fatalf("expected existing result after conflict, got %q", got)
	}
	if repo.putCalls != 1 || repo.getCalls != 2 {
		t.Fatalf("unexpected cache calls: get=%d put=%d", repo.getCalls, repo.putCalls)
	}
}

func TestPredictImageResultWithCache_PredictErrorDoesNotWrite(t *testing.T) {
	repo := &fakeVLMImageResultCacheRepo{}
	svc := &ImageMultimodalService{vlmCacheRepo: repo}
	wantErr := errors.New("vlm failed")

	_, hit, err := svc.predictImageResultWithCache(context.Background(), vlmImageResultRequest{
		TenantID:                   1,
		ImageBytes:                 []byte("image"),
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeOCR,
		Prompt:                     "ocr prompt",
		PromptVersion:              types.VLMOCRPromptVersion,
		ResultCanonicalizerVersion: types.VLMOCRCanonicalizerV1,
		Predict: func(context.Context) (string, error) {
			return "", wantErr
		},
		Canonicalize: strings.TrimSpace,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected VLM error, got %v", err)
	}
	if hit {
		t.Fatal("failed prediction should not be a cache hit")
	}
	if repo.putCalls != 0 {
		t.Fatalf("failed prediction should not write cache, putCalls=%d", repo.putCalls)
	}
}

func TestPredictImageResultWithCache_EmptyContentHitSkipsPredict(t *testing.T) {
	cacheKey, err := types.BuildVLMImageResultCacheKey(types.VLMImageResultCacheKeyPayload{
		ImageHash:                  types.HashVLMImageBytes([]byte("image")),
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeOCR,
		PromptVersion:              types.VLMOCRPromptVersion,
		PromptHash:                 types.HashVLMPrompt("ocr prompt"),
		ResultCanonicalizerVersion: types.VLMOCRCanonicalizerV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeVLMImageResultCacheRepo{
		entries: map[string]*types.VLMImageResultCache{
			cacheKey: {Content: ""},
		},
	}
	svc := &ImageMultimodalService{vlmCacheRepo: repo}
	predictCalls := 0

	got, hit, err := svc.predictImageResultWithCache(context.Background(), vlmImageResultRequest{
		TenantID:                   1,
		ImageBytes:                 []byte("image"),
		ModelFingerprint:           "model-fp",
		ResultType:                 types.VLMImageResultTypeOCR,
		Prompt:                     "ocr prompt",
		PromptVersion:              types.VLMOCRPromptVersion,
		ResultCanonicalizerVersion: types.VLMOCRCanonicalizerV1,
		Predict: func(context.Context) (string, error) {
			predictCalls++
			return "should not run", nil
		},
		Canonicalize: strings.TrimSpace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("empty content entry should be a hit")
	}
	if got != "" {
		t.Fatalf("expected empty cached content, got %q", got)
	}
	if predictCalls != 0 {
		t.Fatalf("cache hit should skip VLM, calls=%d", predictCalls)
	}
}
