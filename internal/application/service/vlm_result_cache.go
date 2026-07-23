package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	// vlmCacheKeyPrefix versions the entry format; bump if it ever changes.
	vlmCacheKeyPrefix = "weknora:cache:vlm:v1:"
	// defaultVLMCacheTTL: VLM OCR/caption is the single most expensive
	// per-image call, and its input (rendered image bytes) is fully
	// deterministic — keep results long enough to cover reparse cycles.
	defaultVLMCacheTTL = 30 * 24 * time.Hour
)

// vlmResultCache caches VLM OCR/caption text keyed by
// (image bytes hash, VLM model, prompt) — the exact "纯函数" inputs from
// issue #1679. A hit freezes the cached text as the canonical parse result,
// isolating VLM output nondeterminism at the source so downstream
// content-addressed layers (chunk IDs, embeddings, wiki maps) stay hittable.
//
// nil-receiver and nil-client safe: every method degrades to a miss/no-op,
// so Lite mode (no Redis) runs exactly as before.
type vlmResultCache struct {
	client *redis.Client
	ttl    time.Duration
}

// newVLMResultCache builds the cache. TTL is overridable via
// VLM_CACHE_TTL_DAYS (<=0 keeps the default). Safe to call with nil client.
func newVLMResultCache(client *redis.Client) *vlmResultCache {
	if client == nil {
		return nil
	}
	ttl := defaultVLMCacheTTL
	if v := os.Getenv("VLM_CACHE_TTL_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			ttl = time.Duration(days) * 24 * time.Hour
		}
	}
	return &vlmResultCache{client: client, ttl: ttl}
}

// Get returns (text, true) on a cache hit. An empty cached string is a valid
// hit: it freezes "this image has no extractable text/caption" so the VLM is
// not re-consulted for a known-empty result.
func (c *vlmResultCache) Get(ctx context.Context, key string) (string, bool) {
	if c == nil || c.client == nil {
		return "", false
	}
	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			// Backend trouble degrades to a miss — never fails the task.
			return "", false
		}
		return "", false
	}
	return val, true
}

// Set stores the canonical text, best-effort.
func (c *vlmResultCache) Set(ctx context.Context, key, text string) {
	if c == nil || c.client == nil {
		return
	}
	_ = c.client.Set(ctx, key, text, c.ttl).Err()
}

// vlmCacheKey derives the content-addressed key for one VLM call:
// hash(image bytes) + model identity + hash(full prompt). The prompt hash
// embeds language/custom-instruction/prompt-version changes, giving precise
// layered invalidation (统一失效策略): changing the caption language only
// invalidates captions, never OCR or embeddings.
func vlmCacheKey(imageHash, modelKey, prompt string) string {
	promptSum := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%s%s:%s:%s", vlmCacheKeyPrefix, modelKey, imageHash, hex.EncodeToString(promptSum[:8]))
}

// vlmModelCacheKey identifies the VLM model for cache keying. New-style
// configs use the DB model ID; legacy inline configs derive a stable
// fingerprint from their routing fields (API keys excluded — rotating a key
// does not change model output).
func vlmModelCacheKey(cfg types.VLMConfig) string {
	if id := strings.TrimSpace(cfg.ModelID); id != "" {
		return id
	}
	sum := sha256.Sum256([]byte(cfg.BaseURL + "|" + cfg.ModelName + "|" + cfg.InterfaceType))
	return "legacy-" + hex.EncodeToString(sum[:8])
}

// hashImageBytes returns the hex SHA-256 of the raw image bytes — the
// content-addressed identity of the image, independent of its storage URL.
func hashImageBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
