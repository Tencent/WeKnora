package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// contentCache is the shared JSON wrapper around the content-addressed cache
// store. It is nil-safe: a service constructed without a cache repo (test
// harness / minimal wiring) behaves as a permanent cache miss, so caching is
// strictly additive and never changes behavior when the store is absent.
type contentCache struct {
	repo interfaces.ContentCacheRepository
}

// get loads the cached payload for key into out. Returns true on a hit.
// Corrupt payloads are treated as misses (they will be recomputed and
// overwritten on the next Set).
func (c *contentCache) get(ctx context.Context, key string, out any) (bool, error) {
	if c == nil || c.repo == nil {
		return false, nil
	}
	payload, found, err := c.repo.Get(ctx, key)
	if err != nil {
		logger.Warnf(ctx, "content cache get failed for %q: %v", key, err)
		return false, nil
	}
	if !found {
		return false, nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		logger.Warnf(ctx, "content cache payload corrupt for %q: %v (will recompute)", key, err)
		return false, nil
	}
	return true, nil
}

// set stores the JSON encoding of in under key. Best-effort: failures are
// logged and never propagated, so a broken cache cannot fail the pipeline.
func (c *contentCache) set(ctx context.Context, key, kind string, in any) {
	if c == nil || c.repo == nil {
		return
	}
	payload, err := json.Marshal(in)
	if err != nil {
		logger.Warnf(ctx, "content cache marshal failed for %q: %v", key, err)
		return
	}
	if err := c.repo.Set(ctx, key, kind, payload); err != nil {
		logger.Warnf(ctx, "content cache set failed for %q: %v", key, err)
	}
}

// cachingEmbedder wraps an embedding.Embedder with a content-addressed cache.
// Embedding is a pure function of (normalized text, model, dimensions), so two
// ingestion runs over the same text with the same model produce the same
// vector. The wrapper sits OUTSIDE the concurrency/debug/langfuse decorators
// (mirroring how the retrieve engine consumes an Embedder), and forwards
// BatchEmbedWithPool through so the pooler's per-sub-batch callbacks land on
// the caching BatchEmbed.
type cachingEmbedder struct {
	inner embedding.Embedder
	cache *contentCache
}

// embeddingCacheKey derives the cache key for a normalized embedding input.
// The model id and dimensions are part of the key so switching the embedding
// model invalidates exactly the vector layer (VLM/Wiki map caches survive).
func embeddingCacheKey(modelID string, dims int, text string) string {
	return types.ContentCacheKey(types.ContentCacheKindEmbedding,
		modelID, itoa(dims), text)
}

func (w *cachingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := embeddingCacheKey(w.inner.GetModelID(), w.inner.GetDimensions(), text)
	var cached []float32
	if hit, _ := w.cache.get(ctx, key, &cached); hit && len(cached) > 0 {
		return cached, nil
	}
	vec, err := w.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	w.cache.set(ctx, key, types.ContentCacheKindEmbedding, vec)
	return vec, nil
}

func (w *cachingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	results := make([][]float32, len(texts))
	missing := make([]int, 0, len(texts))
	modelID := w.inner.GetModelID()
	dims := w.inner.GetDimensions()
	for i, text := range texts {
		var cached []float32
		if hit, _ := w.cache.get(ctx, embeddingCacheKey(modelID, dims, text), &cached); hit && len(cached) > 0 {
			results[i] = cached
			continue
		}
		missing = append(missing, i)
	}
	if len(missing) == 0 {
		return results, nil
	}
	missTexts := make([]string, len(missing))
	for k, idx := range missing {
		missTexts[k] = texts[idx]
	}
	computed, err := w.inner.BatchEmbed(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	for k, idx := range missing {
		results[idx] = computed[k]
		w.cache.set(ctx, embeddingCacheKey(modelID, dims, texts[idx]), types.ContentCacheKindEmbedding, computed[k])
	}
	return results, nil
}

func (w *cachingEmbedder) BatchEmbedWithPool(ctx context.Context, model embedding.Embedder, texts []string) ([][]float32, error) {
	return w.inner.BatchEmbedWithPool(ctx, w, texts)
}

func (w *cachingEmbedder) GetModelName() string { return w.inner.GetModelName() }
func (w *cachingEmbedder) GetDimensions() int   { return w.inner.GetDimensions() }
func (w *cachingEmbedder) GetModelID() string   { return w.inner.GetModelID() }

// withEmbeddingCache wraps an embedder with the content-addressed cache when a
// cache store is available. nil inputs (no embedding model configured) pass
// through unchanged.
func (c *contentCache) wrapEmbedder(e embedding.Embedder) embedding.Embedder {
	if e == nil || c == nil || c.repo == nil {
		return e
	}
	return &cachingEmbedder{inner: e, cache: c}
}

// itoa is a small int formatter for cache key parts (avoids importing strconv
// in every call site of the key builders).
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// promptFingerprint hashes prompt text so edits to built-in or configured
// prompts automatically invalidate every cache layer that consumed them.
func promptFingerprint(parts ...string) string {
	return strings.Join(parts, "\x00")
}
