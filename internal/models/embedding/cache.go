package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/redis/go-redis/v9"
)

const embeddingCacheTTL = 30 * 24 * time.Hour

// EmbeddingCache is the storage seam used by CachedEmbedder.
type EmbeddingCache interface {
	GetEmbedding(ctx context.Context, key string) ([]float32, bool)
	SetEmbedding(ctx context.Context, key string, vector []float32)
}

type cachedEmbedder struct {
	inner Embedder
	cache EmbeddingCache
}

// NewCachedEmbedder wraps an Embedder with content-addressed embedding reuse.
func NewCachedEmbedder(inner Embedder, cache EmbeddingCache) Embedder {
	if inner == nil || cache == nil {
		return inner
	}
	return &cachedEmbedder{inner: inner, cache: cache}
}

func (e *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := e.cacheKey(text)
	if vec, ok := e.cache.GetEmbedding(ctx, key); ok {
		return vec, nil
	}
	vec, err := e.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	e.cache.SetEmbedding(ctx, key, vec)
	return vec, nil
}

func (e *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	missTexts := make([]string, 0, len(texts))
	missKeys := make([]string, 0, len(texts))
	positionsByKey := map[string][]int{}

	for i, text := range texts {
		key := e.cacheKey(text)
		if vec, ok := e.cache.GetEmbedding(ctx, key); ok {
			results[i] = vec
			continue
		}
		positionsByKey[key] = append(positionsByKey[key], i)
		if len(positionsByKey[key]) == 1 {
			missKeys = append(missKeys, key)
			missTexts = append(missTexts, text)
		}
	}

	if len(missTexts) == 0 {
		return results, nil
	}
	missVectors, err := e.inner.BatchEmbed(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	if len(missVectors) != len(missTexts) {
		return nil, fmt.Errorf("embedding cache: expected %d vectors, got %d", len(missTexts), len(missVectors))
	}
	for i, key := range missKeys {
		vec := missVectors[i]
		e.cache.SetEmbedding(ctx, key, vec)
		for _, pos := range positionsByKey[key] {
			results[pos] = vec
		}
	}
	return results, nil
}

func (e *cachedEmbedder) GetModelName() string {
	return e.inner.GetModelName()
}

func (e *cachedEmbedder) GetDimensions() int {
	return e.inner.GetDimensions()
}

func (e *cachedEmbedder) GetModelID() string {
	return e.inner.GetModelID()
}

func (e *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *cachedEmbedder) cacheKey(text string) string {
	modelID := e.inner.GetModelID()
	if modelID == "" {
		modelID = e.inner.GetModelName()
	}
	return contentcache.EmbeddingKey(contentcache.TextHash(text), modelID, e.inner.GetDimensions())
}

type redisEmbeddingCache struct {
	client *redis.Client
	ttl    time.Duration
}

type cachedEmbeddingVector struct {
	Vector   []float32 `json:"vector"`
	CachedAt int64     `json:"cached_at"`
}

// NewRedisEmbeddingCache adapts Redis to the EmbeddingCache seam.
func NewRedisEmbeddingCache(client *redis.Client) EmbeddingCache {
	if client == nil {
		return nil
	}
	return &redisEmbeddingCache{client: client, ttl: embeddingCacheTTL}
}

func (c *redisEmbeddingCache) GetEmbedding(ctx context.Context, key string) ([]float32, bool) {
	data, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil || err != nil {
		return nil, false
	}
	var cached cachedEmbeddingVector
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, false
	}
	return cached.Vector, true
}

func (c *redisEmbeddingCache) SetEmbedding(ctx context.Context, key string, vector []float32) {
	data, err := json.Marshal(cachedEmbeddingVector{
		Vector:   vector,
		CachedAt: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	_ = c.client.Set(ctx, key, data, c.ttl).Err()
}
