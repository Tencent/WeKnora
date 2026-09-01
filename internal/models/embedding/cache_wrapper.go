package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Tencent/WeKnora/internal/types"
)

// cachedEmbedder reuses embedding vectors for identical inputs.
type cachedEmbedder struct {
	inner     Embedder
	cache     EmbeddingCache
	tenantID  uint64
	pooler    EmbedderPooler
	modelID   string
	modelName string
}

func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := c.keyFor(ctx, text)
	if vector, ok, err := c.cache.Get(ctx, &key); err == nil && ok {
		recordCacheHit(c.modelID, c.modelName)
		_ = c.cache.IncrementHit(ctx, &key)
		return vector, nil
	}
	recordCacheMiss(c.modelID, c.modelName)
	vector, err := c.inner.Embed(ctx, text)
	recordProviderCall(c.modelID, c.modelName)
	if err == nil {
		_ = c.cache.Set(ctx, &key, vector)
	}
	return vector, err
}

func (c *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	keys := make([]types.EmbeddingCacheKey, len(texts))
	results := make([][]float32, len(texts))
	missingIndexes := make([]int, 0, len(texts))
	missingTexts := make([]string, 0, len(texts))

	for i, text := range texts {
		key := c.keyFor(ctx, text)
		keys[i] = key
		if vector, ok, err := c.cache.Get(ctx, &key); err == nil && ok {
			recordCacheHit(c.modelID, c.modelName)
			_ = c.cache.IncrementHit(ctx, &key)
			results[i] = vector
			continue
		}
		recordCacheMiss(c.modelID, c.modelName)
		missingIndexes = append(missingIndexes, i)
		missingTexts = append(missingTexts, text)
	}
	if len(missingTexts) == 0 {
		return results, nil
	}

	vectors, err := c.inner.BatchEmbed(ctx, missingTexts)
	recordProviderCall(c.modelID, c.modelName)
	if err != nil {
		return nil, err
	}
	for j, index := range missingIndexes {
		results[index] = vectors[j]
		_ = c.cache.Set(ctx, &keys[index], vectors[j])
	}
	return results, nil
}

func (c *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	if c.pooler == nil {
		return c.inner.BatchEmbedWithPool(ctx, c, texts)
	}
	return c.pooler.BatchEmbedWithPool(ctx, c, texts)
}

func (c *cachedEmbedder) GetModelName() string { return c.inner.GetModelName() }
func (c *cachedEmbedder) GetDimensions() int   { return c.inner.GetDimensions() }
func (c *cachedEmbedder) GetModelID() string   { return c.inner.GetModelID() }

func (c *cachedEmbedder) keyFor(ctx context.Context, text string) types.EmbeddingCacheKey {
	tenantID := c.tenantID
	if t, ok := types.TenantIDFromContext(ctx); ok && t > 0 {
		tenantID = t
	}
	sum := sha256.Sum256([]byte(text))
	return types.EmbeddingCacheKey{
		TenantID:  tenantID,
		ModelID:   c.inner.GetModelID(),
		Dimension: c.inner.GetDimensions(),
		TextHash:  hex.EncodeToString(sum[:]),
	}
}

func wrapEmbeddingCache(e Embedder, tenantID uint64, pooler EmbedderPooler) Embedder {
	cache := GetEmbeddingCache()
	if cache == nil || e == nil {
		return e
	}
	return &cachedEmbedder{
		inner:     e,
		cache:     cache,
		tenantID:  tenantID,
		pooler:    pooler,
		modelID:   e.GetModelID(),
		modelName: e.GetModelName(),
	}
}
