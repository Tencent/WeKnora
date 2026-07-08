package embedding

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type fakeEmbeddingCache struct {
	values map[string][]float32
}

func newFakeEmbeddingCache() *fakeEmbeddingCache {
	return &fakeEmbeddingCache{values: map[string][]float32{}}
}

func (c *fakeEmbeddingCache) GetEmbedding(_ context.Context, key string) ([]float32, bool) {
	vec, ok := c.values[key]
	return vec, ok
}

func (c *fakeEmbeddingCache) SetEmbedding(_ context.Context, key string, vector []float32) {
	c.values[key] = vector
}

type countingEmbedder struct {
	calls []string
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
	return model.BatchEmbed(ctx, texts)
}

func (e *countingEmbedder) GetModelName() string { return "fake-model" }
func (e *countingEmbedder) GetDimensions() int   { return 1 }
func (e *countingEmbedder) GetModelID() string   { return "fake-model-id" }

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
	if !reflect.DeepEqual(inner.calls, []string{" alpha\r\n"}) {
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
