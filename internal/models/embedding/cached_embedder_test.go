package embedding

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingEmbedder struct {
	calls      int
	modelID    string
	modelName  string
	dimensions int
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls++
	return []float32{float32(len(text)), float32(e.calls)}, nil
}

func (e *countingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls++
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{float32(len(text)), float32(i)}
	}
	return out, nil
}

func (e *countingEmbedder) GetModelName() string { return e.modelName }
func (e *countingEmbedder) GetDimensions() int   { return e.dimensions }
func (e *countingEmbedder) GetModelID() string   { return e.modelID }

func (e *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	model Embedder,
	texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func TestCachedEmbedderBatchEmbedReusesSameTextForSameModel(t *testing.T) {
	t.Parallel()

	base := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 3}
	cache := NewMemoryEmbeddingCache()
	cached := NewCachedEmbedder(base, cache, CacheScope{TenantID: 7})

	first, err := cached.BatchEmbed(context.Background(), []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	require.Len(t, first, 3)
	require.Equal(t, 1, base.calls)

	second, err := cached.BatchEmbed(context.Background(), []string{" alpha ", "beta"})
	require.NoError(t, err)
	require.Equal(t, 1, base.calls, "second batch should be served entirely from cache")
	require.Equal(t, first[0], second[0])
	require.Equal(t, first[1], second[1])
}

func TestCachedEmbedderMissesWhenModelOrDimensionChanges(t *testing.T) {
	t.Parallel()

	cache := NewMemoryEmbeddingCache()
	ctx := context.Background()

	modelA := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 3}
	_, err := NewCachedEmbedder(modelA, cache, CacheScope{TenantID: 7}).BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, modelA.calls)

	modelB := &countingEmbedder{modelID: "embed-2", modelName: "text-embedding", dimensions: 3}
	_, err = NewCachedEmbedder(modelB, cache, CacheScope{TenantID: 7}).BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, modelB.calls)

	modelC := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 8}
	_, err = NewCachedEmbedder(modelC, cache, CacheScope{TenantID: 7}).BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, modelC.calls)
}

func TestCachedEmbedderPreservesInputOrderWithPartialHits(t *testing.T) {
	t.Parallel()

	cache := NewMemoryEmbeddingCache()
	base := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 3}
	cached := NewCachedEmbedder(base, cache, CacheScope{TenantID: 7})
	ctx := context.Background()

	seed, err := cached.BatchEmbed(ctx, []string{"cached"})
	require.NoError(t, err)
	require.Equal(t, 1, base.calls)

	got, err := cached.BatchEmbed(ctx, []string{"cached", "new", "cached"})
	require.NoError(t, err)
	require.Equal(t, 2, base.calls)
	require.Equal(t, seed[0], got[0])
	require.Equal(t, seed[0], got[2])
	require.Equal(t, []float32{3, 0}, got[1])
}

func TestCachedEmbedderDoesNotShareAcrossTenants(t *testing.T) {
	t.Parallel()

	cache := NewMemoryEmbeddingCache()
	ctx := context.Background()

	tenantA := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 3}
	_, err := NewCachedEmbedder(tenantA, cache, CacheScope{TenantID: 7}).BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)

	tenantB := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 3}
	_, err = NewCachedEmbedder(tenantB, cache, CacheScope{TenantID: 8}).BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, tenantB.calls)
}

func TestCachedEmbedderDoesNotWriteFailedResults(t *testing.T) {
	t.Parallel()

	errBoom := fmt.Errorf("boom")
	base := &failingEmbedder{err: errBoom}
	cache := NewMemoryEmbeddingCache()
	cached := NewCachedEmbedder(base, cache, CacheScope{TenantID: 7})

	_, err := cached.BatchEmbed(context.Background(), []string{"alpha"})
	require.ErrorIs(t, err, errBoom)

	retry := &countingEmbedder{modelID: "embed-1", modelName: "text-embedding", dimensions: 3}
	got, err := NewCachedEmbedder(retry, cache, CacheScope{TenantID: 7}).BatchEmbed(context.Background(), []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, retry.calls)
	require.Equal(t, []float32{5, 0}, got[0])
}

type failingEmbedder struct {
	err error
}

func (e *failingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, e.err
}

func (e *failingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, e.err
}

func (e *failingEmbedder) GetModelName() string { return "text-embedding" }
func (e *failingEmbedder) GetDimensions() int   { return 3 }
func (e *failingEmbedder) GetModelID() string   { return "embed-1" }

func (e *failingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	model Embedder,
	texts []string,
) ([][]float32, error) {
	return nil, e.err
}
