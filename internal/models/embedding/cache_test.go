package embedding

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type cacheTestEmbedder struct {
	modelID       string
	embedCalls    int
	batchCalls    int
	pooledCalls   int
	returnedCount int
}

type cacheTestPersistentBackend struct {
	mu        sync.Mutex
	vectors   map[string][]float32
	expiresAt map[string]time.Time
}

type cacheObservationCollector struct {
	observations []types.EmbeddingCacheObservation
}

func (*cacheObservationCollector) ObserveLLMCall(types.LLMCallObservation) {}

func (c *cacheObservationCollector) ObserveEmbeddingCache(observation types.EmbeddingCacheObservation) {
	c.observations = append(c.observations, observation)
}

func (b *cacheTestPersistentBackend) Get(
	_ context.Context, keys []string, now time.Time,
) (map[string]PersistentCacheEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make(map[string]PersistentCacheEntry)
	for _, key := range keys {
		if vector, ok := b.vectors[key]; ok && now.Before(b.expiresAt[key]) {
			result[key] = PersistentCacheEntry{
				Vector: cloneVector(vector), ExpiresAt: b.expiresAt[key],
			}
		}
	}
	return result, nil
}

func (b *cacheTestPersistentBackend) Put(
	_ context.Context, _ string, vectors map[string][]float32, expiresAt time.Time,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.expiresAt == nil {
		b.expiresAt = make(map[string]time.Time)
	}
	for key, vector := range vectors {
		b.vectors[key] = cloneVector(vector)
		b.expiresAt[key] = expiresAt
	}
	return nil
}

func TestCachedEmbedderReusesPersistentTenantEntryAcrossInstances(t *testing.T) {
	backend := &cacheTestPersistentBackend{
		vectors: make(map[string][]float32), expiresAt: make(map[string]time.Time),
	}
	SetPersistentCache(backend)
	t.Cleanup(func() { SetPersistentCache(nil) })
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(11))

	firstInner := &cacheTestEmbedder{modelID: "persistent-model"}
	first := newTestCachedEmbedder(firstInner)
	_, err := first.Embed(ctx, "same text")
	require.NoError(t, err)
	require.Equal(t, 1, firstInner.embedCalls)

	secondInner := &cacheTestEmbedder{modelID: "persistent-model"}
	second := newTestCachedEmbedder(secondInner)
	vector, err := second.Embed(ctx, "same text")
	require.NoError(t, err)
	require.Equal(t, cacheTestVector("same text"), vector)
	require.Zero(t, secondInner.embedCalls)

	otherTenant := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(12))
	_, err = second.Embed(otherTenant, "same text")
	require.NoError(t, err)
	require.Equal(t, 1, secondInner.embedCalls)
}

func TestCachedEmbedderSeparatesQueryAndPassageKeys(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)
	passageCtx := context.Background()
	queryCtx := context.WithValue(passageCtx, types.EmbedQueryContextKey, true)

	_, err := cached.Embed(passageCtx, "same text")
	require.NoError(t, err)
	_, err = cached.Embed(queryCtx, "same text")
	require.NoError(t, err)
	_, err = cached.Embed(passageCtx, "same text")
	require.NoError(t, err)
	require.Equal(t, 2, inner.embedCalls)
}

func TestCachedEmbedderPreservesPersistentExpiry(t *testing.T) {
	now := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(11))
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)
	cached.now = func() time.Time { return now }
	key := cached.key(ctx, "expires soon")
	backend := &cacheTestPersistentBackend{
		vectors:   map[string][]float32{key: cacheTestVector("persisted")},
		expiresAt: map[string]time.Time{key: now.Add(time.Minute)},
	}
	SetPersistentCache(backend)
	t.Cleanup(func() { SetPersistentCache(nil) })

	_, err := cached.Embed(ctx, "expires soon")
	require.NoError(t, err)
	require.Zero(t, inner.embedCalls)
	now = now.Add(2 * time.Minute)
	_, err = cached.Embed(ctx, "expires soon")
	require.NoError(t, err)
	require.Equal(t, 1, inner.embedCalls)
}

func (e *cacheTestEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.embedCalls++
	return cacheTestVector(text), nil
}

func (e *cacheTestEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.batchCalls++
	count := len(texts)
	if e.returnedCount > 0 {
		count = e.returnedCount
	}
	vectors := make([][]float32, count)
	for index := range vectors {
		vectors[index] = cacheTestVector(texts[index])
	}
	return vectors, nil
}

func (e *cacheTestEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ Embedder, texts []string,
) ([][]float32, error) {
	e.pooledCalls++
	return e.BatchEmbed(ctx, texts)
}

func (e *cacheTestEmbedder) GetModelName() string { return "cache-test" }
func (e *cacheTestEmbedder) GetDimensions() int   { return 2 }
func (e *cacheTestEmbedder) GetModelID() string   { return e.modelID }

func TestCachedEmbedderReusesSingleEmbeddingAndClonesVectors(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)

	first, err := cached.Embed(context.Background(), "same text")
	require.NoError(t, err)
	first[0] = 999
	second, err := cached.Embed(context.Background(), "same text")
	require.NoError(t, err)

	require.Equal(t, 1, inner.embedCalls)
	require.Equal(t, cacheTestVector("same text"), second)
}

func TestCachedEmbedderBatchPreservesOrderAndDeduplicates(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)

	first, err := cached.BatchEmbed(context.Background(), []string{"a", "b", "a"})
	require.NoError(t, err)
	require.Equal(t, [][]float32{cacheTestVector("a"), cacheTestVector("b"), cacheTestVector("a")}, first)
	require.Equal(t, 1, inner.batchCalls)

	second, err := cached.BatchEmbed(context.Background(), []string{"b", "a"})
	require.NoError(t, err)
	require.Equal(t, [][]float32{cacheTestVector("b"), cacheTestVector("a")}, second)
	require.Equal(t, 1, inner.batchCalls)
}

func TestCachedEmbedderReportsHitsMissesAndBatchDeduplication(t *testing.T) {
	collector := &cacheObservationCollector{}
	types.SetGlobalLLMCallObserver(collector)
	t.Cleanup(func() { types.SetGlobalLLMCallObserver(nil) })
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)

	_, err := cached.BatchEmbed(ctx, []string{"a", "b", "a"})
	require.NoError(t, err)
	_, err = cached.BatchEmbed(ctx, []string{"b", "a"})
	require.NoError(t, err)

	require.Len(t, collector.observations, 2)
	require.Equal(t, types.EmbeddingCacheObservation{
		TenantID: 42, ModelID: t.Name(), ModelName: "cache-test",
		LookupCount: 3, HitCount: 0, MissCount: 2, DeduplicatedCount: 1,
		AvoidedComputations: 1,
	}, collector.observations[0])
	require.Equal(t, types.EmbeddingCacheObservation{
		TenantID: 42, ModelID: t.Name(), ModelName: "cache-test",
		LookupCount: 2, HitCount: 2, MissCount: 0, DeduplicatedCount: 0,
		AvoidedComputations: 2,
	}, collector.observations[1])
}

func TestCachedEmbedderPooledPathOnlyFetchesMisses(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)

	_, err := cached.Embed(context.Background(), "cached")
	require.NoError(t, err)
	result, err := cached.BatchEmbedWithPool(context.Background(), cached, []string{"cached", "new", "new"})
	require.NoError(t, err)

	require.Equal(t, [][]float32{cacheTestVector("cached"), cacheTestVector("new"), cacheTestVector("new")}, result)
	require.Equal(t, 1, inner.pooledCalls)
	require.Equal(t, 1, inner.batchCalls)
}

func TestCachedEmbedderRejectsMismatchedProviderResponse(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name(), returnedCount: 1}
	cached := newTestCachedEmbedder(inner)

	_, err := cached.BatchEmbed(context.Background(), []string{"one", "two"})
	require.ErrorContains(t, err, "returned 1 vectors for 2 inputs")
}

func TestCachedEmbedderExpiresEntries(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	cached.now = func() time.Time { return now }

	_, err := cached.Embed(context.Background(), "expires")
	require.NoError(t, err)
	now = now.Add(2 * time.Hour)
	_, err = cached.Embed(context.Background(), "expires")
	require.NoError(t, err)
	require.Equal(t, 2, inner.embedCalls)
}

func TestCachedEmbedderEvictsLeastRecentlyUsedEntry(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: t.Name()}
	cached := newTestCachedEmbedder(inner)
	cached.options.maxEntries = 2

	for _, text := range []string{"a", "b", "a", "c", "b"} {
		_, err := cached.Embed(context.Background(), text)
		require.NoError(t, err)
	}
	require.Equal(t, 4, inner.embedCalls)
}

func TestEmbeddingCacheOptions(t *testing.T) {
	options := embeddingCacheOptionsFromConfig(Config{ExtraConfig: map[string]string{
		embeddingCacheEnabledKey:    "false",
		embeddingCacheTTLSecondsKey: "60",
		embeddingCacheMaxEntriesKey: "25",
	}})
	require.False(t, options.enabled)
	require.Equal(t, time.Minute, options.ttl)
	require.Equal(t, 25, options.maxEntries)
}

func newTestCachedEmbedder(inner Embedder) *cachedEmbedder {
	return newCachedEmbedder(inner, inner.GetModelID(), embeddingCacheOptions{
		enabled: true, ttl: time.Hour, maxEntries: 100,
	}, newEmbeddingCacheStore()).(*cachedEmbedder)
}

func cacheTestVector(text string) []float32 {
	return []float32{float32(len(text)), float32(len(text)) + 0.5}
}
