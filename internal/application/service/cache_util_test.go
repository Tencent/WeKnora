package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memCacheRepo is an in-memory ContentCacheRepository for service-level tests.
type memCacheRepo struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemCacheRepo() *memCacheRepo { return &memCacheRepo{m: map[string][]byte{}} }

func (r *memCacheRepo) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.m[key]
	return v, ok, nil
}

func (r *memCacheRepo) Set(_ context.Context, key, _ string, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[key] = payload
	return nil
}

func (r *memCacheRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, key)
	return nil
}

func (r *memCacheRepo) PruneBefore(_ context.Context, _ time.Time, _ int) (int, error) { return 0, nil }

// fakeEmbedder counts provider calls so tests can assert cache hits.
type fakeEmbedder struct {
	modelID   string
	dims      int
	callCount int
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	f.callCount++
	return []float32{float32(len(text))}, nil
}

func (f *fakeEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	f.callCount += len(texts)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{float32(len(t))}
	}
	return out, nil
}

func (f *fakeEmbedder) BatchEmbedWithPool(_ context.Context, model embedding.Embedder, texts []string) ([][]float32, error) {
	return model.BatchEmbed(context.Background(), texts)
}

func (f *fakeEmbedder) GetModelName() string { return "fake-model" }
func (f *fakeEmbedder) GetDimensions() int   { return f.dims }
func (f *fakeEmbedder) GetModelID() string   { return f.modelID }

func TestCachingEmbedder_EmbedHitsCache(t *testing.T) {
	cache := &contentCache{repo: newMemCacheRepo()}
	inner := &fakeEmbedder{modelID: "emb-a", dims: 3}
	wrapped := cache.wrapEmbedder(inner)
	require.NotNil(t, wrapped)

	ctx := context.Background()
	v1, err := wrapped.Embed(ctx, "hello world")
	require.NoError(t, err)
	v2, err := wrapped.Embed(ctx, "hello world")
	require.NoError(t, err)
	assert.Equal(t, v1, v2)
	assert.Equal(t, 1, inner.callCount, "second Embed must be served from cache")

	// A different text is a miss.
	_, err = wrapped.Embed(ctx, "different text")
	require.NoError(t, err)
	assert.Equal(t, 2, inner.callCount)
}

func TestCachingEmbedder_ModelInKey(t *testing.T) {
	cache := &contentCache{repo: newMemCacheRepo()}
	innerA := &fakeEmbedder{modelID: "emb-a", dims: 3}
	innerB := &fakeEmbedder{modelID: "emb-b", dims: 3}
	wrappedA := cache.wrapEmbedder(innerA)
	wrappedB := cache.wrapEmbedder(innerB)

	ctx := context.Background()
	_, _ = wrappedA.Embed(ctx, "same text")
	_, _ = wrappedB.Embed(ctx, "same text")
	assert.Equal(t, 1, innerA.callCount)
	assert.Equal(t, 1, innerB.callCount, "different model must not share embedding cache")
}

func TestCachingEmbedder_BatchEmbedPartialHit(t *testing.T) {
	cache := &contentCache{repo: newMemCacheRepo()}
	inner := &fakeEmbedder{modelID: "emb-a", dims: 3}
	wrapped := cache.wrapEmbedder(inner)

	ctx := context.Background()
	first, err := wrapped.BatchEmbed(ctx, []string{"alpha", "beta"})
	require.NoError(t, err)
	assert.Equal(t, 2, inner.callCount)

	// Second batch overlaps "beta" only; "gamma" is new.
	second, err := wrapped.BatchEmbed(ctx, []string{"beta", "gamma"})
	require.NoError(t, err)
	assert.Equal(t, 3, inner.callCount, "only the unseen text should hit the provider")
	assert.Equal(t, first[1], second[0])
}

func TestCachingEmbedder_NilCacheIsPassthrough(t *testing.T) {
	inner := &fakeEmbedder{modelID: "emb-a", dims: 3}
	var cache *contentCache
	wrapped := cache.wrapEmbedder(inner)
	require.NotNil(t, wrapped, "nil cache must still return a working embedder")

	ctx := context.Background()
	_, err := wrapped.Embed(ctx, "text")
	require.NoError(t, err)
	_, err = wrapped.Embed(ctx, "text")
	require.NoError(t, err)
	assert.Equal(t, 2, inner.callCount, "without a cache store every call reaches the provider")
}

func TestContentCache_GetSetJSONAndNilSafety(t *testing.T) {
	cache := &contentCache{repo: newMemCacheRepo()}
	ctx := context.Background()

	type payload struct {
		A string `json:"a"`
	}
	key := types.ContentCacheKey(types.ContentCacheKindSummary, "model", "content")
	cache.set(ctx, key, types.ContentCacheKindSummary, payload{A: "summary"})

	var out payload
	hit, err := cache.get(ctx, key, &out)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, "summary", out.A)

	// Missing key -> miss.
	hit, err = cache.get(ctx, types.ContentCacheKey(types.ContentCacheKindSummary, "other"), &out)
	require.NoError(t, err)
	assert.False(t, hit)

	// Nil cache behaves as a permanent miss without panicking.
	var nilCache *contentCache
	hit, err = nilCache.get(ctx, key, &out)
	require.NoError(t, err)
	assert.False(t, hit)
	nilCache.set(ctx, key, types.ContentCacheKindSummary, payload{A: "x"})
}

// TestWikiMapCacheKey_Deterministic verifies the wiki per-document map cache
// key is a pure function of its inputs: identical inputs produce the same key,
// slug ordering is irrelevant, and any input change (model, content, language,
// granularity, instructions) moves the key so only that layer invalidates.
func TestWikiMapCacheKey_Deterministic(t *testing.T) {
	batchCtx := &WikiBatchContext{
		ExtractionGranularity:  types.WikiExtractionStandard,
		ContentInstructions:    "content-instr",
		ExtractionInstructions: "extract-instr",
	}
	slugs := map[string]bool{"entity/alpha": true, "concept/beta": true}
	key1 := wikiMapCacheKey("model-a", "doc-content", "zh", slugs, batchCtx)
	key2 := wikiMapCacheKey("model-a", "doc-content", "zh", slugs, batchCtx)
	require.Equal(t, key1, key2)

	// Slug ordering must not change the key (sorted before hashing).
	require.Equal(t, key1, wikiMapCacheKey("model-a", "doc-content", "zh",
		map[string]bool{"concept/beta": true, "entity/alpha": true}, batchCtx))

	// Each input change moves the key.
	require.NotEqual(t, key1, wikiMapCacheKey("model-b", "doc-content", "zh", slugs, batchCtx))
	require.NotEqual(t, key1, wikiMapCacheKey("model-a", "doc-content-v2", "zh", slugs, batchCtx))
	require.NotEqual(t, key1, wikiMapCacheKey("model-a", "doc-content", "en", slugs, batchCtx))

	alt := *batchCtx
	alt.ExtractionGranularity = types.WikiExtractionExhaustive
	require.NotEqual(t, key1, wikiMapCacheKey("model-a", "doc-content", "zh", slugs, &alt))

	alt2 := *batchCtx
	alt2.ContentInstructions = "content-instr-v2"
	require.NotEqual(t, key1, wikiMapCacheKey("model-a", "doc-content", "zh", slugs, &alt2))
}
