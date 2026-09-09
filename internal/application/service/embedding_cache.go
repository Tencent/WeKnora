package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type cachedEmbedder struct {
	inner    embedding.Embedder
	cache    interfaces.EmbeddingCacheRepository
	usage    interfaces.ModelUsageRepository
	tenantID uint64
	modelKey string
	enabled  bool
	mu       sync.Mutex
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func embeddingModelKey(model *types.Model) string {
	b, _ := json.Marshal(struct {
		ID, Name, Provider, BaseURL string
		Dimensions, Truncate        int
		Extra                       map[string]string
	}{model.ID, model.Name, model.Parameters.Provider, model.Parameters.BaseURL,
		model.Parameters.EmbeddingParameters.Dimension, model.Parameters.EmbeddingParameters.TruncatePromptTokens,
		model.Parameters.ExtraConfig})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func embeddingCacheEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TOPIC3_EMBEDDING_CACHE_ENABLED")))
	return value != "0" && value != "false" && value != "off"
}

func wrapEmbeddingCache(inner embedding.Embedder, cache interfaces.EmbeddingCacheRepository, usage interfaces.ModelUsageRepository, tenantID uint64, model *types.Model) embedding.Embedder {
	if inner == nil || (cache == nil && usage == nil) {
		return inner
	}
	return &cachedEmbedder{
		inner: inner, cache: cache, usage: usage, tenantID: tenantID,
		modelKey: embeddingModelKey(model), enabled: cache != nil && embeddingCacheEnabled(),
	}
}

func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	rows, err := c.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("embedding cache returned %d vectors for one input", len(rows))
	}
	return rows[0], nil
}

func (c *cachedEmbedder) cachedBatch(ctx context.Context, texts []string, provider func([]string) ([][]float32, error)) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	if !c.enabled {
		vectors, err := provider(texts)
		if err != nil {
			c.recordUsage(ctx, len(texts), 0, 1, "failed")
			return nil, err
		}
		if err := c.validateVectors(vectors, len(texts)); err != nil {
			c.recordUsage(ctx, len(texts), 0, 1, "failed")
			return nil, err
		}
		c.recordUsage(ctx, len(texts), 0, 1, "success")
		return vectors, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	hashes := make([]string, len(texts))
	for i, text := range texts {
		hashes[i] = hashText(text)
	}
	hits, err := c.cache.Get(ctx, c.tenantID, c.modelKey, hashes)
	if err != nil {
		return nil, err
	}
	results := make([][]float32, len(texts))
	missingTexts := make([]string, 0)
	missingHashes := make([]string, 0)
	missingIndex := make(map[string]int)
	localHits := 0
	for i, hash := range hashes {
		if vector, ok := hits[hash]; ok {
			results[i] = vector
			localHits++
			continue
		}
		if _, ok := missingIndex[hash]; !ok {
			missingIndex[hash] = len(missingTexts)
			missingTexts = append(missingTexts, texts[i])
			missingHashes = append(missingHashes, hash)
		}
	}

	actualCalls := 0
	if len(missingTexts) > 0 {
		vectors, err := provider(missingTexts)
		actualCalls = 1
		if err != nil {
			return nil, err
		}
		if err := c.validateVectors(vectors, len(missingTexts)); err != nil {
			return nil, err
		}
		entries := make([]*types.EmbeddingCacheEntry, 0, len(vectors))
		for i, vector := range vectors {
			encoded, _ := json.Marshal(vector)
			entries = append(entries, &types.EmbeddingCacheEntry{TenantID: c.tenantID, ModelKey: c.modelKey, InputHash: missingHashes[i], Vector: types.JSON(encoded), Dimensions: len(vector)})
			hits[missingHashes[i]] = vector
		}
		if err := c.cache.Put(ctx, entries); err != nil {
			return nil, err
		}
	}
	for i, hash := range hashes {
		results[i] = hits[hash]
	}
	c.recordUsage(ctx, len(texts), localHits, actualCalls, "success")
	return results, nil
}

func (c *cachedEmbedder) validateVectors(vectors [][]float32, expected int) error {
	if len(vectors) != expected {
		return fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(vectors), expected)
	}
	for i, vector := range vectors {
		if len(vector) == 0 || (c.GetDimensions() > 0 && len(vector) != c.GetDimensions()) {
			return fmt.Errorf("invalid embedding dimensions for input %d: got %d want %d", i, len(vector), c.GetDimensions())
		}
	}
	return nil
}

func (c *cachedEmbedder) recordUsage(ctx context.Context, inputCount, localHits, actualCalls int, status string) {
	if c.usage == nil {
		return
	}
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	_ = c.usage.Create(context.WithoutCancel(ctx), &types.ModelUsage{
		TenantID: c.tenantID, Model: c.GetModelName(), CallType: "embedding", ActualCalls: actualCalls,
		Purpose: purpose, InputCount: inputCount, LocalCacheHits: localHits,
		CacheStatus: types.PromptCacheStatusUnsupported, Status: status,
	})
}

func (c *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return c.cachedBatch(ctx, texts, func(missing []string) ([][]float32, error) {
		return c.inner.BatchEmbed(ctx, missing)
	})
}

func (c *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return c.cachedBatch(ctx, texts, func(missing []string) ([][]float32, error) {
		return c.inner.BatchEmbedWithPool(ctx, c.inner, missing)
	})
}

func (c *cachedEmbedder) GetModelName() string { return c.inner.GetModelName() }
func (c *cachedEmbedder) GetModelID() string   { return c.inner.GetModelID() }
func (c *cachedEmbedder) GetDimensions() int   { return c.inner.GetDimensions() }
