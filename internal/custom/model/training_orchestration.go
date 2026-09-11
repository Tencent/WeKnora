package model

import "time"

type TrainingOrchestrationJob struct {
	ID                string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	OwnerScopeID      string     `gorm:"type:varchar(128);not null;index" json:"owner_scope_id"`
	Status            string     `gorm:"type:varchar(24);not null;index" json:"status"`
	Stage             string     `gorm:"type:varchar(32)" json:"stage,omitempty"`
	Progress          int        `gorm:"not null;default:0" json:"progress"`
	SourceFingerprint string     `gorm:"type:varchar(80);index" json:"source_fingerprint"`
	InputReferences   string     `gorm:"type:text" json:"-"`
	ResultWikiPageID  string     `gorm:"type:varchar(64);index" json:"result_wiki_page_id,omitempty"`
	Model             string     `gorm:"type:varchar(128)" json:"model,omitempty"`
	PromptVersion     string     `gorm:"type:varchar(64)" json:"prompt_version,omitempty"`
	ErrorCode         string     `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage      string     `gorm:"type:text" json:"error_message,omitempty"`
	WarningCode       string     `gorm:"type:varchar(64)" json:"warning_code,omitempty"`
	WarningMessage    string     `gorm:"type:text" json:"warning_message,omitempty"`
	Reused            bool       `gorm:"not null;default:false" json:"reused"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type TrainingOrchestrationCurrent struct {
	OwnerScopeID      string    `gorm:"type:varchar(128);primaryKey" json:"owner_scope_id"`
	JobID             string    `gorm:"type:varchar(36);not null;index" json:"job_id"`
	ResultWikiPageID  string    `gorm:"type:varchar(64);not null;index" json:"result_wiki_page_id"`
	SourceFingerprint string    `gorm:"type:varchar(80);not null;index" json:"source_fingerprint"`
	UpdatedAt         time.Time `json:"updated_at"`
}
