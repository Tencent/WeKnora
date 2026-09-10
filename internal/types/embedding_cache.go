package types

import (
	"encoding/json"
	"time"
)

// EmbeddingCacheKey identifies one cacheable embedding input.
type EmbeddingCacheKey struct {
	TenantID  uint64
	ModelID   string
	Dimension int
	TextHash  string
}

// EmbeddingCacheEntry is the persisted embedding vector cache row.
type EmbeddingCacheEntry struct {
	ID        string          `gorm:"primaryKey;type:varchar(64)" json:"id"`
	TenantID  uint64          `gorm:"index;uniqueIndex:idx_embedding_cache_key" json:"tenant_id"`
	ModelID   string          `gorm:"type:varchar(128);index;uniqueIndex:idx_embedding_cache_key" json:"model_id"`
	Dimension int             `gorm:"index;uniqueIndex:idx_embedding_cache_key" json:"dimension"`
	TextHash  string          `gorm:"type:varchar(64);uniqueIndex:idx_embedding_cache_key" json:"text_hash"`
	Vector    json.RawMessage `gorm:"type:jsonb" json:"vector"`
	Hits      int64           `gorm:"default:1" json:"hits"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TableName returns the database table name for EmbeddingCacheEntry.
func (EmbeddingCacheEntry) TableName() string {
	return "embedding_cache_entries"
}

// EmbeddingCacheStats exposes process-level hit/miss counters.
type EmbeddingCacheStats struct {
	Enabled       bool                       `json:"enabled"`
	Hits          int64                      `json:"hits"`
	Misses        int64                      `json:"misses"`
	ProviderCalls int64                      `json:"provider_calls"`
	Models        []EmbeddingCacheModelStats `json:"models"`
}

// EmbeddingCacheModelStats is the per-model portion of the process cache
// counters. Hits/misses count text lookups; ProviderCalls counts actual model
// requests (one batch request can cover several misses).
type EmbeddingCacheModelStats struct {
	ModelID       string `json:"model_id"`
	ModelName     string `json:"model_name"`
	Hits          int64  `json:"hits"`
	Misses        int64  `json:"misses"`
	ProviderCalls int64  `json:"provider_calls"`
}
