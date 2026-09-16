package modelcache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// staleLookupStore releases one captured miss only after another caller has
// completed its provider request and persistent cache write.
type staleLookupStore struct {
	*cacheStore
	claim    sync.Once
	captured chan struct{}
	release  chan struct{}
}

func (s *staleLookupStore) GetEmbeddingCache(
	ctx context.Context,
	prefix CachePrefix,
	hashes []string,
) (map[string]*types.EmbeddingCacheEntry, error) {
	delayed := false
	s.claim.Do(func() { delayed = true })
	snapshot, err := s.cacheStore.GetEmbeddingCache(ctx, prefix, hashes)
	if delayed {
		close(s.captured)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return snapshot, err
}

func TestEmbeddingCacheRechecksStaleMissAfterConcurrentFill(t *testing.T) {
	for _, tc := range []struct {
		name     string
		delayed  []string
		expected [][]string
	}{
		{"identical_batch", []string{"alpha"}, [][]string{{"alpha"}}},
		{"partially_overlapping_batch", []string{"alpha", "beta", "alpha"}, [][]string{{"alpha"}, {"beta"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &staleLookupStore{
				cacheStore: &cacheStore{},
				captured:   make(chan struct{}),
				release:    make(chan struct{}),
			}
			coordinator := NewCoordinator(store)
			provider := &countingEmbedder{}
			model := &types.Model{ID: "stale-miss-model", TenantID: 7}
			first, second := coordinator.Wrap(model, provider), coordinator.Wrap(model, provider)
			ctx, cancel := context.WithTimeout(
				context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)),
				5*time.Second,
			)
			defer cancel()
			type answer struct {
				vectors [][]float32
				err     error
			}
			done := make(chan answer, 1)
			go func() { vectors, err := first.BatchEmbed(ctx, tc.delayed); done <- answer{vectors, err} }()
			select {
			case <-store.captured:
			case <-ctx.Done():
				t.Fatal("initial lookup was not captured")
			}
			filled, err := second.BatchEmbed(ctx, []string{"alpha"})
			require.NoError(t, err)
			close(store.release)
			result := <-done
			require.NoError(t, result.err)
			require.Len(t, result.vectors, len(tc.delayed))
			require.Equal(t, filled[0], result.vectors[0])
			if len(tc.delayed) > 1 {
				require.Equal(t, []float32{4, 1}, result.vectors[1])
				require.Equal(t, result.vectors[0], result.vectors[2])
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			require.Equal(t, tc.expected, provider.batchInputs)
		})
	}
}
