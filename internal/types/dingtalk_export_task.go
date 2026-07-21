package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	DingTalkExportTaskStatusPending   = "pending"
	DingTalkExportTaskStatusSucceeded = "succeeded"
	DingTalkExportTaskStatusFailed    = "failed"
)

// DingTalkExportTask tracks an official DingTalk Markdown export job until
// DingTalk pushes the dingdoc_export_finish event with the download URL.
type DingTalkExportTask struct {
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`

	TenantID     uint64 `json:"tenant_id" gorm:"index"`
	DataSourceID string `json:"data_source_id" gorm:"type:varchar(36);index"`
	SyncLogID    string `json:"sync_log_id" gorm:"type:varchar(36);index"`

	ExternalID       string `json:"external_id" gorm:"index"`
	SourceResourceID string `json:"source_resource_id"`
	WorkspaceID      string `json:"workspace_id" gorm:"index"`
	NodeID           string `json:"node_id" gorm:"index"`
	DentryUUID       string `json:"dentry_uuid" gorm:"index"`
	TaskID           string `json:"task_id" gorm:"uniqueIndex;not null"`

	Title     string `json:"title"`
	FileName  string `json:"file_name"`
	SourceURL string `json:"source_url"`

	Status       string     `json:"status" gorm:"type:varchar(32);index"`
	EventID      string     `json:"event_id" gorm:"index"`
	ExportURL    string     `json:"export_url"`
	ErrorCode    string     `json:"error_code"`
	ErrorMessage string     `json:"error_message"`
	FinishedAt   *time.Time `json:"finished_at"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (t *DingTalkExportTask) TableName() string {
	return "dingtalk_export_tasks"
}

func (t *DingTalkExportTask) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.Status == "" {
		t.Status = DingTalkExportTaskStatusPending
	}
	return nil
}
