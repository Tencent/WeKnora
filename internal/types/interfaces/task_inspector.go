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
}
