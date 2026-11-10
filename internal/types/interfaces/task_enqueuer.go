package interfaces

import "github.com/hibiken/asynq"

// TaskEnqueuer abstracts task enqueueing. *asynq.Client satisfies this interface.
// For Lite mode (no Redis), a synchronous implementation dispatches tasks inline.
type TaskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}
