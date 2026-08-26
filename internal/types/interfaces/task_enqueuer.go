package interfaces

import "github.com/hibiken/asynq"

// TaskEnqueuer abstracts task enqueueing. *asynq.Client satisfies this interface.
// For Lite mode (no Redis), a synchronous implementation dispatches tasks inline.
type TaskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// TaskRecoveryController exposes the minimum task-state operations required
// by durable outbox recovery. Redis mode is backed by *asynq.Inspector; Lite
// mode reports its in-process active task IDs through SyncTaskExecutor.
type TaskRecoveryController interface {
	GetTaskInfo(queue, id string) (*asynq.TaskInfo, error)
	DeleteTask(queue, id string) error
}
