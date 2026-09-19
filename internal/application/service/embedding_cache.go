package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/redis/go-redis/v9"
)

// embeddingCacheTTL is how long a cached embedding vector stays valid. It is
// deliberately generous: chunk text is immutable once ingested, so the vector
// only needs to be evicted to bound Redis memory, not for correctness.
const embeddingCacheTTL = 24 * time.Hour

// embeddingCacheKeyPrefix namespaces embedding cache entries in Redis.
const embeddingCacheKeyPrefix = "emb"

// cachedEmbedder decorates an embedding.Embedder with a Redis-backed cache keyed
// by the normalized input text + embedding model ID. Identical text within a
// batch or across ingest runs reuses the cached vector instead of re-hitting the
// embedding provider. Cache read/write failures only log a warning and fall
// through to the wrapped embedder, so a Redis outage can never break ingestion.
type cachedEmbedder struct {
	inner  embedding.Embedder
	client *redis.Client
}

// newCachedEmbedder wraps inner with the Redis cache. It returns inner unchanged
// when either argument is nil (Lite mode has no Redis client), so callers can
// invoke it unconditionally.
func newCachedEmbedder(inner embedding.Embedder, client *redis.Client) embedding.Embedder {
	if inner == nil || client == nil {
		return inner
	}
	return &cachedEmbedder{inner: inner, client: client}
}

// embeddingCacheKey derives the Redis key from the model ID and normalized text.
func embeddingCacheKey(modelID, text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return fmt.Sprintf("%s:%x:%s", embeddingCacheKeyPrefix, sum[:], modelID)
}

// cacheGet returns a cached vector for key. A miss (or any Redis error) returns
// ok=false; the caller falls back to the wrapped embedder.
func (c *cachedEmbedder) cacheGet(ctx context.Context, key string) ([]float32, bool) {
	if c.client == nil {
		return nil, false
	}
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			logger.GetLogger(ctx).Warnf("embedding cache get failed: %v", err)
		}
		return nil, false
	}
	var vec []float32
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&vec); err != nil {
		logger.GetLogger(ctx).Warnf("embedding cache decode failed: %v", err)
		return nil, false
	}
	return vec, true
}

// cacheSet writes vec to the cache. Failures only log a warning so a Redis
// outage degrades to recomputing the vector on the next request.
func (c *cachedEmbedder) cacheSet(ctx context.Context, key string, vec []float32) {
	if c.client == nil {
		return
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(vec); err != nil {
		logger.GetLogger(ctx).Warnf("embedding cache encode failed: %v", err)
		return
	}
	if err := c.client.Set(ctx, key, buf.Bytes(), embeddingCacheTTL).Err(); err != nil {
		logger.GetLogger(ctx).Warnf("embedding cache set failed: %v", err)
	}
}

// cachePartition holds the result of a cache lookup across a batch: hits are
// filled into results immediately, misses are collected (deduplicated by cache
// key) so the caller can compute them in a single inner call.
type cachePartition struct {
	results    [][]float32
	missTexts  []string
	missKeys   []string
	missPos    map[string]int // cache key -> index into missTexts
	resultMiss map[int]int    // original index -> index into missTexts
}

func (c *cachedEmbedder) partition(ctx context.Context, texts []string) *cachePartition {
	p := &cachePartition{
		results:    make([][]float32, len(texts)),
		missPos:    make(map[string]int),
		resultMiss: make(map[int]int),
	}
	modelID := c.inner.GetModelID()
	for i, text := range texts {
		key := embeddingCacheKey(modelID, text)
		if vec, ok := c.cacheGet(ctx, key); ok {
			p.results[i] = vec
			continue
		}
		pos, ok := p.missPos[key]
		if !ok {
			pos = len(p.missTexts)
			p.missTexts = append(p.missTexts, text)
			p.missKeys = append(p.missKeys, key)
			p.missPos[key] = pos
		}
		p.resultMiss[i] = pos
	}
	return p
}

// finish caches the computed miss vectors and folds them back into the results
// at their original positions.
func (c *cachedEmbedder) finish(ctx context.Context, p *cachePartition, computed [][]float32) {
	for j, vec := range computed {
		c.cacheSet(ctx, p.missKeys[j], vec)
	}
	for i, pos := range p.resultMiss {
		p.results[i] = computed[pos]
	}
}

// Embed satisfies embedding.Embedder.
func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := embeddingCacheKey(c.inner.GetModelID(), text)
	if vec, ok := c.cacheGet(ctx, key); ok {
		return vec, nil
	}
	vec, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	c.cacheSet(ctx, key, vec)
	return vec, nil
}

// BatchEmbed satisfies embedding.Embedder.
func (c *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	p := c.partition(ctx, texts)
	if len(p.missTexts) == 0 {
		return p.results, nil
	}
	computed, err := c.inner.BatchEmbed(ctx, p.missTexts)
	if err != nil {
		return nil, err
	}
	if len(computed) != len(p.missTexts) {
		return nil, fmt.Errorf("cached embedder: got %d embeddings for %d inputs", len(computed), len(p.missTexts))
	}
	c.finish(ctx, p, computed)
	return p.results, nil
}

// BatchEmbedWithPool satisfies embedding.EmbedderPooler. It resolves cache hits
// before delegating only the misses to the inner pooler, preserving the normal
// concurrency/gating/observability chain for real provider calls.
func (c *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	p := c.partition(ctx, texts)
	if len(p.missTexts) == 0 {
		return p.results, nil
	}
	computed, err := c.inner.BatchEmbedWithPool(ctx, c.inner, p.missTexts)
	if err != nil {
		return nil, err
	}
	if len(computed) != len(p.missTexts) {
		return nil, fmt.Errorf("cached embedder: got %d embeddings for %d inputs", len(computed), len(p.missTexts))
	}
	c.finish(ctx, p, computed)
	return p.results, nil
}

// GetModelName satisfies embedding.Embedder.
func (c *cachedEmbedder) GetModelName() string { return c.inner.GetModelName() }

// GetDimensions satisfies embedding.Embedder.
func (c *cachedEmbedder) GetDimensions() int { return c.inner.GetDimensions() }

// GetModelID satisfies embedding.Embedder.
func (c *cachedEmbedder) GetModelID() string { return c.inner.GetModelID() }
