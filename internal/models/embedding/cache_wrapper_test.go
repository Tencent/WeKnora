package embedding

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

// memCacheStore is an in-memory CacheStore for tests.
type memCacheStore struct {
	mu   sync.Mutex
	data map[string][]float32
}

func newMemCacheStore() *memCacheStore {
	return &memCacheStore{data: map[string][]float32{}}
}

func (m *memCacheStore) MGet(_ context.Context, keys []string) [][]float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]float32, len(keys))
	for i, k := range keys {
		if v, ok := m.data[k]; ok {
			out[i] = v
		}
	}
	return out
}

func (m *memCacheStore) MSet(_ context.Context, keys []string, vectors [][]float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, k := range keys {
		m.data[k] = vectors[i]
	}
}

// countingEmbedder returns a distinct vector per text and records every text
// that actually reached the "provider".
type countingEmbedder struct {
	id         string
	dims       int
	mu         sync.Mutex
	seenTexts  []string
	batchCalls int
}

func (c *countingEmbedder) vectorFor(text string) []float32 {
	return []float32{float32(len(text)), 1}
}

func (c *countingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seenTexts = append(c.seenTexts, text)
	return c.vectorFor(text), nil
}

func (c *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batchCalls++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		c.seenTexts = append(c.seenTexts, t)
		out[i] = c.vectorFor(t)
	}
	return out, nil
}

func (c *countingEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return model.BatchEmbed(ctx, texts)
}

func (c *countingEmbedder) GetModelName() string { return "counting" }
func (c *countingEmbedder) GetDimensions() int   { return c.dims }
func (c *countingEmbedder) GetModelID() string   { return c.id }

func (c *countingEmbedder) providerTexts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seenTexts...)
}

func TestCacheEmbedderBatchEmbedReusesCachedVectors(t *testing.T) {
	store := newMemCacheStore()
	inner := &countingEmbedder{id: "model-a", dims: 2}
	w := &cacheEmbedder{inner: inner, store: store}
	ctx := context.Background()

	texts := []string{"alpha", "beta", "gamma"}
	first, err := w.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("first BatchEmbed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 3 {
		t.Fatalf("expected 3 provider texts on cold cache, got %v", got)
	}

	// Second identical batch must be a full cache hit: zero provider calls.
	second, err := w.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("second BatchEmbed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 3 {
		t.Fatalf("rebuild must not re-call provider, provider saw %v", got)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached vectors differ: %v vs %v", first, second)
	}
}

func TestCacheEmbedderBatchEmbedPartialHitOnlySendsMisses(t *testing.T) {
	store := newMemCacheStore()
	inner := &countingEmbedder{id: "model-a", dims: 2}
	w := &cacheEmbedder{inner: inner, store: store}
	ctx := context.Background()

	if _, err := w.BatchEmbed(ctx, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("warm-up: %v", err)
	}

	results, err := w.BatchEmbed(ctx, []string{"alpha", "NEW", "beta"})
	if err != nil {
		t.Fatalf("partial batch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r == nil {
			t.Fatalf("result[%d] is nil", i)
		}
	}
	// Only "NEW" should have reached the provider on the second call.
	got := inner.providerTexts()
	if len(got) != 3 || got[2] != "NEW" {
		t.Fatalf("expected exactly one extra provider text NEW, provider saw %v", got)
	}
}

func TestCacheEmbedderDeduplicatesIdenticalTextsWithinBatch(t *testing.T) {
	store := newMemCacheStore()
	inner := &countingEmbedder{id: "model-a", dims: 2}
	w := &cacheEmbedder{inner: inner, store: store}

	results, err := w.BatchEmbed(context.Background(), []string{"dup", "dup", "dup"})
	if err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 1 {
		t.Fatalf("expected duplicates deduped to 1 provider text, saw %v", got)
	}
	if !reflect.DeepEqual(results[0], results[1]) || !reflect.DeepEqual(results[1], results[2]) {
		t.Fatalf("duplicate texts must share the same vector: %v", results)
	}
}

func TestCacheEmbedderKeyIsolatesModels(t *testing.T) {
	store := newMemCacheStore()
	innerA := &countingEmbedder{id: "model-a", dims: 2}
	innerB := &countingEmbedder{id: "model-b", dims: 2}
	wA := &cacheEmbedder{inner: innerA, store: store}
	wB := &cacheEmbedder{inner: innerB, store: store}
	ctx := context.Background()

	if _, err := wA.BatchEmbed(ctx, []string{"same text"}); err != nil {
		t.Fatalf("model A: %v", err)
	}
	// Same text, different model — must NOT hit model A's cache entry
	// (换 embedding 模型仅重算向量层).
	if _, err := wB.BatchEmbed(ctx, []string{"same text"}); err != nil {
		t.Fatalf("model B: %v", err)
	}
	if got := innerB.providerTexts(); len(got) != 1 {
		t.Fatalf("model B must compute its own vector, provider saw %v", got)
	}
}

func TestCacheEmbedderEmbedSingle(t *testing.T) {
	store := newMemCacheStore()
	inner := &countingEmbedder{id: "model-a", dims: 2}
	w := &cacheEmbedder{inner: inner, store: store}
	ctx := context.Background()

	v1, err := w.Embed(ctx, "solo")
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}
	v2, err := w.Embed(ctx, "solo")
	if err != nil {
		t.Fatalf("second Embed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 1 {
		t.Fatalf("second Embed must be a cache hit, provider saw %v", got)
	}
	if !reflect.DeepEqual(v1, v2) {
		t.Fatalf("cached vector differs: %v vs %v", v1, v2)
	}
}

func TestCacheEmbedderBatchEmbedWithPoolUsesCache(t *testing.T) {
	store := newMemCacheStore()
	inner := &countingEmbedder{id: "model-a", dims: 2}
	w := &cacheEmbedder{inner: inner, store: store}
	ctx := context.Background()

	if _, err := w.BatchEmbedWithPool(ctx, w, []string{"p1", "p2"}); err != nil {
		t.Fatalf("first pool batch: %v", err)
	}
	if _, err := w.BatchEmbedWithPool(ctx, w, []string{"p1", "p2"}); err != nil {
		t.Fatalf("second pool batch: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 2 {
		t.Fatalf("second pool batch must be fully cached, provider saw %v", got)
	}
}

func TestCacheEmbedderEmptyBatch(t *testing.T) {
	w := &cacheEmbedder{inner: &countingEmbedder{id: "m", dims: 2}, store: newMemCacheStore()}
	out, err := w.BatchEmbed(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty result, got %v", out)
	}
}

func TestEncodeDecodeVectorRoundTrip(t *testing.T) {
	vec := []float32{0, 1.5, -3.25, 1e-7, 42}
	got := decodeVector(encodeVector(vec))
	if !reflect.DeepEqual(vec, got) {
		t.Fatalf("round trip mismatch: %v vs %v", vec, got)
	}
	if decodeVector([]byte{1, 2, 3}) != nil {
		t.Fatal("malformed payload must decode to nil (cache miss)")
	}
	if decodeVector(nil) != nil {
		t.Fatal("empty payload must decode to nil (cache miss)")
	}
}
