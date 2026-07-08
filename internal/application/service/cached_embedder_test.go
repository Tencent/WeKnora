package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

type memoryProcessingCache struct {
	rows map[string]*types.ProcessingCache
}

func newMemoryProcessingCache() *memoryProcessingCache {
	return &memoryProcessingCache{rows: map[string]*types.ProcessingCache{}}
}

func (m *memoryProcessingCache) Get(ctx context.Context, tenantID uint64, stage, cacheKey string) (*types.ProcessingCache, error) {
	row := m.rows[processingCacheKey(stage, cacheKey)]
	if row == nil || row.TenantID != tenantID || row.Stage != stage || row.CacheKey != cacheKey {
		return nil, nil
	}
	now := time.Now()
	row.LastHitAt = &now
	copied := *row
	return &copied, nil
}

func (m *memoryProcessingCache) Upsert(ctx context.Context, cache *types.ProcessingCache) error {
	copied := *cache
	m.rows[processingCacheKey(cache.Stage, cache.CacheKey)] = &copied
	return nil
}

type fakeCacheEmbedder struct {
	calls      int
	modelID    string
	modelName  string
	dimensions int
}

func (f *fakeCacheEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	f.calls++
	return f.vectorFor(text, 0), nil
}

func (f *fakeCacheEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = f.vectorFor(text, i)
	}
	return out, nil
}

func (f *fakeCacheEmbedder) BatchEmbedWithPool(ctx context.Context, model embedding.Embedder, texts []string) ([][]float32, error) {
	return f.BatchEmbed(ctx, texts)
}

func (f *fakeCacheEmbedder) GetModelName() string {
	if f.modelName != "" {
		return f.modelName
	}
	return "fake-embed"
}

func (f *fakeCacheEmbedder) GetDimensions() int {
	if f.dimensions > 0 {
		return f.dimensions
	}
	return 2
}

func (f *fakeCacheEmbedder) GetModelID() string {
	if f.modelID != "" {
		return f.modelID
	}
	return "model-1"
}

func (f *fakeCacheEmbedder) vectorFor(text string, ordinal int) []float32 {
	dim := f.GetDimensions()
	if dim <= 0 {
		dim = 2
	}
	out := make([]float32, dim)
	out[0] = float32(len(text))
	if dim > 1 {
		out[1] = float32(ordinal)
	}
	return out
}

func TestCachedEmbedderBatchEmbedCachesAndDedupes(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	inner := &fakeCacheEmbedder{}
	cached := cacheEmbeddingModel(7, cache, inner)

	first, err := cached.BatchEmbed(ctx, []string{"hello", "world", "hello"})
	if err != nil {
		t.Fatalf("first batch embed failed: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected one inner call for deduped misses, got %d", inner.calls)
	}
	if len(first) != 3 || first[0][0] != 5 || first[2][0] != 5 {
		t.Fatalf("unexpected first embeddings: %#v", first)
	}

	second, err := cached.BatchEmbed(ctx, []string{"hello", "world"})
	if err != nil {
		t.Fatalf("second batch embed failed: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected second batch to hit cache, got %d inner calls", inner.calls)
	}
	if len(second) != 2 || second[0][0] != 5 || second[1][0] != 5 {
		t.Fatalf("unexpected second embeddings: %#v", second)
	}

	row, err := cache.Get(ctx, 7, types.ProcessingCacheStageEmbedding, (&cachedEmbedder{inner: inner, cache: cache, tenantID: 7}).embeddingCacheKey("hello"))
	if err != nil {
		t.Fatalf("cache get failed: %v", err)
	}
	if row == nil {
		t.Fatal("expected embedding cache row")
	}
}

func TestCachedEmbedderBatchEmbedWithPoolUsesCache(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()
	inner := &fakeCacheEmbedder{}
	cached := cacheEmbeddingModel(7, cache, inner)

	first, err := cached.BatchEmbedWithPool(ctx, cached, []string{"alpha", "beta", "alpha"})
	if err != nil {
		t.Fatalf("first pool batch embed failed: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected one inner pool call for deduped misses, got %d", inner.calls)
	}
	if len(first) != 3 || first[0][0] != 5 || first[1][0] != 4 || first[2][0] != 5 {
		t.Fatalf("unexpected first pool embeddings: %#v", first)
	}

	second, err := cached.BatchEmbedWithPool(ctx, cached, []string{"beta", "alpha"})
	if err != nil {
		t.Fatalf("second pool batch embed failed: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected second pool batch to hit cache, got %d inner calls", inner.calls)
	}
	if len(second) != 2 || second[0][0] != 4 || second[1][0] != 5 {
		t.Fatalf("unexpected second pool embeddings: %#v", second)
	}
}

func TestCachedEmbedderLayeredInvalidation(t *testing.T) {
	ctx := context.Background()
	cache := newMemoryProcessingCache()

	firstInner := &fakeCacheEmbedder{modelID: "embed-a", dimensions: 2}
	first := cacheEmbeddingModel(7, cache, firstInner)
	if _, err := first.BatchEmbed(ctx, []string{"hello world"}); err != nil {
		t.Fatalf("first embed failed: %v", err)
	}
	if firstInner.calls != 1 {
		t.Fatalf("expected first call to miss cache, got %d calls", firstInner.calls)
	}

	sameModelInner := &fakeCacheEmbedder{modelID: "embed-a", dimensions: 2}
	sameModel := cacheEmbeddingModel(7, cache, sameModelInner)
	if _, err := sameModel.BatchEmbed(ctx, []string{" hello   world "}); err != nil {
		t.Fatalf("same model embed failed: %v", err)
	}
	if sameModelInner.calls != 0 {
		t.Fatalf("whitespace-only text change should hit cache, got %d calls", sameModelInner.calls)
	}

	changedModelInner := &fakeCacheEmbedder{modelID: "embed-b", dimensions: 2}
	changedModel := cacheEmbeddingModel(7, cache, changedModelInner)
	if _, err := changedModel.BatchEmbed(ctx, []string{"hello world"}); err != nil {
		t.Fatalf("changed model embed failed: %v", err)
	}
	if changedModelInner.calls != 1 {
		t.Fatalf("embedding model change should miss cache, got %d calls", changedModelInner.calls)
	}

	changedDimInner := &fakeCacheEmbedder{modelID: "embed-a", dimensions: 3}
	changedDim := cacheEmbeddingModel(7, cache, changedDimInner)
	if _, err := changedDim.BatchEmbed(ctx, []string{"hello world"}); err != nil {
		t.Fatalf("changed dimension embed failed: %v", err)
	}
	if changedDimInner.calls != 1 {
		t.Fatalf("embedding dimension change should miss cache, got %d calls", changedDimInner.calls)
	}
}
