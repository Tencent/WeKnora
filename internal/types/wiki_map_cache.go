package types

import "time"

const (
	// WikiMapCacheSchemaVersion bumps when cached Wiki map payload semantics
	// change. It is part of every key so old rows become cold misses.
	WikiMapCacheSchemaVersion = "wiki_map_v1"
)

// WikiMapCache stores deterministic LLM outputs from the per-document Wiki map
// phase. Reduce/merge stays uncached because it depends on current KB state.
type WikiMapCache struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID   uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_wiki_map_cache_key"`
	CacheKey   string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_wiki_map_cache_key"`
	Kind       string    `json:"kind" gorm:"type:varchar(64);not null;index"`
	ContentKey string    `json:"content_key" gorm:"type:varchar(64);not null;index"`
	ModelID    string    `json:"model_id" gorm:"type:varchar(128);not null;default:''"`
	ConfigHash string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer  string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Payload    JSON      `json:"payload" gorm:"type:jsonb;not null"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (WikiMapCache) TableName() string {
	return "wiki_map_caches"
}
