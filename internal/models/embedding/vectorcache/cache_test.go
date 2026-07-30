package vectorcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type failingCache struct{}

func (failingCache) GetMany(_ context.Context, _ []string) (map[string][]float32, error) {
	return nil, errors.New("redis unavailable")
}

func (failingCache) SetMany(_ context.Context, _ map[string][]float32) error {
	return errors.New("redis unavailable")
}

func TestResolveReusesHitsAndEmbedsUniqueMisses(t *testing.T) {
	cache := New(nil)
	var batches [][]string
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		batches = append(batches, append([]string(nil), texts...))
		results := make([][]float32, len(texts))
		for i, text := range texts {
			results[i] = []float32{float32(len(text)), float32(len(text) * 10)}
		}
		return results, nil
	}

	first, stats, err := Resolve(
		context.Background(), cache, "tenant:7:model:a", 2,
		[]string{"alpha", "beta", "alpha"}, embed,
	)
	require.NoError(t, err)
	require.Equal(t, Stats{
		Inputs: 3, Unique: 2, Hits: 0, Misses: 2, ProviderInputs: 2,
		MissSamples: []MissSample{{Index: 0, Hash: "8ed3f6ad", Runes: 5}, {Index: 1, Hash: "f44e64e7", Runes: 4}},
	}, stats)
	require.Equal(t, [][]float32{{5, 50}, {4, 40}, {5, 50}}, first)
	require.Equal(t, [][]string{{"alpha", "beta"}}, batches,
		"duplicate content should be embedded once")

	second, stats, err := Resolve(
		context.Background(), cache, "tenant:7:model:a", 2,
		[]string{"alpha", "gamma"}, embed,
	)
	require.NoError(t, err)
	require.Equal(t, Stats{
		Inputs: 2, Unique: 2, Hits: 1, Misses: 1, ProviderInputs: 1,
		MissSamples: []MissSample{{Index: 1, Hash: "be9d587d", Runes: 5}},
	}, stats)
	require.Equal(t, [][]float32{{5, 50}, {5, 50}}, second)
	require.Equal(t, [][]string{{"alpha", "beta"}, {"gamma"}}, batches,
		"only the new content should reach the provider")
}

func TestResolveScopesEntriesByPrefix(t *testing.T) {
	cache := New(nil)
	calls := 0
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		calls++
		return [][]float32{{float32(len(texts[0])), 1}}, nil
	}

	_, _, err := Resolve(context.Background(), cache, "tenant:1:model:a", 2, []string{"private"}, embed)
	require.NoError(t, err)
	_, _, err = Resolve(context.Background(), cache, "tenant:2:model:a", 2, []string{"private"}, embed)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "different tenant prefixes must not share vectors")
}

func TestCachePersistsThroughRedisAcrossInstances(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	first := New(client)
	require.NoError(t, first.SetMany(ctx, map[string][]float32{"key": []float32{1, 2, 3}}))

	second := New(client)
	values, err := second.GetMany(ctx, []string{"key", "missing"})
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2, 3}, values["key"])
	_, found := values["missing"]
	require.False(t, found)
}

func TestResolveRecomputesWrongDimensionCacheEntry(t *testing.T) {
	cache := New(nil)
	require.NoError(t, cache.SetMany(context.Background(), map[string][]float32{
		"prefix:2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881": []float32{1},
	}))

	calls := 0
	results, stats, err := Resolve(
		context.Background(), cache, "prefix", 2, []string{"x"},
		func(_ context.Context, _ []string) ([][]float32, error) {
			calls++
			return [][]float32{{2, 3}}, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, stats.Misses)
	require.Equal(t, [][]float32{{2, 3}}, results)
}

func TestDisabledCacheAlwaysMisses(t *testing.T) {
	t.Setenv("WEKNORA_EMBEDDING_CACHE_ENABLED", "false")
	cache := New(nil)
	require.NoError(t, cache.SetMany(context.Background(), map[string][]float32{
		"key": []float32{1, 2},
	}))
	values, err := cache.GetMany(context.Background(), []string{"key"})
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestResolveFailsOpenWhenCacheBackendIsUnavailable(t *testing.T) {
	results, stats, err := Resolve(
		context.Background(), failingCache{}, "prefix", 2, []string{"alpha"},
		func(_ context.Context, _ []string) ([][]float32, error) {
			return [][]float32{{1, 2}}, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 2}}, results)
	require.Error(t, stats.ReadError)
	require.Error(t, stats.WriteError)
}

func TestResolveCoalescesConcurrentMisses(t *testing.T) {
	cache := New(nil)
	var providerCalls atomic.Int32
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		if providerCalls.Add(1) == 1 {
			close(providerStarted)
		}
		<-releaseProvider
		return [][]float32{{float32(len(texts[0])), 1}}, nil
	}

	const workers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, _, err := Resolve(context.Background(), cache, "concurrent", 2, []string{"same"}, embed)
			if err == nil && len(result) != 1 {
				err = errors.New("unexpected result length")
			}
			errs <- err
		}()
	}
	close(start)
	<-providerStarted
	// Keep the owner in flight long enough for peer goroutines to join the
	// per-key flight. This is bounded and does not affect production code.
	time.Sleep(25 * time.Millisecond)
	close(releaseProvider)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), providerCalls.Load())
}

func TestMemoryCacheHonorsTTL(t *testing.T) {
	cache := New(nil).(*hybridCache)
	now := time.Unix(1000, 0)
	cache.now = func() time.Time { return now }
	cache.ttl = time.Hour

	require.NoError(t, cache.SetMany(context.Background(), map[string][]float32{"key": {1, 2}}))
	values, err := cache.GetMany(context.Background(), []string{"key"})
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2}, values["key"])

	now = now.Add(time.Hour)
	values, err = cache.GetMany(context.Background(), []string{"key"})
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestMemoryCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := New(nil).(*hybridCache)
	cache.maxEntries = 2
	ctx := context.Background()
	require.NoError(t, cache.SetMany(ctx, map[string][]float32{
		"first":  {1},
		"second": {2},
	}))
	// Touch first so second becomes the least-recently-used entry.
	_, err := cache.GetMany(ctx, []string{"first"})
	require.NoError(t, err)
	require.NoError(t, cache.SetMany(ctx, map[string][]float32{"third": {3}}))

	values, err := cache.GetMany(ctx, []string{"first", "second", "third"})
	require.NoError(t, err)
	require.Contains(t, values, "first")
	require.NotContains(t, values, "second")
	require.Contains(t, values, "third")
}
