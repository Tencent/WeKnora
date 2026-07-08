package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type cachedEmbedder struct {
	inner    embedding.Embedder
	cache    interfaces.ProcessingCacheRepository
	tenantID uint64
}

type embeddingCachePayload struct {
	Embedding []float32 `json:"embedding"`
}

func cacheEmbeddingModel(tenantID uint64, cache interfaces.ProcessingCacheRepository, inner embedding.Embedder) embedding.Embedder {
	if cache == nil || inner == nil {
		return inner
	}
	return &cachedEmbedder{
		inner:    inner,
		cache:    cache,
		tenantID: tenantID,
	}
}

func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := c.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, nil
	}
	return embeddings[0], nil
}

func (c *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return c.batchEmbedCached(ctx, texts, func(missing []string) ([][]float32, error) {
		return c.inner.BatchEmbed(ctx, missing)
	})
}

func (c *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return c.batchEmbedCached(ctx, texts, func(missing []string) ([][]float32, error) {
		return c.inner.BatchEmbedWithPool(ctx, c.inner, missing)
	})
}

func (c *cachedEmbedder) GetModelName() string {
	return c.inner.GetModelName()
}

func (c *cachedEmbedder) GetDimensions() int {
	return c.inner.GetDimensions()
}

func (c *cachedEmbedder) GetModelID() string {
	return c.inner.GetModelID()
}

func (c *cachedEmbedder) batchEmbedCached(
	ctx context.Context,
	texts []string,
	fetchMissing func([]string) ([][]float32, error),
) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	results := make([][]float32, len(texts))
	missingKeys := make([]string, 0, len(texts))
	missingTexts := make([]string, 0, len(texts))
	positionsByKey := make(map[string][]int)

	for i, text := range texts {
		key := c.embeddingCacheKey(text)
		if vec, ok := c.getCachedEmbedding(ctx, key); ok {
			results[i] = vec
			continue
		}
		positionsByKey[key] = append(positionsByKey[key], i)
		if len(positionsByKey[key]) == 1 {
			missingKeys = append(missingKeys, key)
			missingTexts = append(missingTexts, text)
		}
	}

	if len(missingTexts) == 0 {
		return results, nil
	}

	missingEmbeddings, err := fetchMissing(missingTexts)
	if err != nil {
		return nil, err
	}
	if len(missingEmbeddings) != len(missingTexts) {
		return nil, fmt.Errorf("embedding cache: got %d embeddings for %d missing texts", len(missingEmbeddings), len(missingTexts))
	}

	for i, key := range missingKeys {
		vec := copyFloat32s(missingEmbeddings[i])
		c.putCachedEmbedding(ctx, key, vec)
		for _, pos := range positionsByKey[key] {
			results[pos] = copyFloat32s(vec)
		}
	}
	return results, nil
}

func (c *cachedEmbedder) embeddingCacheKey(text string) string {
	modelID := strings.TrimSpace(c.inner.GetModelID())
	if modelID == "" {
		modelID = strings.TrimSpace(c.inner.GetModelName())
	}
	return processingCacheKey(
		normalizedContentHash(text),
		modelID,
		fmt.Sprintf("dim:%d", c.inner.GetDimensions()),
	)
}

func (c *cachedEmbedder) getCachedEmbedding(ctx context.Context, key string) ([]float32, bool) {
	row, err := c.cache.Get(ctx, c.tenantID, types.ProcessingCacheStageEmbedding, key)
	if err != nil {
		logger.Warnf(ctx, "embedding cache lookup failed: %v", err)
		return nil, false
	}
	if row == nil {
		return nil, false
	}
	var payload embeddingCachePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil || len(payload.Embedding) == 0 {
		if err != nil {
			logger.Warnf(ctx, "embedding cache payload invalid: %v", err)
		}
		return nil, false
	}
	if dim := c.inner.GetDimensions(); dim > 0 && len(payload.Embedding) != dim {
		return nil, false
	}
	return copyFloat32s(payload.Embedding), true
}

func (c *cachedEmbedder) putCachedEmbedding(ctx context.Context, key string, vec []float32) {
	if len(vec) == 0 {
		return
	}
	payloadBytes, err := json.Marshal(embeddingCachePayload{Embedding: vec})
	if err != nil {
		logger.Warnf(ctx, "embedding cache marshal failed: %v", err)
		return
	}
	metaBytes, _ := json.Marshal(map[string]string{
		"model_id":   c.inner.GetModelID(),
		"model_name": c.inner.GetModelName(),
		"dimensions": fmt.Sprintf("%d", c.inner.GetDimensions()),
	})
	if err := c.cache.Upsert(ctx, &types.ProcessingCache{
		TenantID: c.tenantID,
		Stage:    types.ProcessingCacheStageEmbedding,
		CacheKey: key,
		Payload:  types.JSON(payloadBytes),
		Metadata: types.JSON(metaBytes),
	}); err != nil {
		logger.Warnf(ctx, "embedding cache write failed: %v", err)
	}
}

func copyFloat32s(in []float32) []float32 {
	if in == nil {
		return nil
	}
	out := make([]float32, len(in))
	copy(out, in)
	return out
}
