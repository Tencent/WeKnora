package types

import "time"

const (
	// GraphExtractionCacheSchemaVersion bumps whenever the cached graph JSON
	// contract changes. It is part of the cache key so old rows become cold
	// misses instead of being interpreted with new semantics.
	GraphExtractionCacheSchemaVersion = "graph_extract_v1"
)

// GraphExtractionCache stores the LLM-produced per-chunk GraphRAG extraction.
//
// The row is intentionally scoped by the deterministic extraction inputs
// (chunk text, model, prompt/config), not by chunk_id. That lets reparses reuse
// the expensive LLM output even when the rebuilt document receives new chunk IDs.
type GraphExtractionCache struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID   uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_graph_extract_cache_key"`
	CacheKey   string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_graph_extract_cache_key"`
	ContentKey string    `json:"content_key" gorm:"type:varchar(64);not null;index"`
	ModelID    string    `json:"model_id" gorm:"type:varchar(128);not null;default:''"`
	ConfigHash string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer  string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Graph      JSON      `json:"graph" gorm:"type:jsonb;not null"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (GraphExtractionCache) TableName() string {
	return "graph_extraction_caches"
}
