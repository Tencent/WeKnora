package types

import "time"

type EmbeddingCacheEntry struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID   uint64    `gorm:"not null;uniqueIndex:uq_embedding_cache_key" json:"tenant_id"`
	ModelKey   string    `gorm:"type:varchar(128);not null;uniqueIndex:uq_embedding_cache_key" json:"model_key"`
	InputHash  string    `gorm:"type:varchar(64);not null;uniqueIndex:uq_embedding_cache_key" json:"input_hash"`
	Vector     JSON      `gorm:"type:jsonb;not null" json:"vector"`
	Dimensions int       `gorm:"not null" json:"dimensions"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (EmbeddingCacheEntry) TableName() string { return "embedding_cache_entries" }
