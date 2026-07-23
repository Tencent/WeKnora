package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
)

// CacheStore is the pluggable content-addressed embedding cache backend
// (issue #1679). Embedding is a pure function of (text, model, dimensions),
// so results can be reused across reparse/rebuild/crash-resume instead of
// re-billing the provider for unchanged content.
//
// Implementations must be best-effort and never fail the embedding call:
// a backend error is reported as a miss, never as an error.
type CacheStore interface {
	// MGet returns cached vectors for keys; result[i] == nil means miss.
	// The returned slice always has len(keys) entries.
	MGet(ctx context.Context, keys []string) [][]float32
	// MSet stores vectors under keys, best-effort (errors are swallowed).
	MSet(ctx context.Context, keys []string, vectors [][]float32)
}

// globalCacheStore is installed once at container startup (Redis mode only).
// Mirrors the process-wide singleton pattern used by langfuse.GetManager()
// and limiter.SetGovernor(). nil = caching disabled (Lite mode / tests).
var globalCacheStore CacheStore

// SetCacheStore installs the process-wide embedding cache backend.
// Call once during startup, before any embedder is constructed.
func SetCacheStore(store CacheStore) { globalCacheStore = store }

// wrapEmbeddingCache decorates e with the content-addressed cache when a
// backend is installed. Sits OUTERMOST in the decorator chain: cache hits
// short-circuit before the concurrency gate and debug/langfuse tracing, so a
// fully cached batch costs zero provider round-trips and zero gate waits.
func wrapEmbeddingCache(e Embedder) Embedder {
	if e == nil || globalCacheStore == nil {
		return e
	}
	return &cacheEmbedder{inner: e, store: globalCacheStore}
}

// cacheEmbedder is a caching decorator around an Embedder. Keys embed the
// model ID and dimensions so switching the embedding model (or its dimension
// override) precisely invalidates only the embedding layer — OCR/caption and
// wiki caches keyed on other inputs stay live (统一失效策略).
type cacheEmbedder struct {
	inner Embedder
	store CacheStore
}

// cacheKey derives the content-addressed key for one text:
// hash(text) + embedding model ID + dimensions.
func (w *cacheEmbedder) cacheKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%s:%d:%s", w.inner.GetModelID(), w.inner.GetDimensions(), hex.EncodeToString(sum[:]))
}

func (w *cacheEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := w.cacheKey(text)
	if hits := w.store.MGet(ctx, []string{key}); len(hits) == 1 && hits[0] != nil {
		return hits[0], nil
	}
	vec, err := w.inner.Embed(ctx, text)
	if err == nil && len(vec) > 0 {
		w.store.MSet(ctx, []string{key}, [][]float32{vec})
	}
	return vec, err
}

func (w *cacheEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return w.embedBatch(ctx, texts, func(ctx context.Context, miss []string) ([][]float32, error) {
		return w.inner.BatchEmbed(ctx, miss)
	})
}

// BatchEmbedWithPool resolves cache hits first and only fans the misses out
// through the pooler. The inner embedder is threaded down as the model so
// per-sub-batch callbacks do not re-check the cache (they are known misses).
func (w *cacheEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ Embedder, texts []string,
) ([][]float32, error) {
	return w.embedBatch(ctx, texts, func(ctx context.Context, miss []string) ([][]float32, error) {
		return w.inner.BatchEmbedWithPool(ctx, w.inner, miss)
	})
}

// embedBatch is the shared hit/miss merge logic. Duplicate texts inside one
// batch are deduplicated before hitting the provider (跨文档相同块去重 applies
// naturally since the key is content-addressed and shared store-wide).
func (w *cacheEmbedder) embedBatch(
	ctx context.Context,
	texts []string,
	fill func(ctx context.Context, miss []string) ([][]float32, error),
) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	keys := make([]string, len(texts))
	for i, t := range texts {
		keys[i] = w.cacheKey(t)
	}
	hits := w.store.MGet(ctx, keys)

	results := make([][]float32, len(texts))
	missIndexes := make(map[string][]int) // key -> positions in texts
	var missKeys []string
	var missTexts []string
	hitCount := 0
	for i := range texts {
		if i < len(hits) && hits[i] != nil {
			results[i] = hits[i]
			hitCount++
			continue
		}
		key := keys[i]
		if _, seen := missIndexes[key]; !seen {
			missKeys = append(missKeys, key)
			missTexts = append(missTexts, texts[i])
		}
		missIndexes[key] = append(missIndexes[key], i)
	}

	if len(missTexts) == 0 {
		logger.GetLogger(ctx).Debugf(
			"[EmbeddingCache] full hit: model=%s texts=%d", w.inner.GetModelID(), len(texts))
		return results, nil
	}
	if hitCount > 0 {
		logger.GetLogger(ctx).Debugf(
			"[EmbeddingCache] partial hit: model=%s hits=%d misses=%d",
			w.inner.GetModelID(), hitCount, len(missTexts))
	}

	vectors, err := fill(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(missTexts) {
		return nil, fmt.Errorf(
			"embedding cache: provider returned %d vectors for %d texts", len(vectors), len(missTexts))
	}

	storeKeys := make([]string, 0, len(missKeys))
	storeVectors := make([][]float32, 0, len(missKeys))
	for j, key := range missKeys {
		for _, i := range missIndexes[key] {
			results[i] = vectors[j]
		}
		if len(vectors[j]) > 0 {
			storeKeys = append(storeKeys, key)
			storeVectors = append(storeVectors, vectors[j])
		}
	}
	if len(storeKeys) > 0 {
		w.store.MSet(ctx, storeKeys, storeVectors)
	}
	return results, nil
}

func (w *cacheEmbedder) GetModelName() string { return w.inner.GetModelName() }
func (w *cacheEmbedder) GetDimensions() int   { return w.inner.GetDimensions() }
func (w *cacheEmbedder) GetModelID() string   { return w.inner.GetModelID() }
