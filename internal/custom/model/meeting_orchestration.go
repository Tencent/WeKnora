package model

import "time"

type MeetingOrchestrationJob struct {
	ID                string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	OwnerScopeID      string     `gorm:"type:varchar(128);not null;index" json:"owner_scope_id"`
	Status            string     `gorm:"type:varchar(24);not null;index" json:"status"`
	Stage             string     `gorm:"type:varchar(32)" json:"stage,omitempty"`
	Progress          int        `gorm:"not null;default:0" json:"progress"`
	SourceFingerprint string     `gorm:"type:varchar(80);index" json:"source_fingerprint"`
	ResultWikiPageID  string     `gorm:"type:varchar(64);index" json:"result_wiki_page_id,omitempty"`
	ErrorCode         string     `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage      string     `gorm:"type:text" json:"error_message,omitempty"`
	PromptVersion     string     `gorm:"type:varchar(96)" json:"prompt_version,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type MeetingOrchestrationCurrent struct {
	OwnerScopeID      string    `gorm:"type:varchar(128);primaryKey" json:"owner_scope_id"`
	JobID             string    `gorm:"type:varchar(36);not null;index" json:"job_id"`
	ResultWikiPageID  string    `gorm:"type:varchar(64);not null;index" json:"result_wiki_page_id"`
	SourceFingerprint string    `gorm:"type:varchar(80);not null;index" json:"source_fingerprint"`
	UpdatedAt         time.Time `json:"updated_at"`
}
