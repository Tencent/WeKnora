package types

import (
	"encoding/json"
	"time"
)

// TaskDeadLetter is one row in the `task_dead_letters` archive: a
// permanent record of a task whose retry budget was exhausted. The table
// is populated from two distinct paths:
//
//  1. The asynq dead-letter middleware
//     (internal/middleware/asynq_dead_letter.go) inserts a row whenever
//     an asynq task's retry count equals its MaxRetry on the way out.
//     This is the catch-all that covers every registered task type
//     uniformly without per-handler instrumentation.
//
//  2. Service-level retry handlers insert directly when an in-batch
//     retry counter exceeds the service-defined cap. Wiki ingest is the
//     primary example: per-document ops accumulate fail_count in
//     `task_pending_ops`, and once the count exceeds wikiMaxFailRetries
//     the op is moved here instead of being requeued.
//
// Operators query by (Scope, ScopeID) — e.g. "all dead letters for KB
// abc" — or by TaskType — e.g. "all summary:generation failures in the
// last 24h". The table has no TTL; rows are kept until manually pruned.
type TaskDeadLetter struct {
	// Auto-increment row id.
	ID int64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// Tenant scope mirrored from the original task payload (best-effort:
	// if the middleware can't decode the payload, this falls back to 0).
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Task identifier (e.g. "wiki:ingest", "summary:generation").
	TaskType string `json:"task_type" gorm:"type:varchar(64)"`
	// Logical scope, mirrors task_pending_ops.scope. Falls back to
	// "unknown" when the middleware doesn't recognize the task type's
	// payload.
	Scope string `json:"scope" gorm:"type:varchar(32)"`
	// Identifier within scope, e.g. kbID for scope="knowledge_base".
	ScopeID string `json:"scope_id" gorm:"type:varchar(64)"`
	// Optional secondary identifier. Wiki ingest uses this for the
	// knowledge_id so per-document failures cluster around the source
	// document; other consumers can leave it empty.
	RelatedID string `json:"related_id" gorm:"type:varchar(64);default:''"`
	// Raw task payload at the time of failure. For asynq tasks this is
	// the verbatim asynq.Task.Payload(); for service-level dead letters
	// the consumer chooses what to record.
	Payload json.RawMessage `json:"payload" gorm:"type:jsonb"`
	// String form of the final error. Long stack traces are kept verbatim.
	LastError string `json:"last_error" gorm:"type:text;default:''"`
	// Total attempt count when the dead-letter record was written.
	FailCount int `json:"fail_count"`
	// Server-side write time.
	FailedAt time.Time `json:"failed_at"`
}

// TableName binds TaskDeadLetter to the `task_dead_letters` table.
func (TaskDeadLetter) TableName() string {
	return "task_dead_letters"
}

// Standard scope literals. Repositories accept any string, but consumers
// should prefer these so SQL queries (run from the ops console) don't
// have to guess at variations.
const (
	TaskScopeKnowledgeBase = "knowledge_base"
	TaskScopeKnowledge     = "knowledge"
	TaskScopeTenant        = "tenant"
	TaskScopeUnknown       = "unknown"
)
