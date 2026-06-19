package types

import "time"

type TaskJobKind string

const (
	TaskJobKindUpload         TaskJobKind = "upload"
	TaskJobKindReparse        TaskJobKind = "reparse"
	TaskJobKindMove           TaskJobKind = "move"
	TaskJobKindDelete         TaskJobKind = "delete"
	TaskJobKindFAQImport      TaskJobKind = "faq_import"
	TaskJobKindKBClone        TaskJobKind = "kb_clone"
	TaskJobKindDatasourceSync TaskJobKind = "datasource_sync"
	TaskJobKindRebuildWiki    TaskJobKind = "rebuild_wiki"
)

type TaskJobOrigin string

const (
	TaskJobOriginUser     TaskJobOrigin = "user"
	TaskJobOriginInternal TaskJobOrigin = "internal"
)

type TaskJobState string

const (
	TaskJobStateQueued     TaskJobState = "queued"
	TaskJobStateProcessing TaskJobState = "processing"
	TaskJobStateFinalizing TaskJobState = "finalizing"
	TaskJobStateSucceeded  TaskJobState = "succeeded"
	TaskJobStateFailed     TaskJobState = "failed"
	TaskJobStateCanceled   TaskJobState = "canceled"
)

type TaskErrorClass string

const (
	TaskErrorClassRetryable     TaskErrorClass = "retryable"
	TaskErrorClassTerminal      TaskErrorClass = "terminal"
	TaskErrorClassCanceled      TaskErrorClass = "canceled"
	TaskErrorClassEnqueueFailed TaskErrorClass = "enqueue_failed"
)

// TaskJob is the durable user-facing task ledger. One row represents a
// logical task a user can understand and operate on, while TaskExecution rows
// hold the concrete asynq/lite execution attempts behind it.
type TaskJob struct {
	JobID          string         `json:"job_id" gorm:"primaryKey;type:varchar(64)"`
	TenantID       uint64         `json:"tenant_id" gorm:"not null;index:idx_task_jobs_tenant_state_created,priority:1;index:idx_task_jobs_tenant_kind_state,priority:1;index:idx_task_jobs_tenant_creator_created,priority:1;index:idx_task_jobs_scope_attempt,priority:1;index:idx_task_jobs_related_state,priority:1"`
	CreatedBy      string         `json:"created_by" gorm:"type:varchar(64);default:'';index:idx_task_jobs_tenant_creator_created,priority:2"`
	Kind           TaskJobKind    `json:"kind" gorm:"type:varchar(32);not null;index:idx_task_jobs_tenant_kind_state,priority:2"`
	Origin         TaskJobOrigin  `json:"origin" gorm:"type:varchar(8);not null;default:user"`
	DisplayName    string         `json:"display_name" gorm:"type:varchar(255);default:''"`
	Scope          string         `json:"scope" gorm:"type:varchar(32);not null;index:idx_task_jobs_scope_attempt,priority:2"`
	ScopeID        string         `json:"scope_id" gorm:"type:varchar(64);not null;index:idx_task_jobs_scope_attempt,priority:3"`
	RelatedID      string         `json:"related_id" gorm:"type:varchar(64);default:'';index:idx_task_jobs_related_state,priority:2"`
	ProcessAttempt int            `json:"process_attempt" gorm:"not null;default:0;index:idx_task_jobs_scope_attempt,priority:4"`
	State          TaskJobState   `json:"state" gorm:"type:varchar(16);not null;default:queued;index:idx_task_jobs_tenant_state_created,priority:2;index:idx_task_jobs_tenant_kind_state,priority:3;index:idx_task_jobs_related_state,priority:3"`
	Metadata       JSON           `json:"metadata" gorm:"type:jsonb;not null;default:'{}'"`
	ReplaySpec     JSON           `json:"-" gorm:"type:jsonb;not null;default:'{}'"`
	LastErrorClass TaskErrorClass `json:"last_error_class" gorm:"type:varchar(24);default:''"`
	LastError      string         `json:"last_error" gorm:"type:text;default:''"`
	FailedTaskType string         `json:"failed_task_type" gorm:"type:varchar(64);default:''"`
	FailedTaskID   string         `json:"failed_task_id" gorm:"type:varchar(64);default:''"`
	CreatedAt      time.Time      `json:"created_at" gorm:"index:idx_task_jobs_tenant_state_created,priority:3,sort:desc;index:idx_task_jobs_tenant_creator_created,priority:3,sort:desc"`
	UpdatedAt      time.Time      `json:"updated_at"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
}

func (TaskJob) TableName() string { return "task_jobs" }

func TaskJobIsTerminal(state TaskJobState) bool {
	return state == TaskJobStateSucceeded ||
		state == TaskJobStateFailed ||
		state == TaskJobStateCanceled
}
