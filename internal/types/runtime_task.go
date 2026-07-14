package types

import "time"

// RuntimeTaskState is the stable operator-facing task lifecycle. It mirrors
// the durable states exposed by asynq while keeping the HTTP API independent
// from the queue library's Go enum.
type RuntimeTaskState string

const (
	RuntimeTaskPending   RuntimeTaskState = "pending"
	RuntimeTaskActive    RuntimeTaskState = "active"
	RuntimeTaskScheduled RuntimeTaskState = "scheduled"
	RuntimeTaskRetry     RuntimeTaskState = "retry"
	RuntimeTaskArchived  RuntimeTaskState = "archived"
	RuntimeTaskCompleted RuntimeTaskState = "completed"
)

func (s RuntimeTaskState) Valid() bool {
	switch s {
	case RuntimeTaskPending, RuntimeTaskActive, RuntimeTaskScheduled,
		RuntimeTaskRetry, RuntimeTaskArchived, RuntimeTaskCompleted:
		return true
	default:
		return false
	}
}

// RuntimeTaskAction is returned by the backend for every task. The frontend
// renders only these actions instead of inferring safety from the state.
type RuntimeTaskAction string

const (
	RuntimeTaskActionCancel RuntimeTaskAction = "cancel"
	RuntimeTaskActionRunNow RuntimeTaskAction = "run_now"
	RuntimeTaskActionDelete RuntimeTaskAction = "delete"
)

// RuntimeTaskInfo is the safe SystemAdmin projection of one queue task.
// Raw payloads and results are deliberately excluded because they may contain
// document content, signed object URLs, or connector credentials.
type RuntimeTaskInfo struct {
	ID              string              `json:"id"`
	Queue           string              `json:"queue"`
	Type            string              `json:"type"`
	State           RuntimeTaskState    `json:"state"`
	AllowedActions  []RuntimeTaskAction `json:"allowed_actions"`
	LastError       string              `json:"last_error,omitempty"`
	LastFailedAt    *time.Time          `json:"last_failed_at,omitempty"`
	NextProcessAt   *time.Time          `json:"next_process_at,omitempty"`
	StartedAt       *time.Time          `json:"started_at,omitempty"`
	CompletedAt     *time.Time          `json:"completed_at,omitempty"`
	Deadline        *time.Time          `json:"deadline,omitempty"`
	EnqueuedAt      *time.Time          `json:"enqueued_at,omitempty"`
	Retried         int                 `json:"retried"`
	MaxRetry        int                 `json:"max_retry"`
	IsOrphaned      bool                `json:"is_orphaned,omitempty"`
	Worker          string              `json:"worker,omitempty"`
	TenantID        uint64              `json:"tenant_id,omitempty"`
	KnowledgeBaseID string              `json:"knowledge_base_id,omitempty"`
	KnowledgeID     string              `json:"knowledge_id,omitempty"`
	TaskID          string              `json:"task_id,omitempty"`
	SourceID        string              `json:"source_id,omitempty"`
	TargetID        string              `json:"target_id,omitempty"`
	SourceKBID      string              `json:"source_kb_id,omitempty"`
	TargetKBID      string              `json:"target_kb_id,omitempty"`
	DataSourceID    string              `json:"data_source_id,omitempty"`
	SyncLogID       string              `json:"sync_log_id,omitempty"`
	KnowledgeCount  int                 `json:"knowledge_count,omitempty"`
}

func (t RuntimeTaskInfo) Allows(action RuntimeTaskAction) bool {
	for _, allowed := range t.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}
