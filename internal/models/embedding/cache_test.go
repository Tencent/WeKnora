package embedding

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type fakeEmbeddingCache struct {
	mu     sync.RWMutex
	values map[string][]float32
}

func newFakeEmbeddingCache() *fakeEmbeddingCache {
	return &fakeEmbeddingCache{values: map[string][]float32{}}
}

func (c *fakeEmbeddingCache) GetEmbedding(_ context.Context, key string) ([]float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	vec, ok := c.values[key]
	return vec, ok
}

func (c *fakeEmbeddingCache) SetEmbedding(_ context.Context, key string, vector []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = vector
}

type countingEmbedder struct {
	calls     []string
	poolCalls int
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	result, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return result[0], nil
}

func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		e.calls = append(e.calls, text)
		out = append(out, []float32{float32(len(text))})
	}
	return out, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	e.poolCalls++
	return e.BatchEmbed(ctx, texts)
}

func (e *countingEmbedder) GetModelName() string { return "fake-model" }
func (e *countingEmbedder) GetDimensions() int   { return 1 }
func (e *countingEmbedder) GetModelID() string   { return "fake-model-id" }

type modelMetaEmbedder struct {
	calls  []string
	id     string
	name   string
	dim    int
	value  float32
	pooler Embedder
}

func (e *modelMetaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	result, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return result[0], nil
}

func (e *modelMetaEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		e.calls = append(e.calls, text)
		vec := make([]float32, e.dim)
		for i := range vec {
			vec[i] = e.value
		}
		out = append(out, vec)
	}
	return out, nil
}

func (e *modelMetaEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *modelMetaEmbedder) GetModelName() string { return e.name }
func (e *modelMetaEmbedder) GetDimensions() int   { return e.dim }
func (e *modelMetaEmbedder) GetModelID() string   { return e.id }

type alternateMetaEmbedder struct {
	modelMetaEmbedder
}

func (e *alternateMetaEmbedder) GetModelName() string { return e.modelMetaEmbedder.GetModelName() }
func (e *alternateMetaEmbedder) GetDimensions() int   { return e.modelMetaEmbedder.GetDimensions() }
func (e *alternateMetaEmbedder) GetModelID() string   { return e.modelMetaEmbedder.GetModelID() }

func TestCachedEmbedderReusesSingleEmbedding(t *testing.T) {
	ctx := context.Background()
	inner := &countingEmbedder{}
	cached := NewCachedEmbedder(inner, newFakeEmbeddingCache())

	first, err := cached.Embed(ctx, " alpha\r\n")
	if err != nil {
		t.Fatalf("Embed first error: %v", err)
	}
	second, err := cached.Embed(ctx, "alpha\n")
	if err != nil {
		t.Fatalf("Embed second error: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached vector mismatch: got %v want %v", second, first)
	}
	if !reflect.DeepEqual(inner.calls, []string{"alpha"}) {
		t.Fatalf("inner calls = %v", inner.calls)
	}
}

func TestCachedEmbedderBatchUsesPartialHitsAndDedupesMisses(t *testing.T) {
	ctx := context.Background()
	inner := &countingEmbedder{}
	cached := NewCachedEmbedder(inner, newFakeEmbeddingCache())

	_, err := cached.BatchEmbed(ctx, []string{"one", "two", "one"})
	if err != nil {
		t.Fatalf("BatchEmbed first error: %v", err)
	}
	if !reflect.DeepEqual(inner.calls, []string{"one", "two"}) {
		t.Fatalf("first inner calls = %v", inner.calls)
	}

	inner.calls = nil
	got, err := cached.BatchEmbed(ctx, []string{"two", "three", "one", "three"})
	if err != nil {
		t.Fatalf("BatchEmbed second error: %v", err)
	}
	if !reflect.DeepEqual(got, [][]float32{{3}, {5}, {3}, {5}}) {
		t.Fatalf("second vectors = %v", got)
	}
	if !reflect.DeepEqual(inner.calls, []string{"three"}) {
		t.Fatalf("second inner calls = %v", inner.calls)
	}
}

func TestCachedEmbedderBatchEmbedWithPoolUsesCache(t *testing.T) {
	ctx := context.Background()
	inner := &countingEmbedder{}
	cached := NewCachedEmbedder(inner, newFakeEmbeddingCache())

	_, err := cached.BatchEmbedWithPool(ctx, cached, []string{"cached"})
	if err != nil {
		t.Fatalf("BatchEmbedWithPool first error: %v", err)
	}
	_, err = cached.BatchEmbedWithPool(ctx, cached, []string{"cached"})
	if err != nil {
		t.Fatalf("BatchEmbedWithPool second error: %v", err)
	}

	if !reflect.DeepEqual(inner.calls, []string{"cached"}) {
		t.Fatalf("inner calls = %v", inner.calls)
	}
	if inner.poolCalls != 1 {
		t.Fatalf("pool calls = %d", inner.poolCalls)
	}
}

func TestCachedEmbedderRejectsCorruptCachedVector(t *testing.T) {
	ctx := context.Background()
	inner := &countingEmbedder{}
	cache := newFakeEmbeddingCache()
	cached := NewCachedEmbedder(inner, cache)
	key := cached.(*cachedEmbedder).cacheKey("bad")
	cache.SetEmbedding(ctx, key, []float32{1, 2})

	got, err := cached.Embed(ctx, "bad")
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if !reflect.DeepEqual(got, []float32{3}) {
		t.Fatalf("vector = %v", got)
	}
	if !reflect.DeepEqual(inner.calls, []string{"bad"}) {
		t.Fatalf("inner calls = %v", inner.calls)
	}
	if !reflect.DeepEqual(cache.values[key], []float32{3}) {
		t.Fatalf("cache value = %v", cache.values[key])
	}
}

func TestRedisEmbeddingCacheRejectsMissingTimestampAndEmptyVectors(t *testing.T) {
	ctx := context.Background()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer server.Close()

	cache := NewRedisEmbeddingCache(redis.NewClient(&redis.Options{Addr: server.Addr()}))

	server.Set("missing-timestamp", `{"vector":[1]}`)
	if _, ok := cache.GetEmbedding(ctx, "missing-timestamp"); ok {
		t.Fatalf("missing timestamp cache entry was accepted")
	}

	server.Set("empty-vector", `{"vector":[],"cached_at":1}`)
	if _, ok := cache.GetEmbedding(ctx, "empty-vector"); ok {
		t.Fatalf("empty vector cache entry was accepted")
	}

	server.Set("valid", `{"vector":[1],"cached_at":1}`)
	got, ok := cache.GetEmbedding(ctx, "valid")
	if !ok || !reflect.DeepEqual(got, []float32{1}) {
		t.Fatalf("valid vector = %v, ok=%v", got, ok)
	}
}

func TestCachedEmbedderReusesRedisCacheAcrossWrapperRebuild(t *testing.T) {
	ctx := context.Background()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()

	firstInner := &modelMetaEmbedder{id: "id", name: "name", dim: 1, value: 7}
	first := NewCachedEmbedder(firstInner, NewRedisEmbeddingCache(client))
	got, err := first.Embed(ctx, "persist me")
	if err != nil {
		t.Fatalf("first Embed error: %v", err)
	}
	if !reflect.DeepEqual(got, []float32{7}) || len(firstInner.calls) != 1 {
		t.Fatalf("first vector=%v calls=%v", got, firstInner.calls)
	}

	secondInner := &modelMetaEmbedder{id: "id", name: "name", dim: 1, value: 9}
	second := NewCachedEmbedder(secondInner, NewRedisEmbeddingCache(client))
	got, err = second.Embed(ctx, "persist me")
	if err != nil {
		t.Fatalf("second Embed error: %v", err)
	}
	if !reflect.DeepEqual(got, []float32{7}) {
		t.Fatalf("rebuilt wrapper vector = %v", got)
	}
	if len(secondInner.calls) != 0 {
		t.Fatalf("rebuilt wrapper called inner: %v", secondInner.calls)
	}
}

func TestCachedEmbedderInvalidatesOnConcreteModelMetadata(t *testing.T) {
	ctx := context.Background()
	cache := newFakeEmbeddingCache()

	baseInner := &modelMetaEmbedder{id: "same-id", name: "name-a", dim: 1, value: 1}
	base := NewCachedEmbedder(baseInner, cache)
	if _, err := base.Embed(ctx, "same text"); err != nil {
		t.Fatalf("base Embed error: %v", err)
	}

	nameChanged := &modelMetaEmbedder{id: "same-id", name: "name-b", dim: 1, value: 2}
	if got, err := NewCachedEmbedder(nameChanged, cache).Embed(ctx, "same text"); err != nil {
		t.Fatalf("name changed Embed error: %v", err)
	} else if !reflect.DeepEqual(got, []float32{2}) || len(nameChanged.calls) != 1 {
		t.Fatalf("name changed vector=%v calls=%v", got, nameChanged.calls)
	}

	typeChanged := &alternateMetaEmbedder{modelMetaEmbedder{id: "same-id", name: "name-a", dim: 1, value: 3}}
	if got, err := NewCachedEmbedder(typeChanged, cache).Embed(ctx, "same text"); err != nil {
		t.Fatalf("type changed Embed error: %v", err)
	} else if !reflect.DeepEqual(got, []float32{3}) || len(typeChanged.calls) != 1 {
		t.Fatalf("type changed vector=%v calls=%v", got, typeChanged.calls)
	}
}

type blockingEmbedder struct {
	calls   atomic.Int32
	ready   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *blockingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	e.once.Do(func() { close(e.ready) })
	<-e.release
	return []float32{float32(len(text))}, nil
}

func (e *blockingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vec, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	return out, nil
}

func (e *blockingEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *blockingEmbedder) GetModelName() string { return "blocking" }
func (e *blockingEmbedder) GetDimensions() int   { return 1 }
func (e *blockingEmbedder) GetModelID() string   { return "blocking-id" }

func TestCachedEmbedderSingleflightsConcurrentSameKey(t *testing.T) {
	ctx := context.Background()
	inner := &blockingEmbedder{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
	cached := NewCachedEmbedder(inner, newFakeEmbeddingCache())

	const goroutines = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := cached.Embed(ctx, "same concurrent text")
			if err != nil {
				errs <- err
				return
			}
			if !reflect.DeepEqual(got, []float32{20}) {
				errs <- fmt.Errorf("vector = %v", got)
			}
		}()
	}
	close(start)
	<-inner.ready
	time.Sleep(20 * time.Millisecond)
	close(inner.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls := inner.calls.Load(); calls != 1 {
		t.Fatalf("inner calls = %d, want 1", calls)
	}
}

func TestCachedEmbedderSingleflightsConcurrentBatchMissSet(t *testing.T) {
	ctx := context.Background()
	inner := &blockingEmbedder{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
	cached := NewCachedEmbedder(inner, newFakeEmbeddingCache())

	const goroutines = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := cached.BatchEmbed(ctx, []string{"same batch text"})
			if err != nil {
				errs <- err
				return
			}
			if !reflect.DeepEqual(got, [][]float32{{15}}) {
				errs <- fmt.Errorf("vectors = %v", got)
			}
		}()
	}
	close(start)
	<-inner.ready
	time.Sleep(20 * time.Millisecond)
	close(inner.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls := inner.calls.Load(); calls != 1 {
		t.Fatalf("inner calls = %d, want 1", calls)
	}
}

func TestCachedEmbedderFallsBackWithoutCache(t *testing.T) {
	ctx := context.Background()
	inner := &countingEmbedder{}
	cached := NewCachedEmbedder(inner, nil)

	_, err := cached.BatchEmbed(ctx, []string{"x", "x"})
	if err != nil {
		t.Fatalf("BatchEmbed without cache error: %v", err)
	}

	if !reflect.DeepEqual(inner.calls, []string{"x", "x"}) {
		t.Fatalf("inner calls = %v", inner.calls)
	}
	if cached.GetModelName() != "fake-model" {
		t.Fatalf("model name = %q", cached.GetModelName())
	}
	if cached.GetDimensions() != 1 {
		t.Fatalf("dimensions = %d", cached.GetDimensions())
	}
	if cached.GetModelID() != "fake-model-id" {
		t.Fatalf("model id = %q", cached.GetModelID())
	}
}

func TestCachedEmbedderPropagatesInnerErrors(t *testing.T) {
	ctx := context.Background()
	errBoom := fmt.Errorf("boom")
	inner := errorEmbedder{err: errBoom}
	cached := NewCachedEmbedder(inner, newFakeEmbeddingCache())

	_, err := cached.Embed(ctx, "x")
	if !errors.Is(err, errBoom) {
		t.Fatalf("Embed error = %v, want %v", err, errBoom)
	}
}

type errorEmbedder struct {
	err error
}

func (e errorEmbedder) Embed(context.Context, string) ([]float32, error) { return nil, e.err }
func (e errorEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, e.err
}
func (e errorEmbedder) BatchEmbedWithPool(context.Context, Embedder, []string) ([][]float32, error) {
	return nil, e.err
}
func (e errorEmbedder) GetModelName() string { return "error" }
func (e errorEmbedder) GetDimensions() int   { return 1 }
func (e errorEmbedder) GetModelID() string   { return "error-id" }
