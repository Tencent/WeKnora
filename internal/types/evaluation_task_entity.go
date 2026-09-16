package types

import (
	"time"

	"gorm.io/gorm"
)

// EvaluationTaskEntity is the database representation of one evaluation task.
// API serialization continues to use EvaluationTask and EvaluationDetail.
type EvaluationTaskEntity struct {
	ID        string           `json:"id" gorm:"type:varchar(128);primaryKey"`
	TenantID  uint64           `json:"tenant_id" gorm:"not null;index"`
	DatasetID string           `json:"dataset_id" gorm:"type:varchar(255);not null"`
	Status    EvaluationStatue `json:"status" gorm:"not null;index"`
	StartTime time.Time        `json:"start_time" gorm:"not null"`
	EndTime   *time.Time       `json:"end_time,omitempty"`
	Total     int              `json:"total" gorm:"not null;default:0"`
	Finished  int              `json:"finished" gorm:"not null;default:0"`
	ErrMsg    string           `json:"err_msg" gorm:"type:text;not null;default:''"`
	DeletedAt gorm.DeletedAt   `json:"-" gorm:"index"`
	CreatedAt time.Time        `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time        `json:"updated_at" gorm:"not null"`

	CleanupErrors  JSON `json:"cleanup_errors" gorm:"type:jsonb;not null;default:'[]'"`
	Params         JSON `json:"params" gorm:"type:jsonb;not null;default:'{}'"`
	Metric         JSON `json:"metric,omitempty" gorm:"type:jsonb"`
	RuntimeMetrics JSON `json:"runtime_metrics,omitempty" gorm:"type:jsonb"`

	// Frozen experiment provenance (M3). All four stay nil for pre-M3 tasks:
	// null provenance is reported as experiment=null and
	// provenance_complete=false instead of being backfilled.
	DatasetVersionID     *string `json:"dataset_version_id,omitempty" gorm:"type:varchar(64)"`
	DatasetContentSHA256 *string `json:"dataset_content_sha256,omitempty" gorm:"type:char(64)"`
	ExperimentSnapshot   JSON    `json:"-" gorm:"type:jsonb"`
	ExperimentSHA256     *string `json:"experiment_sha256,omitempty" gorm:"type:char(64)"`

	TemporaryKnowledgeBaseID string `json:"-" gorm:"column:temporary_kb_id;type:varchar(64);not null"`
	TemporaryKnowledgeID     string `json:"-" gorm:"type:varchar(64)"`

	OwnerID        string     `json:"-" gorm:"type:varchar(36);not null"`
	LeaseExpiresAt *time.Time `json:"-" gorm:"index"`
	HeartbeatAt    time.Time  `json:"-" gorm:"not null"`
	Version        uint64     `json:"version" gorm:"not null;default:1"`

	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`

	// Labels is a read-side projection populated after task pagination.
	Labels []string `json:"labels" gorm:"-"`
}

// TableName binds EvaluationTaskEntity to the evaluation task table.
func (EvaluationTaskEntity) TableName() string {
	return "evaluation_tasks"
}

// EvaluationTaskStartCommand conditionally moves a pending task into the
// running state for its current owner.
type EvaluationTaskStartCommand struct {
	TenantID        uint64
	TaskID          string
	OwnerID         string
	ExpectedVersion uint64
	Now             time.Time
	LeaseExpiresAt  time.Time
}

// EvaluationTaskHeartbeatCommand renews the lease of one running task without
// changing its business snapshot version.
type EvaluationTaskHeartbeatCommand struct {
	TenantID       uint64
	TaskID         string
	OwnerID        string
	Now            time.Time
	LeaseExpiresAt time.Time
}

// EvaluationTaskClaimExpiredCommand assigns a bounded batch of expired active
// tasks to one recovery owner.
type EvaluationTaskClaimExpiredCommand struct {
	OwnerID        string
	Now            time.Time
	LeaseExpiresAt time.Time
	Limit          int
}

// EvaluationTaskProgressCommand conditionally publishes one complete progress
// snapshot and renews the running task lease.
type EvaluationTaskProgressCommand struct {
	TenantID        uint64
	TaskID          string
	OwnerID         string
	ExpectedVersion uint64
	Total           int
	Finished        int
	Metric          JSON
	RuntimeMetrics  JSON
	Now             time.Time
	LeaseExpiresAt  time.Time
}

// EvaluationTaskKnowledgeCommand conditionally records the temporary
// Knowledge resource created by a running task.
type EvaluationTaskKnowledgeCommand struct {
	TenantID             uint64
	TaskID               string
	OwnerID              string
	ExpectedVersion      uint64
	TemporaryKnowledgeID string
	UpdatedAt            time.Time
}

// EvaluationTaskTerminalCommand conditionally publishes the complete terminal
// snapshot and releases the task lease.
type EvaluationTaskTerminalCommand struct {
	TenantID        uint64
	TaskID          string
	OwnerID         string
	ExpectedVersion uint64
	Status          EvaluationStatue
	EndTime         time.Time
	ErrMsg          string
	CleanupErrors   JSON
	Metric          JSON
	RuntimeMetrics  JSON
}

// EvaluationTaskCancelCommand records the first persistent user-cancel
// request for one active task. It needs no owner and never increments the
// execution version.
type EvaluationTaskCancelCommand struct {
	TenantID uint64
	TaskID   string
	Now      time.Time
}

// EvaluationTaskListInput carries the validated list parameters accepted by
// the API: an optional numeric status filter, a bounded page size, and the
// opaque keyset cursor.
type EvaluationTaskListInput struct {
	Status           *EvaluationStatue
	DatasetID        string
	DatasetVersionID string
	ModelID          string
	StartedFrom      *time.Time
	StartedTo        *time.Time
	Labels           []string
	PageSize         int
	Cursor           string
}

// EvaluationTaskListQuery selects one keyset page of tenant tasks ordered by
// (start_time DESC, id DESC). StartBefore and IDBefore form the exclusive
// keyset boundary decoded from the client cursor.
type EvaluationTaskListQuery struct {
	Status           *EvaluationStatue
	DatasetID        string
	DatasetVersionID string
	ModelID          string
	StartedFrom      *time.Time
	StartedTo        *time.Time
	Labels           []string
	StartBefore      *time.Time
	IDBefore         string
	Limit            int
}

// EvaluationTaskListPage carries one page and the opaque next cursor.
type EvaluationTaskListPage struct {
	Items      []*EvaluationTaskEntity
	NextCursor string
}
