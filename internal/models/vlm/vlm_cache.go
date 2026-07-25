package vlm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"gorm.io/gorm"
)

// VLMCacheEntry represents a row in the vlm_cache table.
type VLMCacheEntry struct {
	CacheKey    string    `gorm:"column:cache_key;primaryKey"`
	ModelID     string    `gorm:"column:model_id"`
	ImageHash   string    `gorm:"column:image_hash"`
	PromptHash  string    `gorm:"column:prompt_hash"`
	Result      string    `gorm:"column:result"`
	TextPreview string    `gorm:"column:text_preview"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (VLMCacheEntry) TableName() string {
	return "vlm_cache"
}

// VLMCache provides a persistent cache for VLM (OCR/Caption) results.
// Cache key = SHA256(image_bytes_hash + prompt + model_id).
// The image hash isolates non-determinism at the source: same image bytes → same cache key.
type VLMCache struct {
	db *gorm.DB
}

// NewVLMCache creates a new VLMCache using the given GORM DB.
// If db is nil, the cache is disabled (all lookups are misses, no writes).
func NewVLMCache(db *gorm.DB) *VLMCache {
	c := &VLMCache{db: db}
	if db != nil {
		go c.sweepOldEntries()
	}
	return c
}

// sweepOldEntries deletes cache entries older than 90 days.
func (c *VLMCache) sweepOldEntries() {
	ctx := context.Background()
	cutoff := time.Now().AddDate(0, 0, -90)
	result := c.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&VLMCacheEntry{})
	if result.RowsAffected > 0 {
		logger.Infof(ctx, "[VLMCache] Swept %d entries older than 90 days", result.RowsAffected)
	}
}

// vlmCacheKey computes the cache key for a given image + prompt + model.
func vlmCacheKey(imgBytes [][]byte, prompt, modelID string) string {
	h := sha256.New()
	// Hash all image bytes
	for _, img := range imgBytes {
		ih := sha256.Sum256(img)
		h.Write(ih[:])
	}
	h.Write([]byte{0})
	h.Write([]byte(prompt))
	h.Write([]byte{0})
	h.Write([]byte(modelID))
	return hex.EncodeToString(h.Sum(nil))
}

// Lookup retrieves a cached VLM response.
func (c *VLMCache) Lookup(ctx context.Context, imgBytes [][]byte, prompt, modelID string) (string, bool) {
	if c == nil || c.db == nil || len(imgBytes) == 0 {
		return "", false
	}
	key := vlmCacheKey(imgBytes, prompt, modelID)
	var entry VLMCacheEntry
	if err := c.db.WithContext(ctx).Where("cache_key = ?", key).First(&entry).Error; err != nil {
		return "", false
	}
	logger.Infof(ctx, "[VLMCache] HIT key=%s model=%s", key[:16], modelID)
	return entry.Result, true
}

// Store saves a VLM response to the cache.
func (c *VLMCache) Store(ctx context.Context, imgBytes [][]byte, prompt, modelID, result string) {
	if c == nil || c.db == nil || len(imgBytes) == 0 || result == "" {
		return
	}
	key := vlmCacheKey(imgBytes, prompt, modelID)

	// Compute image hash for debugging
	imgHash := ""
	if len(imgBytes) > 0 {
		ih := sha256.Sum256(imgBytes[0])
		imgHash = hex.EncodeToString(ih[:])[:16]
	}

	// Prompt hash
	ph := sha256.Sum256([]byte(prompt))
	promptHash := hex.EncodeToString(ph[:])[:16]

	// Preview from result
	preview := result
	if runes := []rune(preview); len(runes) > 100 {
		preview = string(runes[:100])
	}

	entry := VLMCacheEntry{
		CacheKey:    key,
		ModelID:     modelID,
		ImageHash:   imgHash,
		PromptHash:  promptHash,
		Result:      result,
		TextPreview: preview,
		UpdatedAt:   time.Now(),
	}

	if err := c.db.WithContext(ctx).Save(&entry).Error; err != nil {
		logger.Warnf(ctx, "[VLMCache] Store failed for key=%s: %v", key[:16], err)
	} else {
		logger.Infof(ctx, "[VLMCache] STORED key=%s model=%s img_hash=%s", key[:16], modelID, imgHash)
	}
}

// CachedVLM wraps a VLM interface with a persistent cache.
type CachedVLM struct {
	inner VLM
	cache *VLMCache
}

// NewCachedVLM wraps the given VLM with a VLM cache.
// If cache is nil, the wrapper is a pass-through (no caching).
func NewCachedVLM(inner VLM, cache *VLMCache) VLM {
	if inner == nil {
		return nil
	}
	if cache == nil || cache.db == nil {
		return inner
	}
	return &CachedVLM{inner: inner, cache: cache}
}

func (c *CachedVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	if len(imgBytes) == 0 {
		return c.inner.Predict(ctx, imgBytes, prompt)
	}

	modelID := c.inner.GetModelID()

	// 1. Check cache
	if cached, hit := c.cache.Lookup(ctx, imgBytes, prompt, modelID); hit {
		return cached, nil
	}

	// 2. Call inner VLM for cache miss
	logger.Infof(ctx, "[VLMCache] MISS model=%s — calling VLM", modelID)
	result, err := c.inner.Predict(ctx, imgBytes, prompt)
	if err != nil {
		return "", err
	}

	// 3. Write fresh result to cache (async)
	go func() {
		bgCtx := context.Background()
		c.cache.Store(bgCtx, imgBytes, prompt, modelID, result)
	}()

	return result, nil
}

func (c *CachedVLM) GetModelName() string {
	return c.inner.GetModelName()
}

func (c *CachedVLM) GetModelID() string {
	return c.inner.GetModelID()
}

// String for debugging
func (c *CachedVLM) String() string {
	return fmt.Sprintf("CachedVLM(inner=%s)", c.inner.GetModelName())
}
