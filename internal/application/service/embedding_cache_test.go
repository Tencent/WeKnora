package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryEmbeddingCache struct {
	mu     sync.Mutex
	values map[string][]float32
}

func (m *memoryEmbeddingCache) key(tenant uint64, model, hash string) string {
	return string(rune(tenant)) + model + hash
}
func (m *memoryEmbeddingCache) Get(_ context.Context, tenant uint64, model string, hashes []string) (map[string][]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string][]float32{}
	for _, hash := range hashes {
		if value, ok := m.values[m.key(tenant, model, hash)]; ok {
			result[hash] = value
		}
	}
	return result, nil
}
func (m *memoryEmbeddingCache) Put(_ context.Context, entries []*types.EmbeddingCacheEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range entries {
		var value []float32
		_ = json.Unmarshal(entry.Vector, &value)
		m.values[m.key(entry.TenantID, entry.ModelKey, entry.InputHash)] = value
	}
	return nil
}

type usageCollector struct{ rows []*types.ModelUsage }

func (u *usageCollector) Create(_ context.Context, row *types.ModelUsage) error {
	u.rows = append(u.rows, row)
	return nil
}
func (u *usageCollector) Summary(context.Context, uint64, interfaces.ModelUsageQuery) (*types.ModelUsageSummary, error) {
	return nil, nil
}

type fakeEmbeddingProvider struct {
	calls      int
	inputs     [][]string
	dimensions int
}

func (f *fakeEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	rows, err := f.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}
func (f *fakeEmbeddingProvider) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	f.inputs = append(f.inputs, append([]string(nil), texts...))
	rows := make([][]float32, len(texts))
	for i, text := range texts {
		rows[i] = []float32{float32(len(text)), float32(i + 1)}
	}
	return rows, nil
}
func (f *fakeEmbeddingProvider) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return f.BatchEmbed(ctx, texts)
}
func (f *fakeEmbeddingProvider) GetModelName() string { return "text-embedding-v4" }
func (f *fakeEmbeddingProvider) GetModelID() string   { return "emb-1" }
func (f *fakeEmbeddingProvider) GetDimensions() int   { return f.dimensions }

func TestCachedEmbedderColdWarmAndPartialBatch(t *testing.T) {
	t.Setenv("TOPIC3_EMBEDDING_CACHE_ENABLED", "true")
	provider := &fakeEmbeddingProvider{dimensions: 2}
	cache := &memoryEmbeddingCache{values: map[string][]float32{}}
	usage := &usageCollector{}
	model := &types.Model{ID: "emb-1", TenantID: 7, Name: "text-embedding-v4"}
	cached := wrapEmbeddingCache(provider, cache, usage, 7, model)

	cold, err := cached.BatchEmbed(context.Background(), []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	require.Len(t, cold, 3)
	assert.Equal(t, cold[0], cold[2])
	assert.Equal(t, 1, provider.calls)
	assert.Equal(t, []string{"alpha", "beta"}, provider.inputs[0])

	warm, err := cached.BatchEmbed(context.Background(), []string{"beta", "alpha"})
	require.NoError(t, err)
	assert.Equal(t, 1, provider.calls, "warm cache must not call provider")
	assert.Equal(t, cold[1], warm[0])
	assert.Equal(t, cold[0], warm[1])

	partial, err := cached.BatchEmbed(context.Background(), []string{"alpha", "gamma", "beta"})
	require.NoError(t, err)
	assert.Equal(t, 2, provider.calls)
	assert.Equal(t, []string{"gamma"}, provider.inputs[1])
	assert.Equal(t, cold[0], partial[0])
	assert.Equal(t, cold[1], partial[2])
	require.Len(t, usage.rows, 3)
	assert.Equal(t, 0, usage.rows[1].ActualCalls)
	assert.Equal(t, 2, usage.rows[1].LocalCacheHits)
}

func TestDisabledCacheStillRecordsRealProviderCalls(t *testing.T) {
	t.Setenv("TOPIC3_EMBEDDING_CACHE_ENABLED", "false")
	provider := &fakeEmbeddingProvider{dimensions: 2}
	cache := &memoryEmbeddingCache{values: map[string][]float32{}}
	usage := &usageCollector{}
	model := &types.Model{ID: "emb-1", TenantID: 7, Name: "text-embedding-v4"}
	wrapped := wrapEmbeddingCache(provider, cache, usage, 7, model)

	first, err := wrapped.BatchEmbed(context.Background(), []string{"alpha", "beta"})
	require.NoError(t, err)
	second, err := wrapped.BatchEmbed(context.Background(), []string{"alpha", "beta"})
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 2, provider.calls, "disabled cache must call the provider every time")
	assert.Empty(t, cache.values, "disabled cache must not read or write cached vectors")
	require.Len(t, usage.rows, 2)
	for _, row := range usage.rows {
		assert.Equal(t, 1, row.ActualCalls)
		assert.Equal(t, 2, row.InputCount)
		assert.Equal(t, 0, row.LocalCacheHits)
		assert.Equal(t, "success", row.Status)
	}
}

func TestEmbeddingModelKeySeparatesDimensions(t *testing.T) {
	base := &types.Model{ID: "emb-1", Name: "text-embedding-v4"}
	a := *base
	b := *base
	a.Parameters.EmbeddingParameters.Dimension = 1024
	b.Parameters.EmbeddingParameters.Dimension = 2048
	assert.NotEqual(t, embeddingModelKey(&a), embeddingModelKey(&b))
}
