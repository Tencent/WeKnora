package types

import "time"

const WikiMapCacheSchemaV1 = "wiki-map-v1"

// WikiMapCache stores deterministic per-document Wiki map-phase outputs.
// The reduce phase is intentionally not cached because it depends on the
// current cross-document wiki page state.
type WikiMapCache struct {
	ID          uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID    uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_wiki_map_cache_key"`
	CacheKey    string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_wiki_map_cache_key"`
	ContentHash string    `json:"content_hash" gorm:"type:varchar(64);not null;index"`
	ModelID     string    `json:"model_id" gorm:"type:varchar(128);not null;default:''"`
	ConfigHash  string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer   string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Payload     JSON      `json:"payload" gorm:"type:jsonb;not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (WikiMapCache) TableName() string {
	return "wiki_map_caches"
}
