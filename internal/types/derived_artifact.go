package types

import "time"

type DerivedArtifactStatus string

const (
	DerivedArtifactPending   DerivedArtifactStatus = "pending"
	DerivedArtifactComputing DerivedArtifactStatus = "computing"
	DerivedArtifactSucceeded DerivedArtifactStatus = "succeeded"
	DerivedArtifactFailed    DerivedArtifactStatus = "failed"
)

// DerivedArtifact is a tenant-scoped durable result and computation lease.
// Version changes create a new ArtifactKey; records are never overwritten for
// cache invalidation and deliberately have no gorm.DeletedAt field.
type DerivedArtifact struct {
	ID              uint64                `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID        uint64                `json:"tenant_id" gorm:"not null;uniqueIndex:uk_derived_artifacts_tenant_key,priority:1"`
	ArtifactKey     string                `json:"artifact_key" gorm:"type:varchar(64);not null;uniqueIndex:uk_derived_artifacts_tenant_key,priority:2"`
	ArtifactKind    string                `json:"artifact_kind" gorm:"type:varchar(64);not null;index:idx_derived_artifacts_kind_status,priority:1"`
	InputDigest     string                `json:"input_digest" gorm:"type:varchar(64);not null"`
	ModelID         string                `json:"model_id" gorm:"type:varchar(255);not null;default:''"`
	ModelRevision   string                `json:"model_revision" gorm:"type:varchar(128);not null;default:''"`
	PromptVersion   string                `json:"prompt_version" gorm:"type:varchar(128);not null;default:''"`
	ConfigDigest    string                `json:"config_digest" gorm:"type:varchar(64);not null;default:''"`
	ProducerVersion string                `json:"producer_version" gorm:"type:varchar(128);not null;default:''"`
	Status          DerivedArtifactStatus `json:"status" gorm:"type:varchar(16);not null;index:idx_derived_artifacts_kind_status,priority:2;index:idx_derived_artifacts_lease,priority:1"`
	Payload         []byte                `json:"payload,omitempty"`
	PayloadEncoding string                `json:"payload_encoding" gorm:"type:varchar(32);not null;default:''"`
	ObjectURI       string                `json:"object_uri" gorm:"type:text"`
	PayloadDigest   string                `json:"payload_digest" gorm:"type:varchar(64);not null;default:''"`
	ErrorCode       string                `json:"error_code" gorm:"type:varchar(128);not null;default:''"`
	ErrorMessage    string                `json:"error_message" gorm:"type:varchar(2048);not null;default:''"`
	AttemptCount    int                   `json:"attempt_count" gorm:"not null;default:0"`
	OwnerToken      string                `json:"-" gorm:"type:varchar(128);not null;default:''"`
	LeaseExpiresAt  *time.Time            `json:"lease_expires_at,omitempty" gorm:"index:idx_derived_artifacts_lease,priority:2"`
	CreatedAt       time.Time             `json:"created_at" gorm:"not null"`
	UpdatedAt       time.Time             `json:"updated_at" gorm:"not null"`
	CompletedAt     *time.Time            `json:"completed_at,omitempty"`
}

func (DerivedArtifact) TableName() string { return "derived_artifacts" }
