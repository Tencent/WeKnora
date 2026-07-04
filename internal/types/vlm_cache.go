package types

import "time"

type VLMCache struct {
	ImageHash      string    `gorm:"column:image_hash;primaryKey;type:char(64)"`
	VlmModelID     string    `gorm:"column:vlm_model_id;primaryKey;type:varchar(128)"`
	PromptVersion  string    `gorm:"column:prompt_version;primaryKey;type:varchar(64)"`
	ResultText     string    `gorm:"column:result_text;type:text;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;type:timestamptz;default:now();not null"`
	LastAccessedAt time.Time `gorm:"column:last_accessed_at;type:timestamptz;default:now();not null"`
}

func (VLMCache) TableName() string {
	return "vlm_cache"
}
