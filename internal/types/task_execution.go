package types

import "time"

type TaskExecutionState string

const (
	TaskExecutionStateQueued      TaskExecutionState = "queued"
	TaskExecutionStateActive      TaskExecutionState = "active"
	TaskExecutionStateRetrying    TaskExecutionState = "retrying"
	TaskExecutionStateRescheduled TaskExecutionState = "rescheduled"
	TaskExecutionStateSucceeded   TaskExecutionState = "succeeded"
	TaskExecutionStateFailed      TaskExecutionState = "failed"
	TaskExecutionStateCanceled    TaskExecutionState = "canceled"
)

// TaskExecution records one concrete execution ID for a TaskJob. Asynq
// automatic retries reuse the same execution; user-triggered retry creates a
// new execution and points RetryOf at the previous failed/canceled one.
type TaskExecution struct {
	ExecutionID              string             `json:"execution_id" gorm:"primaryKey;type:varchar(64)"`
	JobID                    string             `json:"job_id" gorm:"type:varchar(64);not null;index:idx_task_executions_job_attempt_enqueued,priority:1;index:idx_task_executions_job_state,priority:1"`
	ProcessAttempt           int                `json:"process_attempt" gorm:"not null;default:0;index:idx_task_executions_job_attempt_enqueued,priority:2"`
	TaskType                 string             `json:"task_type" gorm:"type:varchar(64);not null"`
	Queue                    string             `json:"queue" gorm:"type:varchar(32);default:''"`
	State                    TaskExecutionState `json:"state" gorm:"type:varchar(16);not null;default:queued;index:idx_task_executions_job_state,priority:2;index:idx_task_executions_state_enqueued,priority:1"`
	RetryCount               int                `json:"retry_count" gorm:"not null;default:0"`
	ErrorClass               TaskErrorClass     `json:"error_class" gorm:"type:varchar(24);default:''"`
	LastError                string             `json:"last_error" gorm:"type:text;default:''"`
	RetryOf                  string             `json:"retry_of" gorm:"type:varchar(64);default:''"`
	RescheduledToExecutionID string             `json:"rescheduled_to_execution_id,omitempty" gorm:"type:varchar(64);default:''"`
	EnqueuedAt               time.Time          `json:"enqueued_at" gorm:"index:idx_task_executions_job_attempt_enqueued,priority:3;index:idx_task_executions_state_enqueued,priority:2"`
	DispatchedAt             *time.Time         `json:"dispatched_at,omitempty"`
	StartedAt                *time.Time         `json:"started_at,omitempty"`
	FinishedAt               *time.Time         `json:"finished_at,omitempty"`
}

func (TaskExecution) TableName() string { return "task_executions" }

func TaskExecutionIsTerminal(state TaskExecutionState) bool {
	return state == TaskExecutionStateSucceeded ||
		state == TaskExecutionStateFailed ||
		state == TaskExecutionStateCanceled ||
		state == TaskExecutionStateRescheduled
}
