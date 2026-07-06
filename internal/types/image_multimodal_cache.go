package types

import "time"

const (
	// ImageMultimodalCacheSchemaVersion bumps when cached OCR/caption payload
	// semantics or prompt contracts change. It is part of every key so old rows
	// become cold misses.
	ImageMultimodalCacheSchemaVersion = "image_multimodal_v1"
)

// ImageMultimodalCache stores deterministic OCR/caption outputs for one image.
//
// The row is scoped by image bytes + VLM model/config + prompt version, not by
// knowledge_id or chunk_id. That lets reparses reuse the expensive VLM result
// even when rebuilt chunks receive new IDs.
type ImageMultimodalCache struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID   uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_image_multimodal_cache_key"`
	CacheKey   string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_image_multimodal_cache_key"`
	ContentKey string    `json:"content_key" gorm:"type:varchar(64);not null;index"`
	ModelID    string    `json:"model_id" gorm:"type:varchar(128);not null;default:''"`
	ConfigHash string    `json:"config_hash" gorm:"type:varchar(64);not null"`
	SchemaVer  string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Payload    JSON      `json:"payload" gorm:"type:jsonb;not null"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (ImageMultimodalCache) TableName() string {
	return "image_multimodal_caches"
}
