package interfaces

import "context"

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
}
