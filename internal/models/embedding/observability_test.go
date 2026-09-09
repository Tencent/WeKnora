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
