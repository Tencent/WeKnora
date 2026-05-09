package types

import (
	"encoding/json"
	"time"
)

// TaskPendingOp is one entry in the generic task pending queue
// (`task_pending_ops`). The queue is the durable replacement for the
// Redis-list-backed `wiki:pending:<kbID>` queue: rows survive restarts
// and are not subject to TTL eviction.
//
// The (TaskType, Scope, ScopeID) tuple is the queue identity. A consumer
// pulls a batch with PeekBatch on that tuple, deduplicates by DedupKey
// service-side if it cares, processes the ops, then DeleteByIDs the
// consumed rows. FailCount is incremented per-row by IncrFailCount and
// the consumer dead-letters the row once the count exceeds a service-
// defined cap.
type TaskPendingOp struct {
	// Auto-increment row id. Used by PeekBatch ordering and by
	// DeleteByIDs / IncrFailCount as the row key.
	ID int64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// Tenant scope mirrored from the enclosing object so per-tenant
	// retention / quota queries don't have to join.
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Free-form task identifier (e.g. "wiki:ingest"). Should match an
	// asynq task type when applicable, but the queue itself doesn't
	// enforce that — it's just a string.
	TaskType string `json:"task_type" gorm:"type:varchar(64)"`
	// Logical scope, e.g. "knowledge_base" / "knowledge" / "tenant".
	// Read together with ScopeID.
	Scope string `json:"scope" gorm:"type:varchar(32)"`
	// Identifier within the scope (e.g. kbID for scope="knowledge_base").
	ScopeID string `json:"scope_id" gorm:"type:varchar(64)"`
	// Operation kind. Service-defined: e.g. "ingest" / "retract" for
	// wiki, but other consumers can use whatever vocabulary they like.
	Op string `json:"op" gorm:"type:varchar(32)"`
	// Optional service-defined deduplication key. The consumer is
	// responsible for collapsing equivalent ops within a peeked batch
	// (the queue itself does NOT enforce uniqueness — multiple rows with
	// the same DedupKey can coexist; the consumer chooses which wins).
	DedupKey string `json:"dedup_key" gorm:"type:varchar(128);default:''"`
	// JSON-serialized op payload. Schema is consumer-defined; the queue
	// stores it verbatim. Use json.RawMessage to avoid double-decode.
	Payload json.RawMessage `json:"payload" gorm:"type:jsonb;default:'{}'"`
	// In-batch retry counter. Reset to 0 on successful consume.
	FailCount int `json:"fail_count" gorm:"default:0"`
	// Server-side enqueue time. NOT used for ordering (id is the cursor)
	// but useful for ops queries like "rows older than 1h that never
	// drained".
	EnqueuedAt time.Time `json:"enqueued_at"`
	// Optional claim timestamp for future locking workflows. Not used
	// in the current revision: consumers rely on external mutual
	// exclusion (e.g. wiki:active:<kbID> Redis SetNX). Reserved column
	// so future no-lock parallel workers can flip it inside a row-level
	// lock without another migration.
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
}

// TableName binds TaskPendingOp to the `task_pending_ops` table.
func (TaskPendingOp) TableName() string {
	return "task_pending_ops"
}
