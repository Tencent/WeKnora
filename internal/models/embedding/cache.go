package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
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
	group singleflight.Group
}

// NewCachedEmbedder wraps an Embedder with content-addressed embedding reuse.
func NewCachedEmbedder(inner Embedder, cache EmbeddingCache) Embedder {
	if inner == nil || cache == nil {
		return inner
	}
	return &cachedEmbedder{inner: inner, cache: cache}
}

func (e *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	normalizedText := contentcache.NormalizeText(text)
	key := e.cacheKey(normalizedText)
	if vec, ok := e.cachedVector(ctx, key); ok {
		return vec, nil
	}
	value, err, _ := e.group.Do(key, func() (any, error) {
		if vec, ok := e.cachedVector(ctx, key); ok {
			return vec, nil
		}
		vec, err := e.inner.Embed(ctx, normalizedText)
		if err != nil {
			return nil, err
		}
		if validEmbeddingVector(vec, e.inner.GetDimensions()) {
			e.cache.SetEmbedding(ctx, key, vec)
		}
		return vec, nil
	})
	if err != nil {
		return nil, err
	}
	return value.([]float32), nil
}

func (e *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.batchEmbed(ctx, texts, false)
}

func (e *cachedEmbedder) batchEmbed(ctx context.Context, texts []string, usePool bool) ([][]float32, error) {
	results := make([][]float32, len(texts))
	missTexts := make([]string, 0, len(texts))
	missKeys := make([]string, 0, len(texts))
	positionsByKey := map[string][]int{}

	for i, text := range texts {
		normalizedText := contentcache.NormalizeText(text)
		key := e.cacheKey(normalizedText)
		if vec, ok := e.cachedVector(ctx, key); ok {
			results[i] = vec
			continue
		}
		positionsByKey[key] = append(positionsByKey[key], i)
		if len(positionsByKey[key]) == 1 {
			missKeys = append(missKeys, key)
			missTexts = append(missTexts, normalizedText)
		}
	}

	if len(missTexts) == 0 {
		return results, nil
	}
	value, err, _ := e.group.Do(batchGroupKey(usePool, missKeys), func() (any, error) {
		vectorsByKey := make(map[string][]float32, len(missKeys))
		actualMissTexts := make([]string, 0, len(missTexts))
		actualMissKeys := make([]string, 0, len(missKeys))
		for i, key := range missKeys {
			if vec, ok := e.cachedVector(ctx, key); ok {
				vectorsByKey[key] = vec
				continue
			}
			actualMissKeys = append(actualMissKeys, key)
			actualMissTexts = append(actualMissTexts, missTexts[i])
		}
		if len(actualMissTexts) == 0 {
			return vectorsByKey, nil
		}

		var missVectors [][]float32
		var err error
		if usePool {
			missVectors, err = e.inner.BatchEmbedWithPool(ctx, e.inner, actualMissTexts)
		} else {
			missVectors, err = e.inner.BatchEmbed(ctx, actualMissTexts)
		}
		if err != nil {
			return nil, err
		}
		if len(missVectors) != len(actualMissTexts) {
			return nil, fmt.Errorf("embedding cache: expected %d vectors, got %d", len(actualMissTexts), len(missVectors))
		}
		for i, key := range actualMissKeys {
			vec := missVectors[i]
			if !validEmbeddingVector(vec, e.inner.GetDimensions()) {
				return nil, fmt.Errorf("embedding cache: invalid vector for key %s", key)
			}
			e.cache.SetEmbedding(ctx, key, vec)
			vectorsByKey[key] = vec
		}
		return vectorsByKey, nil
	})
	if err != nil {
		return nil, err
	}
	vectorsByKey := value.(map[string][]float32)
	for _, key := range missKeys {
		vec, ok := vectorsByKey[key]
		if !ok {
			return nil, fmt.Errorf("embedding cache: missing vector for key %s", key)
		}
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
	return e.batchEmbed(ctx, texts, true)
}

func (e *cachedEmbedder) cacheKey(normalizedText string) string {
	modelID := contentcache.TextHash(fmt.Sprintf("%T\x00%s\x00%s\x00%d",
		e.inner, e.inner.GetModelID(), e.inner.GetModelName(), e.inner.GetDimensions()))
	return contentcache.EmbeddingKey(contentcache.TextHash(normalizedText), modelID, e.inner.GetDimensions())
}

func (e *cachedEmbedder) cachedVector(ctx context.Context, key string) ([]float32, bool) {
	vec, ok := e.cache.GetEmbedding(ctx, key)
	if !ok || !validEmbeddingVector(vec, e.inner.GetDimensions()) {
		return nil, false
	}
	return vec, true
}

func validEmbeddingVector(vec []float32, dimensions int) bool {
	if len(vec) == 0 {
		return false
	}
	if dimensions > 0 && len(vec) != dimensions {
		return false
	}
	for _, v := range vec {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return false
		}
	}
	return true
}

func batchGroupKey(usePool bool, keys []string) string {
	mode := "batch"
	if usePool {
		mode = "pool"
	}
	return "embedding-" + mode + "\x00" + strings.Join(keys, "\x00")
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
	if cached.CachedAt <= 0 || !validEmbeddingVector(cached.Vector, 0) {
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
