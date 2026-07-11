package types

import "time"

const DocParseCacheSchemaV1 = "doc-parse-v1"

// DocParseCache stores deterministic DocReader outputs for file-backed
// knowledge. URL pages are intentionally excluded because their content may
// change without the URL changing.
type DocParseCache struct {
	ID          uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID    uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_doc_parse_cache_key"`
	CacheKey    string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_doc_parse_cache_key"`
	ContentHash string    `json:"content_hash" gorm:"type:varchar(64);not null;index"`
	Parser      string    `json:"parser" gorm:"type:varchar(128);not null;default:''"`
	ConfigHash  string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer   string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Payload     JSON      `json:"payload" gorm:"type:jsonb;not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (DocParseCache) TableName() string {
	return "doc_parse_caches"
}
