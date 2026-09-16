package modelcache

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type interruptedEmbedder struct {
	countingEmbedder
	seen   []string
	failAt int
	cancel context.CancelFunc
}

func (e *interruptedEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	if e.failAt > 0 && len(e.seen) >= e.failAt {
		return nil, errors.New("provider unavailable")
	}
	e.seen = append(e.seen, texts...)
	result := make([][]float32, len(texts))
	for i, text := range texts {
		result[i] = []float32{float32(len(text)), 1}
	}
	if e.cancel != nil {
		e.cancel()
	}
	return result, nil
}

func TestPooledCachePreservesProgressAcrossFailureAndRestart(t *testing.T) {
	store := &cacheStore{}
	model := &types.Model{ID: "embedding-1", TenantID: 7}
	texts := make([]string, 500)
	for i := range texts {
		texts[i] = fmt.Sprintf("passage %d %s", i, string(make([]byte, i%11)))
	}
	// Exercise restoration of a duplicate that crosses a persistence window.
	texts[499] = texts[0]
	provider := &interruptedEmbedder{failAt: 128}
	wrapped := NewCoordinator(store).Wrap(model, provider)
	_, err := wrapped.BatchEmbedWithPool(context.Background(), wrapped, texts)
	require.ErrorContains(t, err, "provider unavailable")
	require.Len(t, store.entries, 128)

	// A new coordinator and provider represent a process restart with the same store.
	restarted := &interruptedEmbedder{}
	wrapped = NewCoordinator(store).Wrap(model, restarted)
	vectors, err := wrapped.BatchEmbedWithPool(context.Background(), wrapped, texts)
	require.NoError(t, err)
	require.Len(t, vectors, 500)
	require.Equal(t, texts[128:499], restarted.seen)
	for i, text := range texts {
		require.Equal(t, []float32{float32(len(text)), 1}, vectors[i])
	}
	vectors[0][0] = -1
	require.NotEqual(t, vectors[0], vectors[499], "duplicate vectors must not alias")

	restarted.seen = nil
	_, err = wrapped.BatchEmbedWithPool(context.Background(), wrapped, texts)
	require.NoError(t, err)
	require.Empty(t, restarted.seen, "a complete warm retry must make no provider calls")
}

func TestPooledCacheStopsBeforeNextWindowAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &interruptedEmbedder{cancel: cancel}
	wrapped := NewCoordinator(&cacheStore{}).Wrap(&types.Model{ID: "embedding-1", TenantID: 7}, provider)
	texts := make([]string, 500)
	for i := range texts {
		texts[i] = fmt.Sprintf("passage %d", i)
	}
	_, err := wrapped.BatchEmbedWithPool(ctx, wrapped, texts)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, provider.seen, 64, "cancellation must stop subsequent paid work")
}
