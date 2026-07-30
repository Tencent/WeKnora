package inferencecache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestResolveReusesSuccessfulOutput(t *testing.T) {
	cache := New(nil)
	var calls int
	loader := func(context.Context) ([]byte, error) {
		calls++
		return []byte("validated output"), nil
	}

	first, stats, err := cache.Resolve(context.Background(), "key", loader)
	require.NoError(t, err)
	require.False(t, stats.Hit)
	require.Equal(t, []byte("validated output"), first)

	second, stats, err := cache.Resolve(context.Background(), "key", loader)
	require.NoError(t, err)
	require.True(t, stats.Hit)
	require.Equal(t, first, second)
	require.Equal(t, 1, calls)
}

func TestResolveCoalescesConcurrentLoaders(t *testing.T) {
	cache := New(nil)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte("one result"), nil
	}

	const workers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, _, err := cache.Resolve(context.Background(), "shared", loader)
			if err == nil && string(value) != "one result" {
				err = fmt.Errorf("unexpected result %q", value)
			}
			errs <- err
		}()
	}
	close(start)
	<-started
	time.Sleep(25 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), calls.Load())
}

func TestCachePersistsInRedisAndExpires(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	t.Setenv("WEKNORA_INFERENCE_CACHE_TTL", "1h")

	first := New(client)
	_, _, err := first.Resolve(context.Background(), "persistent", func(context.Context) ([]byte, error) {
		return []byte("cached"), nil
	})
	require.NoError(t, err)

	second := New(client)
	value, stats, err := second.Resolve(context.Background(), "persistent", func(context.Context) ([]byte, error) {
		return []byte("unexpected"), nil
	})
	require.NoError(t, err)
	require.True(t, stats.Hit)
	require.Equal(t, []byte("cached"), value)

	server.FastForward(time.Hour)
	third := New(client)
	value, stats, err = third.Resolve(context.Background(), "persistent", func(context.Context) ([]byte, error) {
		return []byte("fresh"), nil
	})
	require.NoError(t, err)
	require.False(t, stats.Hit)
	require.Equal(t, []byte("fresh"), value)
}

func TestMemoryCacheHonorsTTLAndLRUCapacity(t *testing.T) {
	cache := New(nil).(*hybridCache)
	now := time.Unix(1000, 0)
	cache.now = func() time.Time { return now }
	cache.ttl = time.Hour
	cache.maxEntries = 2
	ctx := context.Background()

	load := func(value string) Loader {
		return func(context.Context) ([]byte, error) { return []byte(value), nil }
	}
	_, _, _ = cache.Resolve(ctx, "first", load("1"))
	_, _, _ = cache.Resolve(ctx, "second", load("2"))
	_, _, _ = cache.Resolve(ctx, "first", load("unexpected")) // touch first
	_, _, _ = cache.Resolve(ctx, "third", load("3"))

	_, stats, _ := cache.Resolve(ctx, "second", load("2-new"))
	require.False(t, stats.Hit, "least-recently-used entry should be evicted")

	now = now.Add(time.Hour)
	_, stats, _ = cache.Resolve(ctx, "first", load("1-new"))
	require.False(t, stats.Hit, "expired in-memory entry should be recomputed")
}

func TestKeySeparatesPartsAndTenants(t *testing.T) {
	fingerprint := Fingerprint(map[string]string{"model": "a"})
	require.NotEqual(t,
		Key("wiki", 1, fingerprint, []byte("ab"), []byte("c")),
		Key("wiki", 1, fingerprint, []byte("a"), []byte("bc")),
	)
	require.NotEqual(t,
		Key("wiki", 1, fingerprint, []byte("same")),
		Key("wiki", 2, fingerprint, []byte("same")),
	)
}

func TestLoaderErrorsAreNotCached(t *testing.T) {
	cache := New(nil)
	calls := 0
	loader := func(context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary provider failure")
		}
		return []byte("recovered"), nil
	}
	_, _, err := cache.Resolve(context.Background(), "retry", loader)
	require.Error(t, err)
	value, _, err := cache.Resolve(context.Background(), "retry", loader)
	require.NoError(t, err)
	require.Equal(t, []byte("recovered"), value)
	require.Equal(t, 2, calls)
}

func TestResolveJSONRepairsCorruptEntry(t *testing.T) {
	cache := New(nil).(*hybridCache)
	require.NoError(t, cache.set(context.Background(), "json", []byte("not-json")))
	type payload struct {
		Value string `json:"value"`
	}
	calls := 0
	value, stats, err := ResolveJSON(context.Background(), cache, "json", func(context.Context) (payload, error) {
		calls++
		return payload{Value: "fresh"}, nil
	})
	require.NoError(t, err)
	require.Error(t, stats.ReadError)
	require.Equal(t, payload{Value: "fresh"}, value)
	require.Equal(t, 1, calls)
}
