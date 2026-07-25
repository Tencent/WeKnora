package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EmbeddingCacheEntry represents a row in the embedding_cache table.
type EmbeddingCacheEntry struct {
	CacheKey    string    `gorm:"column:cache_key;primaryKey"`
	ModelID     string    `gorm:"column:model_id"`
	Dimensions  int       `gorm:"column:dimensions"`
	Embedding   []byte    `gorm:"column:embedding"`
	TextPreview string    `gorm:"column:text_preview"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (EmbeddingCacheEntry) TableName() string {
	return "embedding_cache"
}

// EmbeddingCache provides a persistent, cross-document cache for embedding vectors.
// Cache key = SHA256(normalized_text + model_id + dimensions).
type EmbeddingCache struct {
	db *gorm.DB
}

// NewEmbeddingCache creates a new EmbeddingCache using the given GORM DB.
// If db is nil, the cache is disabled (all lookups are misses, no writes).
// On creation, it runs a background sweep to delete entries older than 90 days.
func NewEmbeddingCache(db *gorm.DB) *EmbeddingCache {
	c := &EmbeddingCache{db: db}
	if db != nil {
		go c.sweepOldEntries()
	}
	return c
}

// sweepOldEntries deletes cache entries older than 90 days.
// Runs once at startup — sufficient since cache growth is slow.
func (c *EmbeddingCache) sweepOldEntries() {
	ctx := context.Background()
	cutoff := time.Now().AddDate(0, 0, -90)
	result := c.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&EmbeddingCacheEntry{})
	if result.RowsAffected > 0 {
		logger.Infof(ctx, "[EmbeddingCache] Swept %d entries older than 90 days", result.RowsAffected)
	}
}

// cacheKey computes the cache key for a given text + model + dimensions.
func cacheKey(text, modelID string, dimensions int) string {
	normalized := strings.TrimSpace(text)
	h := sha256.New()
	h.Write([]byte(normalized))
	h.Write([]byte(":"))
	h.Write([]byte(modelID))
	h.Write([]byte(":"))
	dimBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(dimBytes, uint32(dimensions))
	h.Write(dimBytes)
	return hex.EncodeToString(h.Sum(nil))
}

// floatsToBytes serializes []float32 to a byte slice (little-endian).
func floatsToBytes(floats []float32) []byte {
	buf := make([]byte, 4*len(floats))
	for i, f := range floats {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// bytesToFloats deserializes a byte slice back to []float32.
func bytesToFloats(data []byte) []float32 {
	if len(data)%4 != 0 {
		return nil
	}
	n := len(data) / 4
	floats := make([]float32, n)
	for i := 0; i < n; i++ {
		floats[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return floats
}

// Lookup retrieves cached embeddings for the given texts.
// Returns a map of cache_key → []float32 for cache hits.
func (c *EmbeddingCache) Lookup(ctx context.Context, texts []string, modelID string, dimensions int) map[string][]float32 {
	if c == nil || c.db == nil || len(texts) == 0 {
		return nil
	}
	keys := make([]string, len(texts))
	keyToText := make(map[string]string, len(texts))
	for i, text := range texts {
		k := cacheKey(text, modelID, dimensions)
		keys[i] = k
		keyToText[k] = text
	}

	var entries []EmbeddingCacheEntry
	// Query in batches to avoid IN clause limits
	batchSize := 500
	result := make(map[string][]float32, len(texts))
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]
		if err := c.db.WithContext(ctx).Where("cache_key IN ?", batch).Find(&entries).Error; err != nil {
			logger.Warnf(ctx, "[EmbeddingCache] Lookup failed: %v", err)
			return result
		}
		for _, entry := range entries {
			floats := bytesToFloats(entry.Embedding)
			if len(floats) > 0 {
				result[entry.CacheKey] = floats
			}
		}
	}
	return result
}

// Store saves embedding results to the cache in batches.
// Uses OnConflict upsert so existing entries are updated, new ones inserted,
// all in a single bulk operation instead of N individual Save calls.
func (c *EmbeddingCache) Store(ctx context.Context, texts []string, embeddings [][]float32, modelID string, dimensions int) {
	if c == nil || c.db == nil || len(texts) == 0 {
		return
	}
	now := time.Now()
	entries := make([]EmbeddingCacheEntry, 0, len(texts))
	for i, text := range texts {
		if i >= len(embeddings) || len(embeddings[i]) == 0 {
			continue
		}
		k := cacheKey(text, modelID, dimensions)
		preview := text
		if runes := []rune(preview); len(runes) > 100 {
			preview = string(runes[:100])
		}
		entries = append(entries, EmbeddingCacheEntry{
			CacheKey:    k,
			ModelID:     modelID,
			Dimensions:  dimensions,
			Embedding:   floatsToBytes(embeddings[i]),
			TextPreview: preview,
			UpdatedAt:   now,
		})
	}
	if len(entries) == 0 {
		return
	}
	// Batch upsert: 100 at a time with ON CONFLICT DO UPDATE
	batchSize := 100
	for i := 0; i < len(entries); i += batchSize {
		end := i + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[i:end]
		if err := c.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "cache_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"model_id", "dimensions", "embedding", "text_preview", "updated_at"}),
		}).CreateInBatches(batch, batchSize).Error; err != nil {
			logger.Debugf(ctx, "[EmbeddingCache] Store batch failed (%d entries): %v", len(batch), err)
		}
	}
}

// CachedEmbedder wraps an Embedder with a persistent cache.
// On BatchEmbed: checks cache first, only calls the inner embedder for cache misses,
// then writes the fresh results back to the cache.
type CachedEmbedder struct {
	inner Embedder
	cache *EmbeddingCache
	mu    sync.Mutex // protects batch dedup within a single BatchEmbed call
}

// NewCachedEmbedder wraps the given embedder with an embedding cache.
// If cache is nil, the wrapper is a pass-through (no caching).
func NewCachedEmbedder(inner Embedder, cache *EmbeddingCache) Embedder {
	if inner == nil {
		return nil
	}
	if cache == nil || cache.db == nil {
		return inner
	}
	return &CachedEmbedder{inner: inner, cache: cache}
}

func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := c.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) > 0 {
		return results[0], nil
	}
	return nil, nil
}

func (c *CachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	modelID := c.inner.GetModelID()
	dims := c.inner.GetDimensions()

	// 1. Check cache
	cached := c.cache.Lookup(ctx, texts, modelID, dims)

	// 2. Determine which texts need embedding (cache misses)
	missIndices := make([]int, 0, len(texts))
	missTexts := make([]string, 0, len(texts))
	for i, text := range texts {
		k := cacheKey(text, modelID, dims)
		if _, hit := cached[k]; hit {
			continue
		}
		missIndices = append(missIndices, i)
		missTexts = append(missTexts, text)
	}

	hits := len(texts) - len(missTexts)
	logger.Infof(ctx, "[EmbeddingCache] model=%s dims=%d total=%d hits=%d misses=%d",
		modelID, dims, len(texts), hits, len(missTexts))

	// 3. Call inner embedder for misses
	var freshEmbeddings [][]float32
	if len(missTexts) > 0 {
		var err error
		freshEmbeddings, err = c.inner.BatchEmbed(ctx, missTexts)
		if err != nil {
			return nil, err
		}
		// 4. Write fresh results to cache (async to avoid blocking)
		go func() {
			bgCtx := context.Background()
			c.cache.Store(bgCtx, missTexts, freshEmbeddings, modelID, dims)
		}()
	}

	// 5. Merge cached + fresh results in original order
	result := make([][]float32, len(texts))
	// Fill cached results
	for i, text := range texts {
		k := cacheKey(text, modelID, dims)
		if vec, hit := cached[k]; hit {
			result[i] = vec
		}
	}
	// Fill fresh results
	for j, idx := range missIndices {
		if j < len(freshEmbeddings) {
			result[idx] = freshEmbeddings[j]
		}
	}

	return result, nil
}

func (c *CachedEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	// Delegate to BatchEmbed which has caching logic.
	// The pooler concurrency is handled by the inner embedder's BatchEmbed.
	return c.BatchEmbed(ctx, texts)
}

func (c *CachedEmbedder) GetModelName() string {
	return c.inner.GetModelName()
}

func (c *CachedEmbedder) GetDimensions() int {
	return c.inner.GetDimensions()
}

func (c *CachedEmbedder) GetModelID() string {
	return c.inner.GetModelID()
}
