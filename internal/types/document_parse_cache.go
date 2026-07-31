package types

import "time"

const DocumentParseCacheSchemaVersion = "document_parse_v1"

// DocumentParseCache stores the latest resolved parser artifact for one
// knowledge. It is deliberately knowledge-scoped because the resolved image
// resources follow the knowledge lifecycle and must not leak across documents.
type DocumentParseCache struct {
	ID          uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID    uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_document_parse_cache_knowledge"`
	KnowledgeID string    `json:"knowledge_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_document_parse_cache_knowledge"`
	CacheKey    string    `json:"cache_key" gorm:"type:varchar(64);not null;index"`
	ContentKey  string    `json:"content_key" gorm:"type:varchar(128);not null"`
	ConfigHash  string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer   string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Payload     JSON      `json:"payload" gorm:"type:jsonb;not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (DocumentParseCache) TableName() string {
	return "document_parse_caches"
}
