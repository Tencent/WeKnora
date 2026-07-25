package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// LLMCacheEntry represents a row in the llm_cache table.
type LLMCacheEntry struct {
	CacheKey    string    `gorm:"column:cache_key;primaryKey"`
	ModelID     string    `gorm:"column:model_id"`
	PromptHash  string    `gorm:"column:prompt_hash"`
	Result      string    `gorm:"column:result"`
	TextPreview string    `gorm:"column:text_preview"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (LLMCacheEntry) TableName() string {
	return "llm_cache"
}

// LLMCache provides a persistent cache for deterministic LLM calls
// (question generation, summary, entity extraction, etc.).
// Cache key = SHA256(system_prompt + user_content + model_id + params).
type LLMCache struct {
	db *gorm.DB
}

// NewLLMCache creates a new LLMCache using the given GORM DB.
// If db is nil, the cache is disabled (all lookups are misses, no writes).
// On creation, runs a background sweep to delete entries older than 90 days.
func NewLLMCache(db *gorm.DB) *LLMCache {
	c := &LLMCache{db: db}
	if db != nil {
		go c.sweepOldEntries()
	}
	return c
}

// sweepOldEntries deletes cache entries older than 90 days.
func (c *LLMCache) sweepOldEntries() {
	ctx := context.Background()
	cutoff := time.Now().AddDate(0, 0, -90)
	result := c.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&LLMCacheEntry{})
	if result.RowsAffected > 0 {
		logger.Infof(ctx, "[LLMCache] Swept %d entries older than 90 days", result.RowsAffected)
	}
}

// llmCacheKey computes the cache key for a given set of messages + model + params.
func llmCacheKey(messages []Message, modelID string, temperature float64, maxTokens int) string {
	h := sha256.New()
	for _, msg := range messages {
		h.Write([]byte(msg.Role))
		h.Write([]byte{0})
		h.Write([]byte(msg.Content))
		h.Write([]byte{0})
	}
	h.Write([]byte(modelID))
	h.Write([]byte{0})
	fmt.Fprintf(h, "%.4f|%d", temperature, maxTokens)
	return hex.EncodeToString(h.Sum(nil))
}

// Lookup retrieves a cached LLM response.
// Returns the cached result and true if found, or "" and false if not.
func (c *LLMCache) Lookup(ctx context.Context, messages []Message, modelID string, temperature float64, maxTokens int) (string, bool) {
	if c == nil || c.db == nil || len(messages) == 0 {
		return "", false
	}
	key := llmCacheKey(messages, modelID, temperature, maxTokens)
	var entry LLMCacheEntry
	if err := c.db.WithContext(ctx).Where("cache_key = ?", key).First(&entry).Error; err != nil {
		return "", false
	}
	logger.Infof(ctx, "[LLMCache] HIT key=%s model=%s", key[:16], modelID)
	return entry.Result, true
}

// Store saves an LLM response to the cache.
func (c *LLMCache) Store(ctx context.Context, messages []Message, modelID string, temperature float64, maxTokens int, result string) {
	if c == nil || c.db == nil || len(messages) == 0 || result == "" {
		return
	}
	key := llmCacheKey(messages, modelID, temperature, maxTokens)

	// Build a preview from the last user message
	preview := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			preview = messages[i].Content
			break
		}
	}
	preview = trimPreview(preview)

	// Compute prompt hash for debugging / invalidation
	promptHash := ""
	if len(messages) > 0 {
		ph := sha256.Sum256([]byte(messages[0].Content))
		promptHash = hex.EncodeToString(ph[:])[:16]
	}

	entry := LLMCacheEntry{
		CacheKey:    key,
		ModelID:     modelID,
		PromptHash:  promptHash,
		Result:      result,
		TextPreview: preview,
		UpdatedAt:   time.Now(),
	}

	// Upsert: insert or update on conflict
	if err := c.db.WithContext(ctx).Save(&entry).Error; err != nil {
		logger.Warnf(ctx, "[LLMCache] Store failed for key=%s: %v", key[:16], err)
	} else {
		logger.Infof(ctx, "[LLMCache] STORED key=%s model=%s preview_len=%d", key[:16], modelID, len([]rune(preview)))
	}
}

// CachedChat wraps a Chat interface with a persistent LLM cache.
// On Chat: checks cache first, only calls the inner chat for cache misses,
// then writes the fresh results back to the cache.
// Streaming calls are NOT cached — they delegate directly to the inner chat.
type CachedChat struct {
	inner Chat
	cache *LLMCache
}

// NewCachedChat wraps the given chat with an LLM cache.
// If cache is nil, the wrapper is a pass-through (no caching).
func NewCachedChat(inner Chat, cache *LLMCache) Chat {
	if inner == nil {
		return nil
	}
	if cache == nil || cache.db == nil {
		return inner
	}
	return &CachedChat{inner: inner, cache: cache}
}

func (c *CachedChat) Chat(ctx context.Context, messages []Message, options *ChatOptions) (*types.ChatResponse, error) {
	if len(messages) == 0 {
		return c.inner.Chat(ctx, messages, options)
	}

	modelID := c.inner.GetModelID()
	temperature := 0.0
	maxTokens := 0
	if options != nil {
		temperature = options.Temperature
		maxTokens = options.MaxTokens
	}

	// 1. Check cache
	if cached, hit := c.cache.Lookup(ctx, messages, modelID, temperature, maxTokens); hit {
		return &types.ChatResponse{Content: cached}, nil
	}

	// 2. Call inner chat for cache miss
	logger.Infof(ctx, "[LLMCache] MISS model=%s — calling LLM", modelID)
	resp, err := c.inner.Chat(ctx, messages, options)
	if err != nil {
		return nil, err
	}

	// 3. Write fresh result to cache (async to avoid blocking)
	go func() {
		bgCtx := context.Background()
		c.cache.Store(bgCtx, messages, modelID, temperature, maxTokens, resp.Content)
	}()

	return resp, nil
}

func (c *CachedChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	// Streaming is not cached — delegate directly to inner
	return c.inner.ChatStream(ctx, messages, opts)
}

func (c *CachedChat) GetModelName() string {
	return c.inner.GetModelName()
}

func (c *CachedChat) GetModelID() string {
	return c.inner.GetModelID()
}

// MarshalJSON for debugging
func (c *CachedChat) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":  "cached_chat",
		"inner": c.inner.GetModelName(),
	})
}

// String for debugging
func (c *CachedChat) String() string {
	return fmt.Sprintf("CachedChat(inner=%s)", c.inner.GetModelName())
}

// helper: trim spaces for preview
func trimPreview(s string) string {
	s = strings.TrimSpace(s)
	if runes := []rune(s); len(runes) > 100 {
		return string(runes[:100])
	}
	return s
}
