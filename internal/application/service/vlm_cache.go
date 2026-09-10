package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/redis/go-redis/v9"
)

// vlmCacheTTL is how long a cached OCR/caption result stays valid. Image bytes
// are immutable once uploaded, so the result is content-addressed and only
// needs eviction to bound Redis memory. 7 days is a reasonable compromise over
// "forever", which would let stale results live past model/prompt upgrades.
const vlmCacheTTL = 7 * 24 * time.Hour

// vlmCacheKeyPrefix namespaces VLM cache entries in Redis.
const vlmCacheKeyPrefix = "vlm"

// vlmCacheEntry is the serialized value stored in Redis. A single Predict call
// produces one result (OCR text or caption text, distinguished by the prompt
// hash in the key); the struct leaves room to grow to both fields later.
type vlmCacheEntry struct {
	Text string
}

// cachedVLM decorates a vlm.VLM with a Redis-backed cache keyed by the image
// bytes + model ID + prompt. Identical images reusing the same prompt (OCR vs
// caption, or across ingest runs) return the cached result instead of re-hitting
// the VLM provider. Cache failures only log a warning and fall through to the
// wrapped VLM, so a Redis outage never breaks image processing.
type cachedVLM struct {
	inner  vlm.VLM
	client *redis.Client
}

// newCachedVLM wraps inner with the Redis cache. It returns inner unchanged when
// either argument is nil (Lite mode has no Redis client), so callers can invoke
// it unconditionally.
func newCachedVLM(inner vlm.VLM, client *redis.Client) vlm.VLM {
	if inner == nil || client == nil {
		return inner
	}
	return &cachedVLM{inner: inner, client: client}
}

// vlmCacheKey derives the Redis key from the image bytes, model ID, and prompt.
// The prompt hash serves as the "prompt version": any change to the OCR/caption
// prompt (or its custom instructions) produces a different key and thus a fresh
// cache entry, while OCR vs caption naturally land in different slots.
func vlmCacheKey(modelID, prompt string, images [][]byte) string {
	imgHash := sha256.New()
	for _, img := range images {
		imgHash.Write(img)
	}
	promptSum := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%s:%x:%s:%x", vlmCacheKeyPrefix, imgHash.Sum(nil), modelID, promptSum[:])
}

// cacheGet returns the cached result for key. A miss (or any Redis error)
// returns ok=false; the caller falls back to the wrapped VLM.
func (c *cachedVLM) cacheGet(ctx context.Context, key string) (string, bool) {
	if c.client == nil {
		return "", false
	}
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			logger.GetLogger(ctx).Warnf("vlm cache get failed: %v", err)
		}
		return "", false
	}
	var entry vlmCacheEntry
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&entry); err != nil {
		logger.GetLogger(ctx).Warnf("vlm cache decode failed: %v", err)
		return "", false
	}
	return entry.Text, true
}

// cacheSet writes the result to the cache. Failures only log a warning so a
// Redis outage degrades to recomputing the result on the next request.
func (c *cachedVLM) cacheSet(ctx context.Context, key, text string) {
	if c.client == nil {
		return
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(vlmCacheEntry{Text: text}); err != nil {
		logger.GetLogger(ctx).Warnf("vlm cache encode failed: %v", err)
		return
	}
	if err := c.client.Set(ctx, key, buf.Bytes(), vlmCacheTTL).Err(); err != nil {
		logger.GetLogger(ctx).Warnf("vlm cache set failed: %v", err)
	}
}

// Predict satisfies vlm.VLM.
func (c *cachedVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	modelID := c.inner.GetModelID()
	if modelID == "" {
		// Legacy inline configs carry no model ID; fall back to the name so
		// distinct legacy models don't collide on an empty key segment.
		modelID = c.inner.GetModelName()
	}
	key := vlmCacheKey(modelID, prompt, imgBytes)
	if text, ok := c.cacheGet(ctx, key); ok {
		return text, nil
	}
	text, err := c.inner.Predict(ctx, imgBytes, prompt)
	if err != nil {
		return "", err
	}
	c.cacheSet(ctx, key, text)
	return text, nil
}

// GetModelName satisfies vlm.VLM.
func (c *cachedVLM) GetModelName() string { return c.inner.GetModelName() }

// GetModelID satisfies vlm.VLM.
func (c *cachedVLM) GetModelID() string { return c.inner.GetModelID() }
