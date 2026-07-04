package types

import "time"

const GraphExtractionCacheSchemaV1 = "graph-extraction-v1"

// GraphExtractionCache stores deterministic per-chunk GraphRAG extraction
// output. The cached graph omits chunk bindings; callers rebind nodes to the
// current chunk ID before writing to the graph store.
type GraphExtractionCache struct {
	ID          uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID    uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_graph_extraction_cache_key"`
	CacheKey    string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_graph_extraction_cache_key"`
	ContentHash string    `json:"content_hash" gorm:"type:varchar(64);not null;index"`
	ModelID     string    `json:"model_id" gorm:"type:varchar(128);not null;default:''"`
	ConfigHash  string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer   string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Graph       JSON      `json:"graph" gorm:"type:jsonb;not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (GraphExtractionCache) TableName() string {
	return "graph_extraction_caches"
}
