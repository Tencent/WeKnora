package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding/vectorcache"
	"github.com/redis/go-redis/v9"
)

const embeddingCacheKeyVersion = "v1"

type VectorCache = vectorcache.Cache

func NewVectorCache(redisClient *redis.Client) VectorCache {
	return vectorcache.New(redisClient)
}

// ModelFingerprint scopes cache entries to every non-secret setting that can
// change embedding output. Rotating an API key therefore preserves cache hits,
// while changing provider/model/base URL/dimension or provider-specific config
// invalidates only the affected model namespace.
func ModelFingerprint(config Config) string {
	payload := struct {
		Source                    string            `json:"source"`
		BaseURL                   string            `json:"base_url"`
		ModelName                 string            `json:"model_name"`
		ModelID                   string            `json:"model_id"`
		Provider                  string            `json:"provider"`
		AppID                     string            `json:"app_id,omitempty"`
		Dimensions                int               `json:"dimensions"`
		TruncatePromptTokens      int               `json:"truncate_prompt_tokens"`
		SupportsDimensionOverride bool              `json:"supports_dimension_override"`
		ExtraConfig               map[string]string `json:"extra_config,omitempty"`
		CustomHeaders             map[string]string `json:"custom_headers,omitempty"`
	}{
		Source:                    string(config.Source),
		BaseURL:                   config.BaseURL,
		ModelName:                 config.ModelName,
		ModelID:                   config.ModelID,
		Provider:                  config.Provider,
		AppID:                     config.AppID,
		Dimensions:                config.Dimensions,
		TruncatePromptTokens:      config.TruncatePromptTokens,
		SupportsDimensionOverride: config.SupportsDimensionOverride,
		ExtraConfig:               config.ExtraConfig,
		CustomHeaders:             config.CustomHeaders,
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// cachedEmbedder wraps a provider embedder with content-addressed reuse.
type cachedEmbedder struct {
	inner      Embedder
	cache      VectorCache
	keyPrefix  string
	dimensions int
}

func NewCachedEmbedder(inner Embedder, cache VectorCache, tenantID uint64, modelFingerprint string) Embedder {
	if inner == nil || cache == nil {
		return inner
	}
	return &cachedEmbedder{
		inner:      inner,
		cache:      cache,
		keyPrefix:  fmt.Sprintf("weknora:embedding:%s:%d:%s", embeddingCacheKeyVersion, tenantID, modelFingerprint),
		dimensions: inner.GetDimensions(),
	}
}

func (e *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, _, err := e.resolve(ctx, []string{text}, e.inner.BatchEmbed)
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

func (e *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	results, _, err := e.resolve(ctx, texts, e.inner.BatchEmbed)
	return results, err
}

func (e *cachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	results, _, err := e.resolve(ctx, texts, func(ctx context.Context, misses []string) ([][]float32, error) {
		return e.inner.BatchEmbedWithPool(ctx, e.inner, misses)
	})
	return results, err
}

func (e *cachedEmbedder) resolve(
	ctx context.Context, texts []string, embed vectorcache.EmbedFunc,
) ([][]float32, vectorcache.Stats, error) {
	startedAt := time.Now()
	results, stats, err := vectorcache.Resolve(ctx, e.cache, e.keyPrefix, e.dimensions, texts, embed)
	if stats.ReadError != nil {
		logger.Warnf(ctx, "[EmbeddingCache] read failed, continuing with provider misses: %v", stats.ReadError)
	}
	if stats.WriteError != nil {
		logger.Warnf(ctx, "[EmbeddingCache] write failed: %v", stats.WriteError)
	}
	if err == nil {
		logger.Infof(ctx, "[EmbeddingCache] model=%s inputs=%d unique=%d hits=%d misses=%d provider_inputs=%d coalesced=%d miss_samples=%v elapsed=%s",
			e.GetModelID(), stats.Inputs, stats.Unique, stats.Hits, stats.Misses,
			stats.ProviderInputs, stats.Coalesced, stats.MissSamples,
			time.Since(startedAt).Round(time.Millisecond))
	}
	return results, stats, err
}

func (e *cachedEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *cachedEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *cachedEmbedder) GetModelID() string   { return e.inner.GetModelID() }
