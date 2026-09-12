package types

import "time"

// EmbeddingCacheEntry stores one content-addressed vector and never stores source text.
type EmbeddingCacheEntry struct {
	TenantID             uint64    `json:"tenant_id" gorm:"primaryKey;not null"`
	ModelID              string    `json:"model_id" gorm:"type:varchar(64);primaryKey"`
	ModelFingerprint     string    `json:"model_fingerprint" gorm:"type:char(64);primaryKey"`
	RequestOptionsSHA256 string    `json:"request_options_sha256" gorm:"type:char(64);primaryKey"`
	TextSHA256           string    `json:"text_sha256" gorm:"type:char(64);primaryKey"`
	Embedding            []byte    `json:"-" gorm:"type:bytea;not null"`
	Dimension            int       `json:"dimension" gorm:"not null"`
	ExpiresAt            time.Time `json:"expires_at" gorm:"not null;index"`
	AccessedAt           time.Time `json:"accessed_at" gorm:"not null;index"`
	CreatedAt            time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt            time.Time `json:"updated_at" gorm:"not null"`
}

// TableName returns the persistent embedding cache table name.
func (EmbeddingCacheEntry) TableName() string { return "embedding_cache_entries" }
