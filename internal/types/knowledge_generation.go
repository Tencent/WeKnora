package types

import "time"

// KnowledgeGenerationState is the lifecycle state for a knowledge snapshot.
type KnowledgeGenerationState string

const (
	KnowledgeGenerationStateBuilding KnowledgeGenerationState = "building"
	KnowledgeGenerationStateReady    KnowledgeGenerationState = "ready"
	KnowledgeGenerationStateActive   KnowledgeGenerationState = "active"
	KnowledgeGenerationStateRetired  KnowledgeGenerationState = "retired"
	KnowledgeGenerationStateFailed   KnowledgeGenerationState = "failed"
	KnowledgeGenerationStatePurged   KnowledgeGenerationState = "purged"
)

// KnowledgeGeneration is an invisible materialization of one knowledge rebuild.
// Only the generation referenced by Knowledge.ActiveGenerationID is visible to
// ordinary chunk, vector, wiki, and graph reads.
type KnowledgeGeneration struct {
	ID                  string                   `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID            uint64                   `json:"tenant_id" gorm:"not null;uniqueIndex:uk_knowledge_generation_attempt,priority:1"`
	KnowledgeID         string                   `json:"knowledge_id" gorm:"type:varchar(36);not null;index;uniqueIndex:uk_knowledge_generation_attempt,priority:2"`
	Attempt             int                      `json:"attempt" gorm:"not null;uniqueIndex:uk_knowledge_generation_attempt,priority:3"`
	BaseGenerationID    string                   `json:"base_generation_id,omitempty" gorm:"type:varchar(36)"`
	State               KnowledgeGenerationState `json:"state" gorm:"type:varchar(20);not null;index"`
	SourceDigest        string                   `json:"source_digest" gorm:"type:varchar(64);not null"`
	PipelineDigest      string                   `json:"pipeline_digest" gorm:"type:varchar(64);not null"`
	ManifestDigest      string                   `json:"manifest_digest,omitempty" gorm:"type:varchar(64)"`
	SnapshotDescription string                   `json:"snapshot_description,omitempty" gorm:"type:text"`
	ErrorMessage        string                   `json:"error_message,omitempty" gorm:"type:text"`
	CreatedAt           time.Time                `json:"created_at" gorm:"not null"`
	ReadyAt             *time.Time               `json:"ready_at,omitempty"`
	ActivatedAt         *time.Time               `json:"activated_at,omitempty"`
	RetiredAt           *time.Time               `json:"retired_at,omitempty"`
	UpdatedAt           time.Time                `json:"updated_at"`
}

// TableName pins the table name used by migrations and repositories.
func (KnowledgeGeneration) TableName() string {
	return "knowledge_generations"
}
