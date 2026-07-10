package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// TaskPendingOpsRepository persists rows for the generic task pending
// queue (`task_pending_ops`). The queue is the durable replacement for
// the Redis-list-backed wiki:pending:<kbID> queue. It is intentionally
// stateless about consumer semantics: the (TaskType, Scope, ScopeID)
// tuple is the only routing primitive the repository understands;
// deduplication, batching, and retry policy live in the consumer.
//
// Concurrency model: two consumption primitives coexist.
//   - PeekBatch does NOT take row locks; consumers enforce per-(scope_id)
//     serialization out-of-band (e.g. an external Redis lock). This is the
//     original primitive and is still used by single-process (Lite) mode.
//   - ClaimBatch DOES take row locks (SELECT ... FOR UPDATE SKIP LOCKED on
//     Postgres) and marks rows with claimed_at, so multiple concurrent
//     consumers of the same tuple pull DISJOINT rows without an external
//     lock. This is what lets the wiki pipeline drop its exclusive per-KB
//     batch lock and spread one KB's backlog across the whole worker pool.
type TaskPendingOpsRepository interface {
	// Enqueue inserts a single op. The caller fills in TenantID, TaskType,
	// Scope, ScopeID, Op, DedupKey, Payload; ID, FailCount, EnqueuedAt
	// are server-side defaults.
	Enqueue(ctx context.Context, op *types.TaskPendingOp) error

	// PeekBatch returns up to `limit` rows for the given queue tuple,
	// ordered by id ASC (FIFO within the queue). Rows are NOT removed —
	// callers must DeleteByIDs once the ops have been processed (or
	// IncrFailCount and leave them for the next pass).
	PeekBatch(ctx context.Context, taskType, scope, scopeID string, limit int) ([]*types.TaskPendingOp, error)

	// ClaimBatch atomically claims eligible rows for the tuple, grouped by
	// dedup_key, and returns them with claimed_at set to now. `limit`
	// counts DISTINCT dedup_keys (documents), NOT rows: ALL eligible rows
	// sharing a chosen dedup_key are claimed together so a document with
	// several queued ops is never split across two concurrent batches.
	// A row is eligible when it is unclaimed (claimed_at IS NULL) or its
	// claim is stale (claimed_at < staleBefore) — the latter recovers rows
	// abandoned by a crashed worker. On Postgres the per-key anchor row is
	// locked with FOR UPDATE SKIP LOCKED so concurrent claimers take
	// disjoint key sets without blocking or double-claiming.
	//
	// Claimed rows are NOT removed: the consumer must DeleteByIDs on
	// success, or ReleaseByIDs to hand a still-retryable row back to the
	// pool (otherwise it stays claimed until staleBefore elapses).
	ClaimBatch(ctx context.Context, taskType, scope, scopeID string, limit int, staleBefore time.Time) ([]*types.TaskPendingOp, error)

	// ReleaseByIDs clears claimed_at (back to NULL) for the given rows so
	// a claimed-but-not-consumed row becomes immediately eligible for the
	// next ClaimBatch. No-op for empty input. Used by the wiki retry path
	// to re-queue a transiently-failed op without waiting for its claim to
	// go stale. Harmless on rows that were never claimed (Lite mode).
	ReleaseByIDs(ctx context.Context, ids []int64) error

	// DeleteByIDs removes the given rows. No-op for empty input. Used
	// to consume a successfully-processed batch, and to drop ops that
	// have been moved to task_dead_letters.
	DeleteByIDs(ctx context.Context, ids []int64) error

	// IncrFailCount increments fail_count for one row and returns the
	// new value. Returns (0, nil) if the row does not exist (race with
	// DeleteByIDs is benign).
	IncrFailCount(ctx context.Context, id int64) (int, error)

	// PendingCount returns the number of rows currently queued for the
	// given tuple. Cheap (covered by idx_task_pending_ops_scope) and
	// used by the wiki ingest follow-up scheduler.
	PendingCount(ctx context.Context, taskType, scope, scopeID string) (int64, error)

	// DeleteByDedupKey removes rows for the tuple whose DedupKey
	// matches. If `op` is non-empty, only rows with that exact op are
	// removed (this lets wiki ingest scrub queued "ingest" ops while
	// preserving "retract" ops for the same knowledge — retract is
	// still needed to clean up wiki pages after the source doc is
	// deleted). If `op` is empty, every matching row is removed
	// regardless of op.
	DeleteByDedupKey(ctx context.Context, taskType, scope, scopeID, dedupKey, op string) error
}

// TaskDeadLetterRepository persists rows for the generic task dead-letter
// archive (`task_dead_letters`). Two writers exist: the asynq
// dead-letter middleware (one row per archived asynq task), and the
// service-level retry handlers (one row per in-batch op that exhausted
// its consumer-defined retry budget — wiki ingest is the current case).
//
// Reads are operator-driven: list by scope to triage a single KB,
// list by task_type for cross-KB symptom hunting. No TTL.
type TaskDeadLetterRepository interface {
	// Insert records one dead letter. Best-effort caller: the asynq
	// middleware ignores the error so a failed insert never masks the
	// underlying task error.
	Insert(ctx context.Context, dl *types.TaskDeadLetter) error

	// ListByScope returns dead letters for the given scope tuple,
	// newest-first, paginated by failed-id cursor. `cursor` is the
	// stringified id of the oldest entry from the previous page; "" =
	// from the newest. Empty nextCursor = end of stream. `limit` is
	// clamped to [1, 200].
	ListByScope(ctx context.Context, scope, scopeID, cursor string, limit int) ([]*types.TaskDeadLetter, string, error)

	// ListByTaskType returns dead letters for the given task_type,
	// newest-first, with the same cursor semantics as ListByScope.
	ListByTaskType(ctx context.Context, taskType, cursor string, limit int) ([]*types.TaskDeadLetter, string, error)

	// DeleteByID drops a single dead letter (e.g. after operators have
	// requeued the task manually).
	DeleteByID(ctx context.Context, id int64) error
}
