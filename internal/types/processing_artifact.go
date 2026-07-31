package types

import "time"

// ProcessingArtifact stores reusable, ownership-free pipeline outputs.
type ProcessingArtifact struct {
	ID              string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64     `json:"tenant_id" gorm:"not null;uniqueIndex:uk_processing_artifact_key,priority:1"`
	Stage           string     `json:"stage" gorm:"type:varchar(64);not null;uniqueIndex:uk_processing_artifact_key,priority:2"`
	KeyVersion      int        `json:"key_version" gorm:"not null;uniqueIndex:uk_processing_artifact_key,priority:3"`
	ArtifactKey     string     `json:"artifact_key" gorm:"type:varchar(64);not null;uniqueIndex:uk_processing_artifact_key,priority:4"`
	ProcessorDigest string     `json:"processor_digest" gorm:"type:varchar(64);not null"`
	OutputDigest    string     `json:"output_digest" gorm:"type:varchar(64);not null"`
	OutputSchema    string     `json:"output_schema" gorm:"type:varchar(64);not null"`
	Codec           string     `json:"codec" gorm:"type:varchar(20);not null"`
	Payload         []byte     `json:"payload" gorm:"type:blob"`
	PayloadChecksum string     `json:"payload_checksum" gorm:"type:varchar(64);not null"`
	PayloadSize     int64      `json:"payload_size" gorm:"not null"`
	HitCount        int64      `json:"hit_count" gorm:"not null;default:0"`
	LastHitAt       *time.Time `json:"last_hit_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at" gorm:"not null"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
}

// TableName pins the table name used by migrations and repositories.
func (ProcessingArtifact) TableName() string {
	return "processing_artifacts"
}
