package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	ContentCacheKindEmbedding    = "embedding"
	ContentCacheKindImageOCR     = "image_ocr"
	ContentCacheKindImageCaption = "image_caption"
)

// ContentCacheEntry stores reusable deterministic processing artifacts.
type ContentCacheEntry struct {
	ID        string    `json:"id"         gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"tenant_id"  gorm:"not null;uniqueIndex:idx_content_caches_key,priority:1"`
	CacheKind string    `json:"cache_kind" gorm:"type:varchar(32);not null;uniqueIndex:idx_content_caches_key,priority:2"`
	CacheKey  string    `json:"cache_key"  gorm:"type:varchar(255);not null;uniqueIndex:idx_content_caches_key,priority:3"`
	Payload   JSON      `json:"payload"    gorm:"type:json;not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ContentCacheEntry) TableName() string {
	return "content_caches"
}

func (c *ContentCacheEntry) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}
