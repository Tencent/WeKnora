package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TaskPendingOpsRepository persists rows for the generic task pending
// queue (`task_pending_ops`). The queue is the durable replacement for
// the Redis-list-backed wiki:pending:<kbID> queue. It is intentionally
// stateless about consumer semantics: the (TaskType, Scope, ScopeID)
// tuple is the only routing primitive the repository understands;
// deduplication, batching, and retry policy live in the consumer.
//
// Concurrency model in this revision: PeekBatch does NOT take row
// locks. Consumers are expected to enforce per-(scope_id) serialization
// out-of-band (wiki ingest does this via Redis SetNX wiki:active:<kbID>).
// The reserved `claimed_at` column lets a future revision adopt
// pessimistic locking without a schema change.
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
