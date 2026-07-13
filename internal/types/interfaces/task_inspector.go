package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TaskInspector abstracts queue inspection / cancellation against the
// task backend. It is best-effort: implementations may scan a finite
// number of tasks per call and return whatever count they could
// affect. Lite mode (no Redis) ships a no-op implementation because
// SyncTaskExecutor dispatches inline goroutines that cannot be
// dequeued before they start.
//
// Use cases today: user-initiated cancel of an in-progress knowledge
// parse, which must remove downstream multimodal / post-process /
// question / summary tasks already enqueued against the same
// knowledge_id, plus signal active workers to stop at their next
// checkpoint.
type TaskInspector interface {
	// CancelTasksForKnowledge removes pending/scheduled/retry tasks
	// whose payload references the given knowledge ID, and signals
	// active workers running such tasks to stop. Returns rough
	// counts of (deletedFromQueue, activeCancelled) for observability.
	// Errors are returned but callers should treat the operation as
	// best-effort: the row-level abort flag remains the source of
	// truth, this just prevents wasted work.
	CancelTasksForKnowledge(ctx context.Context, knowledgeID string) (deleted int, cancelled int, err error)

	// HasQueuedTasksForKnowledge reports whether any pending / scheduled
	// / retry / active task referencing the given knowledge ID still
	// lives in the queue backend. It is the read-only counterpart of
	// CancelTasksForKnowledge: the housekeeping sweep calls it before
	// flipping a long-idle "processing"/"finalizing" row to "failed" so
	// it can tell a genuinely orphaned row (no task anywhere) from one
	// whose enrichment subtasks are merely backlogged behind a busy
	// queue (no span heartbeat yet because no worker has picked them up).
	//
	// Best-effort and short-circuiting: it returns true as soon as the
	// first match is seen. On backend error it returns (false, err);
	// callers decide the fail-safe direction. Lite mode (no Redis)
	// always returns false — inline executors never queue, so the
	// span/updated_at checks remain authoritative there.
	HasQueuedTasksForKnowledge(ctx context.Context, knowledgeID string) (bool, error)

	// QueueStats returns a read-only depth snapshot for every queue this
	// application enqueues into, for the System Admin runtime dashboard.
	//
	// The `supported` bool reports whether queue inspection is available
	// on the current backend: true in asynq/Redis mode, false in Lite
	// mode (no Redis) where tasks run inline and no queue exists. Callers
	// use it to render an "unavailable in this deployment" state rather
	// than an empty table.
	//
	// Best-effort: a per-queue backend error is logged and that queue is
	// surfaced as a zeroed row (so the full lane set stays visible) rather
	// than failing the whole call.
	QueueStats(ctx context.Context) (stats []types.QueueStat, supported bool, err error)

	// WorkerServerStats returns live asynq server heartbeats across all
	// replicas. The runtime dashboard uses them to aggregate actual cluster
	// capacity and busy workers for each configured pool.
	WorkerServerStats(ctx context.Context) (stats []types.WorkerServerStat, supported bool, err error)
}
