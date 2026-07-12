package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/redis/go-redis/v9"
)

const embeddingCacheVersion = "v1"

type cachedEmbedder struct {
	inner embedding.Embedder
	redis *redis.Client
}

func cacheEmbeddingModel(client *redis.Client, inner embedding.Embedder) embedding.Embedder {
	if client == nil || inner == nil {
		return inner
	}
	return &cachedEmbedder{inner: inner, redis: client}
}

func (e *cachedEmbedder) cacheKey(text string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return "weknora:artifact:embedding:" + embeddingCacheVersion + ":" + artifactModelKey(e.GetModelID(), e.GetModelName()) + ":" +
		strconv.Itoa(e.GetDimensions()) + ":" + hex.EncodeToString(sum[:])
}

func (e *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	items, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

func (e *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	keys := make([]string, len(texts))
	for i, text := range texts {
		keys[i] = e.cacheKey(text)
	}
	values, cacheErr := e.redis.MGet(ctx, keys...).Result()
	results := make([][]float32, len(texts))
	missTexts := make([]string, 0, len(texts))
	missIndexes := make([]int, 0, len(texts))
	for i := range texts {
		if cacheErr == nil && values[i] != nil {
			if raw, ok := values[i].(string); ok && json.Unmarshal([]byte(raw), &results[i]) == nil && len(results[i]) > 0 {
				continue
			}
		}
		missTexts = append(missTexts, texts[i])
		missIndexes = append(missIndexes, i)
	}
	if len(missTexts) == 0 {
		return results, nil
	}
	computed, err := e.inner.BatchEmbed(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	if len(computed) != len(missIndexes) {
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(computed), len(missIndexes))
	}
	pipe := e.redis.Pipeline()
	for i, vector := range computed {
		idx := missIndexes[i]
		results[idx] = vector
		if encoded, marshalErr := json.Marshal(vector); marshalErr == nil {
			pipe.Set(ctx, keys[idx], encoded, artifactCacheTTL)
		}
	}
	_, _ = pipe.Exec(ctx)
	return results, nil
}

func (e *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *cachedEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *cachedEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *cachedEmbedder) GetModelID() string   { return e.inner.GetModelID() }
