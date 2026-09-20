package embedding

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/panjf2000/ants/v2"
	"github.com/redis/go-redis/v9"
)

type cacheBatchProvider struct {
	*cacheTestEmbedder
	batch func(context.Context, []string) ([][]float32, error)
}

func (p *cacheBatchProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return p.batch(ctx, texts)
}

func TestResultCachePooledMissesKeepConcurrencyLimit(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	limiter.SetGovernor(limiter.NewLocalLimiter(), 1)
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	pool, err := ants.NewPool(4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	provider := newFakeEmbedder("cached-pool")
	provider.pooler = NewBatchEmbedder(pool)
	inner := &langfuseEmbedder{inner: &debugEmbedder{inner: wrapEmbeddingConcurrency(provider, 1)}}
	cache := newMemoryResultCache(8)
	config := testCacheConfig()
	config.Dimensions = provider.GetDimensions()
	wrapped := WrapResultCache(inner, cache, config, 42)
	ctx := types.WithBackgroundTask(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, callErr := wrapped.BatchEmbedWithPool(ctx, wrapped, []string{"a", "b", "a", "c"})
		finished <- callErr
	}()
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(provider.release) }) })
	select {
	case <-provider.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("provider was not reached")
	}
	select {
	case <-provider.enter:
		t.Fatal("cache bypassed the per-model concurrency limit")
	case <-time.After(50 * time.Millisecond):
	}
	release.Do(func() { close(provider.release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&provider.maxSeen); got != 1 {
		t.Fatalf("maximum provider concurrency = %d, want 1", got)
	}
	if _, err := wrapped.BatchEmbedWithPool(ctx, wrapped, []string{"a", "b", "a", "c"}); err != nil {
		t.Fatal(err)
	}
	metrics := CacheMetrics(cache)
	if metrics.ProviderItems != 3 || metrics.HitItems != 4 || metrics.Requests != 2 {
		t.Fatalf("unexpected pooled reuse: %+v", metrics)
	}
}

func TestResultCacheBatchPublishesBeforeUnlock(t *testing.T) {
	server := miniredis.RunT(t)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	provider := &cacheBatchProvider{cacheTestEmbedder: &cacheTestEmbedder{}}
	provider.batch = func(context.Context, []string) ([][]float32, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return [][]float32{{1, 2}, {3, 4}}, nil
	}
	models := make([]Embedder, 2)
	for i := range models {
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		models[i] = WrapResultCache(provider, &redisResultCache{client: client}, testCacheConfig(), 42)
	}
	results := make(chan error, 2)
	go func() {
		_, err := models[0].BatchEmbed(context.Background(), []string{"a", "bb"})
		results <- err
	}()
	<-entered
	go func() {
		_, err := models[1].BatchEmbed(context.Background(), []string{"a", "bb"})
		results <- err
	}()
	unblock.Do(func() { close(release) })
	for range models {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider batch calls across clients = %d, want 1", got)
	}
}

type cacheFilledOnRecheck struct {
	ResultCache
	reads int
}

func (c *cacheFilledOnRecheck) GetMany(_ context.Context, keys []string) (map[string][]float32, error) {
	c.reads++
	if c.reads == 2 {
		return map[string][]float32{keys[0]: {9, 1}}, nil
	}
	return map[string][]float32{}, nil
}

func (c *cacheFilledOnRecheck) SetMany(ctx context.Context, entries map[string][]float32, ttl time.Duration) error {
	for key, vector := range entries {
		if err := c.Set(ctx, key, vector, ttl); err != nil {
			return err
		}
	}
	return nil
}

func TestResultCacheBatchRechecksOnlyRemainingMisses(t *testing.T) {
	provider := &cacheTestEmbedder{}
	cache := &cacheFilledOnRecheck{ResultCache: newMemoryResultCache(4)}
	wrapped := WrapResultCache(provider, cache, testCacheConfig(), 42)
	got, err := wrapped.BatchEmbed(context.Background(), []string{"a", "bb", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(provider.batchInputs[0], []string{"bb"}) || got[0][0] != 9 || got[2][0] != 9 {
		t.Fatalf("recheck did not preserve newly cached values: inputs=%v vectors=%v", provider.batchInputs, got)
	}
}

func TestResultCacheInvalidProviderBatchesAreNotCached(t *testing.T) {
	for _, vectors := range [][][]float32{nil, {{1}}, {{float32(math.NaN()), 2}}, {{1, float32(math.Inf(1))}}} {
		cache := newMemoryResultCache(4)
		var calls int
		provider := &cacheBatchProvider{cacheTestEmbedder: &cacheTestEmbedder{}}
		provider.batch = func(context.Context, []string) ([][]float32, error) {
			calls++
			return vectors, nil
		}
		wrapped := WrapResultCache(provider, cache, testCacheConfig(), 42)
		for range 2 {
			_, _ = wrapped.BatchEmbed(context.Background(), []string{"bad"})
		}
		if calls != 2 {
			t.Fatalf("invalid provider vectors were cached: %v", vectors)
		}
	}
}

func TestResultCacheBatchHitVectorsDoNotAlias(t *testing.T) {
	wrapped := WrapResultCache(&cacheTestEmbedder{}, newMemoryResultCache(4), testCacheConfig(), 42)
	if _, err := wrapped.BatchEmbed(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	got, err := wrapped.BatchEmbed(context.Background(), []string{"a", "a"})
	if err != nil {
		t.Fatal(err)
	}
	got[0][0] = 99
	if got[1][0] == 99 {
		t.Fatal("duplicate result positions share a mutable vector")
	}
}

func TestResultCacheCanceledBatchCannotPopulateCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cache := newMemoryResultCache(4)
	provider := &cacheBatchProvider{cacheTestEmbedder: &cacheTestEmbedder{}}
	done := make(chan struct{})
	provider.batch = func(context.Context, []string) ([][]float32, error) {
		defer close(done)
		cancel()
		return [][]float32{{1, 2}}, nil
	}
	wrapped := WrapResultCache(provider, cache, testCacheConfig(), 42)
	_, _ = wrapped.BatchEmbed(ctx, []string{"cancel"})
	<-done
	_, _, _ = wrapped.(*resultCacheEmbedder).group.Do(
		batchCacheKey(42, CacheFingerprint(testCacheConfig()), []string{"cancel"}),
		func() (interface{}, error) { return nil, nil },
	)
	key := CacheKey(42, CacheFingerprint(testCacheConfig()), "cancel")
	if _, ok, _ := cache.Get(context.Background(), key); ok {
		t.Fatal("canceled request populated the cache")
	}
}

func TestEmbeddingRedisLockReleaseFailurePreservesResult(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	got, err := withEmbeddingRedisLock(context.Background(), client, "release-test",
		time.Minute, time.Second, time.Minute, func(context.Context) (interface{}, error) {
			server.SetError("injected release failure")
			return "provider result", nil
		})
	if err != nil || got != "provider result" {
		t.Fatalf("lock failure replaced provider result: result=%v error=%v", got, err)
	}
}

func TestResultCacheCanceledWaiterReturnsPromptly(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	provider := &cacheBatchProvider{cacheTestEmbedder: &cacheTestEmbedder{}}
	provider.batch = func(context.Context, []string) ([][]float32, error) {
		close(entered)
		<-release
		return [][]float32{{1, 2}}, nil
	}
	wrapped := WrapResultCache(provider, newMemoryResultCache(4), testCacheConfig(), 42)
	leaderDone := make(chan error, 1)
	go func() {
		_, err := wrapped.BatchEmbed(context.Background(), []string{"same"})
		leaderDone <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	waiterDone := make(chan error, 1)
	go func() {
		_, err := wrapped.BatchEmbed(ctx, []string{"same"})
		waiterDone <- err
	}()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiter error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter remained blocked on the provider")
	}
	unblock.Do(func() { close(release) })
	if err := <-leaderDone; err != nil {
		t.Fatal(err)
	}
}

func TestResultCacheLockLossDoesNotCancelProvider(t *testing.T) {
	t.Setenv("WEKNORA_EMBEDDING_CACHE_LOCK_RENEWAL", "5ms")
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &redisResultCache{client: client}
	key := batchCacheKey(42, CacheFingerprint(testCacheConfig()), []string{"lock loss"})
	provider := &cacheBatchProvider{cacheTestEmbedder: &cacheTestEmbedder{}}
	provider.batch = func(ctx context.Context, _ []string) ([][]float32, error) {
		server.Del(key + ":lock")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return [][]float32{{1, 2}}, nil
		}
	}
	wrapped := WrapResultCache(provider, cache, testCacheConfig(), 42)
	got, err := wrapped.BatchEmbed(context.Background(), []string{"lock loss"})
	if err != nil || len(got) != 1 {
		t.Fatalf("cache lock loss interrupted provider: result=%v error=%v", got, err)
	}
}
