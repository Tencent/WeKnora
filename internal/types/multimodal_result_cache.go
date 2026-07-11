package types

import "time"

const MultimodalResultCacheSchemaV1 = "multimodal-result-v1"

type MultimodalOutputType = string

const (
	MultimodalOutputOCR     MultimodalOutputType = "ocr"
	MultimodalOutputCaption MultimodalOutputType = "caption"
)

// MultimodalResultCache stores deterministic VLM outputs for image OCR and
// caption generation. The cache is keyed by image bytes, model, prompt, output
// type, and schema version so reparses can skip expensive VLM calls when the
// actual inputs have not changed.
type MultimodalResultCache struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID   uint64    `json:"tenant_id" gorm:"index;not null;uniqueIndex:idx_multimodal_result_cache_key"`
	CacheKey   string    `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_multimodal_result_cache_key"`
	ImageHash  string    `json:"image_hash" gorm:"type:varchar(64);not null;index"`
	ModelID    string    `json:"model_id" gorm:"type:varchar(128);not null;default:''"`
	PromptHash string    `json:"prompt_hash" gorm:"type:varchar(64);not null"`
	OutputType string    `json:"output_type" gorm:"type:varchar(32);not null;index"`
	SchemaVer  string    `json:"schema_ver" gorm:"type:varchar(32);not null"`
	Content    string    `json:"content" gorm:"type:text;not null"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (MultimodalResultCache) TableName() string {
	return "multimodal_result_caches"
}
