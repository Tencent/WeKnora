package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeContentCacheRepo struct {
	mu        sync.Mutex
	entries   map[string]*types.ContentCacheEntry
	getErr    error
	upsertErr error
}

func newFakeContentCacheRepo() *fakeContentCacheRepo {
	return &fakeContentCacheRepo{entries: make(map[string]*types.ContentCacheEntry)}
}

func (r *fakeContentCacheRepo) GetByKey(
	_ context.Context,
	tenantID uint64,
	cacheKind, cacheKey string,
) (*types.ContentCacheEntry, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[cacheEntryKey(tenantID, cacheKind, cacheKey)]
	if entry == nil {
		return nil, nil
	}
	copied := *entry
	return &copied, nil
}

func (r *fakeContentCacheRepo) Upsert(_ context.Context, entry *types.ContentCacheEntry) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := *entry
	r.entries[cacheEntryKey(entry.TenantID, entry.CacheKind, entry.CacheKey)] = &copied
	return nil
}

func cacheEntryKey(tenantID uint64, cacheKind, cacheKey string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", tenantID, cacheKind, cacheKey)
}

type fakeEmbeddingModel struct {
	modelID    string
	modelName  string
	dimensions int
	calls      [][]string
}

func (m *fakeEmbeddingModel) Embed(ctx context.Context, text string) ([]float32, error) {
	got, err := m.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return got[0], nil
}

func (m *fakeEmbeddingModel) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	m.calls = append(m.calls, append([]string(nil), texts...))
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{float32(len(text)), float32(m.dimensions)}
	}
	return out, nil
}

func (m *fakeEmbeddingModel) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	return m.BatchEmbed(ctx, texts)
}

func (m *fakeEmbeddingModel) GetModelName() string {
	return m.modelName
}

func (m *fakeEmbeddingModel) GetDimensions() int {
	return m.dimensions
}

func (m *fakeEmbeddingModel) GetModelID() string {
	return m.modelID
}

func TestEmbeddingCacheBatchDeduplicatesAndHitsSecondPass(t *testing.T) {
	ctx := context.Background()
	repo := newFakeContentCacheRepo()
	inner := &fakeEmbeddingModel{modelID: "model-a", dimensions: 2}
	cached := withEmbeddingCache(inner, repo, 1)

	first, err := cached.BatchEmbed(ctx, []string{"same", "same", "other"})
	require.NoError(t, err)
	require.Len(t, first, 3)
	require.Len(t, inner.calls, 1)
	assert.Equal(t, []string{"same", "other"}, inner.calls[0])
	assert.Equal(t, first[0], first[1])

	second, err := cached.BatchEmbed(ctx, []string{"same", "same", "other"})
	require.NoError(t, err)
	assert.Equal(t, first, second)
	require.Len(t, inner.calls, 1, "second identical batch should be fully cached")
}

func TestEmbeddingCacheMissesOnTextModelAndDimensions(t *testing.T) {
	ctx := context.Background()
	repo := newFakeContentCacheRepo()

	modelA := &fakeEmbeddingModel{modelID: "model-a", dimensions: 2}
	cachedA := withEmbeddingCache(modelA, repo, 1)
	_, err := cachedA.BatchEmbed(ctx, []string{"text"})
	require.NoError(t, err)
	_, err = cachedA.BatchEmbed(ctx, []string{"text"})
	require.NoError(t, err)
	require.Len(t, modelA.calls, 1)

	_, err = cachedA.BatchEmbed(ctx, []string{"changed"})
	require.NoError(t, err)
	require.Len(t, modelA.calls, 2)

	modelB := &fakeEmbeddingModel{modelID: "model-b", dimensions: 2}
	_, err = withEmbeddingCache(modelB, repo, 1).BatchEmbed(ctx, []string{"text"})
	require.NoError(t, err)
	require.Len(t, modelB.calls, 1)

	modelDim := &fakeEmbeddingModel{modelID: "model-a", dimensions: 3}
	_, err = withEmbeddingCache(modelDim, repo, 1).BatchEmbed(ctx, []string{"text"})
	require.NoError(t, err)
	require.Len(t, modelDim.calls, 1)
}

func TestEmbeddingCacheFallsBackOnCacheErrors(t *testing.T) {
	ctx := context.Background()
	repo := newFakeContentCacheRepo()
	repo.getErr = errors.New("cache read failed")
	repo.upsertErr = errors.New("cache write failed")
	inner := &fakeEmbeddingModel{modelID: "model-a", dimensions: 2}

	got, err := withEmbeddingCache(inner, repo, 1).BatchEmbed(ctx, []string{"text"})

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, inner.calls, 1)
}
