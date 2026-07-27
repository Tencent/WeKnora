package types

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type AgentCollectionSource string

const (
	CollectionSourceStructuredAnswer  AgentCollectionSource = "structured_answer"
	CollectionSourceMessageExtraction AgentCollectionSource = "message_extraction"
	CollectionSourceSystemAdmin       AgentCollectionSource = "system_admin"
	CollectionSourceSchemaMigration   AgentCollectionSource = "schema_migration"
)

type AgentCollectionProfile struct {
	ID                string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64         `json:"tenant_id" gorm:"not null;index"`
	AgentTenantID     uint64         `json:"agent_tenant_id" gorm:"not null;index"`
	AgentID           string         `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	UserID            string         `json:"user_id" gorm:"type:varchar(36);not null;index"`
	SchemaVersion     int64          `json:"schema_version" gorm:"not null;default:0"`
	Values            JSONMap        `json:"values" gorm:"type:jsonb;not null"`
	InactiveValues    JSONMap        `json:"inactive_values" gorm:"type:jsonb;not null"`
	RequiredTotal     int            `json:"required_total" gorm:"not null;default:0"`
	CompletedRequired int            `json:"completed_required" gorm:"not null;default:0"`
	IsComplete        bool           `json:"is_complete" gorm:"not null;default:false"`
	LockVersion       int64          `json:"lock_version" gorm:"not null;default:0"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"-" gorm:"index"`
}

func (AgentCollectionProfile) TableName() string { return "agent_collection_profiles" }

type AgentCollectionHistory struct {
	ID              string                `json:"id" gorm:"type:varchar(36);primaryKey"`
	ProfileID       string                `json:"profile_id" gorm:"type:varchar(36);not null;index"`
	TenantID        uint64                `json:"tenant_id" gorm:"not null;index"`
	AgentID         string                `json:"agent_id" gorm:"type:varchar(36);not null;index"`
	UserID          string                `json:"user_id" gorm:"type:varchar(36);not null;index"`
	FieldKey        string                `json:"field_key" gorm:"type:varchar(64);not null;index"`
	SchemaVersion   int64                 `json:"schema_version" gorm:"not null"`
	OldValue        json.RawMessage       `json:"old_value,omitempty" gorm:"type:jsonb"`
	NewValue        json.RawMessage       `json:"new_value,omitempty" gorm:"type:jsonb"`
	Source          AgentCollectionSource `json:"source" gorm:"type:varchar(32);not null"`
	Confidence      *float64              `json:"confidence,omitempty"`
	SourceMessageID string                `json:"source_message_id,omitempty" gorm:"type:varchar(36)"`
	SourceMessageAt *time.Time            `json:"source_message_at,omitempty"`
	ActorUserID     string                `json:"actor_user_id,omitempty" gorm:"type:varchar(36)"`
	ChangeReason    string                `json:"change_reason,omitempty" gorm:"type:varchar(500)"`
	CreatedAt       time.Time             `json:"created_at"`
}

func (AgentCollectionHistory) TableName() string { return "agent_collection_history" }

type AgentCollectionValueEntry struct {
	Value           any                   `json:"value"`
	UpdatedAt       time.Time             `json:"updated_at"`
	Source          AgentCollectionSource `json:"source"`
	SourceMessageID string                `json:"source_message_id,omitempty"`
	SourceMessageAt *time.Time            `json:"source_message_at,omitempty"`
}

type AgentCollectionValueChange struct {
	FieldKey        string
	Value           any
	Inactive        bool
	Remove          bool
	Source          AgentCollectionSource
	Confidence      *float64
	SourceMessageID string
	SourceMessageAt *time.Time
	ActorUserID     string
	ChangeReason    string
}

type ApplyCollectionChangesInput struct {
	TenantID          uint64
	AgentTenantID     uint64
	AgentID           string
	UserID            string
	SchemaVersion     int64
	RequiredTotal     int
	CompletedRequired int
	IsComplete        bool
	Changes           []AgentCollectionValueChange
}
