package embedding

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type embeddingObservationCollector struct{ calls []types.LLMCallObservation }

func (c *embeddingObservationCollector) ObserveLLMCall(call types.LLMCallObservation) {
	c.calls = append(c.calls, call)
}

func TestObservableEmbedderRecordsProviderCallsBelowCache(t *testing.T) {
	inner := &cacheTestEmbedder{modelID: "embed-model"}
	observed := wrapEmbeddingObservability(inner)
	cached := newCachedEmbedder(observed, "namespace", embeddingCacheOptions{
		enabled: true, ttl: defaultEmbeddingCacheTTL, maxEntries: 10,
	}, newEmbeddingCacheStore())
	collector := &embeddingObservationCollector{}
	ctx := types.WithLLMCallObserver(context.Background(), collector)

	_, err := cached.Embed(ctx, "same text")
	require.NoError(t, err)
	_, err = cached.Embed(ctx, "same text")
	require.NoError(t, err)

	require.Len(t, collector.calls, 1)
	require.Equal(t, types.ModelTypeEmbedding, collector.calls[0].ModelType)
	require.Equal(t, "embed-model", collector.calls[0].ModelID)
	require.Equal(t, "embedding", collector.calls[0].Purpose)
}

type observationPoolEmbedder struct{ cacheTestEmbedder }

func (e *observationPoolEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	middle := len(texts) / 2
	first, err := model.BatchEmbed(ctx, texts[:middle])
	if err != nil {
		return nil, err
	}
	second, err := model.BatchEmbed(ctx, texts[middle:])
	return append(first, second...), err
}

func TestObservableEmbedderRecordsEachProviderSubBatch(t *testing.T) {
	inner := &observationPoolEmbedder{cacheTestEmbedder: cacheTestEmbedder{modelID: "embed-model"}}
	observed := wrapEmbeddingObservability(inner)
	collector := &embeddingObservationCollector{}
	ctx := types.WithLLMCallObserver(context.Background(), collector)

	vectors, err := observed.BatchEmbedWithPool(ctx, observed, []string{"a", "b", "c", "d"})
	require.NoError(t, err)
	require.Len(t, vectors, 4)
	require.Equal(t, 2, inner.batchCalls)
	require.Len(t, collector.calls, 2)
	for _, call := range collector.calls {
		require.Equal(t, types.ModelTypeEmbedding, call.ModelType)
		require.Equal(t, "embed-model", call.ModelID)
		require.True(t, call.Success)
	}
}
