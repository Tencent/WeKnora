package embedding

import (
	"context"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// CacheScope scopes embedding cache entries to an isolation boundary.
type CacheScope struct {
	TenantID uint64
}

type EmbeddingCacheKey struct {
	TenantID   uint64
	ModelID    string
	ModelName  string
	Dimension  int
	TextHash   string
}

// EmbeddingCache stores vectors by deterministic embedding input keys.
type EmbeddingCache interface {
	Get(ctx context.Context, key EmbeddingCacheKey) ([]float32, bool, error)
	Set(ctx context.Context, key EmbeddingCacheKey, vector []float32) error
}

// MemoryEmbeddingCache is a process-local cache useful for tests and lite mode.
type MemoryEmbeddingCache struct {
	mu   sync.RWMutex
	data map[EmbeddingCacheKey][]float32
}

func NewMemoryEmbeddingCache() *MemoryEmbeddingCache {
	return &MemoryEmbeddingCache{data: make(map[EmbeddingCacheKey][]float32)}
}

func (c *MemoryEmbeddingCache) Get(ctx context.Context, key EmbeddingCacheKey) ([]float32, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	vector, ok := c.data[key]
	if !ok {
		return nil, false, nil
	}
	return cloneVector(vector), true, nil
}

func (c *MemoryEmbeddingCache) Set(ctx context.Context, key EmbeddingCacheKey, vector []float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = cloneVector(vector)
	return nil
}

// CachedEmbedder wraps an Embedder and reuses vectors for identical normalized inputs.
type CachedEmbedder struct {
	base  Embedder
	cache EmbeddingCache
	scope CacheScope
}

func NewCachedEmbedder(base Embedder, cache EmbeddingCache, scope CacheScope) *CachedEmbedder {
	return &CachedEmbedder{base: base, cache: cache, scope: scope}
}

func (e *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("cached embedder returned no vector")
	}
	return vectors[0], nil
}

func (e *CachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if e == nil || e.base == nil {
		return nil, fmt.Errorf("cached embedder requires a base embedder")
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	if e.cache == nil {
		return e.base.BatchEmbed(ctx, texts)
	}

	out := make([][]float32, len(texts))
	keys := make([]EmbeddingCacheKey, len(texts))
	missSeen := make(map[EmbeddingCacheKey]int)
	var missTexts []string
	var missKeys []EmbeddingCacheKey
	for i, text := range texts {
		key := e.cacheKey(text)
		keys[i] = key
		if key.TextHash == "" {
			out[i] = []float32{}
			continue
		}
		if vector, ok, err := e.cache.Get(ctx, key); err != nil {
			return nil, err
		} else if ok {
			out[i] = vector
			continue
		}
		if _, ok := missSeen[key]; ok {
			continue
		}
		missSeen[key] = len(missTexts)
		missKeys = append(missKeys, key)
		missTexts = append(missTexts, types.NormalizeContentForIdentity(text))
	}

	if len(missTexts) > 0 {
		missVectors, err := e.base.BatchEmbed(ctx, missTexts)
		if err != nil {
			return nil, err
		}
		if len(missVectors) != len(missTexts) {
			return nil, fmt.Errorf("embedding result count %d does not match input count %d", len(missVectors), len(missTexts))
		}
		for i, key := range missKeys {
			if err := e.cache.Set(ctx, key, missVectors[i]); err != nil {
				return nil, err
			}
		}
	}

	for i, key := range keys {
		if out[i] != nil || key.TextHash == "" {
			continue
		}
		vector, ok, err := e.cache.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("embedding cache miss after set")
		}
		out[i] = vector
	}

	return out, nil
}

func (e *CachedEmbedder) GetModelName() string {
	return e.base.GetModelName()
}

func (e *CachedEmbedder) GetDimensions() int {
	return e.base.GetDimensions()
}

func (e *CachedEmbedder) GetModelID() string {
	return e.base.GetModelID()
}

func (e *CachedEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *CachedEmbedder) cacheKey(text string) EmbeddingCacheKey {
	return EmbeddingCacheKey{
		TenantID:  e.scope.TenantID,
		ModelID:   e.base.GetModelID(),
		ModelName: e.base.GetModelName(),
		Dimension: e.base.GetDimensions(),
		TextHash:  types.StableContentHash(text),
	}
}

func cloneVector(vector []float32) []float32 {
	if vector == nil {
		return nil
	}
	out := make([]float32, len(vector))
	copy(out, vector)
	return out
}
