package types

import (
	"time"

	"gorm.io/gorm"
)

type SkillSource string

const (
	SkillSourcePreloaded SkillSource = "preloaded"
	SkillSourceTenant    SkillSource = "tenant"
)

type SkillReference struct {
	Source  SkillSource `json:"source" yaml:"source"`
	SkillID string      `json:"skill_id" yaml:"skill_id"`
}

type SkillCategory string

const (
	SkillCategoryContent     SkillCategory = "content"
	SkillCategoryData        SkillCategory = "data"
	SkillCategoryDevelopment SkillCategory = "development"
	SkillCategoryWorkflow    SkillCategory = "workflow"
	SkillCategoryOther       SkillCategory = "other"
)

type TenantSkillStatus string

const (
	TenantSkillEnabled  TenantSkillStatus = "enabled"
	TenantSkillDisabled TenantSkillStatus = "disabled"
)

type TenantSkillVersionState string

const (
	SkillVersionStaging TenantSkillVersionState = "staging"
	SkillVersionReady   TenantSkillVersionState = "ready"
	SkillVersionCurrent TenantSkillVersionState = "current"
	SkillVersionGarbage TenantSkillVersionState = "garbage"
)

type TenantSkill struct {
	ID               string            `json:"id" gorm:"primaryKey;size:36"`
	TenantID         uint64            `json:"tenant_id" gorm:"not null;index"`
	Name             string            `json:"name" gorm:"not null;size:50"`
	Description      string            `json:"description" gorm:"not null;size:500"`
	Category         SkillCategory     `json:"category" gorm:"not null;size:32"`
	Status           TenantSkillStatus `json:"status" gorm:"not null;size:16"`
	CurrentVersionID *string           `json:"current_version_id,omitempty" gorm:"size:36"`
	HasScripts       bool              `json:"has_scripts" gorm:"not null"`
	UploadedBy       string            `json:"uploaded_by" gorm:"not null;size:36"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	DeletedAt        gorm.DeletedAt    `json:"-" gorm:"index"`
}

func (TenantSkill) TableName() string { return "tenant_skills" }

type TenantSkillVersion struct {
	ID           string                  `json:"id" gorm:"primaryKey;size:36"`
	TenantID     uint64                  `json:"tenant_id" gorm:"not null;index"`
	SkillID      string                  `json:"skill_id" gorm:"not null;size:36;index"`
	Version      int64                   `json:"version" gorm:"not null"`
	State        TenantSkillVersionState `json:"state" gorm:"not null;size:16;index"`
	StoragePath  string                  `json:"storage_path" gorm:"not null;size:512"`
	ContentHash  string                  `json:"content_hash" gorm:"not null;size:64"`
	ManifestJSON []byte                  `json:"manifest" gorm:"not null"`
	CreatedBy    string                  `json:"created_by" gorm:"not null;size:36"`
	CreatedAt    time.Time               `json:"created_at"`
	GarbageAt    *time.Time              `json:"garbage_at,omitempty"`
}

func (TenantSkillVersion) TableName() string { return "tenant_skill_versions" }

type SkillExecutionAudit struct {
	ID            string     `json:"id" gorm:"primaryKey;size:36"`
	TenantID      uint64     `json:"tenant_id" gorm:"not null;index"`
	SkillID       string     `json:"skill_id" gorm:"not null;size:36;index"`
	VersionID     string     `json:"version_id" gorm:"not null;size:36"`
	UserID        string     `json:"user_id" gorm:"not null;size:36"`
	ScriptPath    string     `json:"script_path" gorm:"not null;size:512"`
	Status        string     `json:"status" gorm:"not null;size:16"`
	StartedAt     time.Time  `json:"started_at" gorm:"not null"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	DurationMS    int64      `json:"duration_ms"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	Killed        bool       `json:"killed"`
	Truncated     bool       `json:"truncated"`
	OutputSummary string     `json:"output_summary" gorm:"size:4096"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (SkillExecutionAudit) TableName() string { return "skill_execution_audits" }

type ExecutionAuditFinish struct {
	Status        string
	FinishedAt    time.Time
	DurationMS    int64
	ExitCode      *int
	Killed        bool
	Truncated     bool
	OutputSummary string
}

type SkillStatusUpdate struct {
	SkillID string            `json:"skill_id"`
	Status  TenantSkillStatus `json:"status"`
}

type SkillStatusResult struct {
	SkillID string `json:"skill_id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
}

type SkillSummary struct {
	Source      SkillSource       `json:"source"`
	SkillID     string            `json:"skill_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    SkillCategory     `json:"category"`
	Status      TenantSkillStatus `json:"status"`
	Version     int64             `json:"version,omitempty"`
	HasScripts  bool              `json:"has_scripts"`
	ReadOnly    bool              `json:"readonly"`
	UploadedBy  string            `json:"uploaded_by,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at,omitempty"`
}

type SkillDetail struct {
	SkillSummary
	VersionID   string   `json:"version_id,omitempty"`
	ContentHash string   `json:"content_hash,omitempty"`
	Files       []string `json:"files,omitempty"`
}
