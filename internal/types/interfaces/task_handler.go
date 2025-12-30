package interfaces

import (
	"context"

	"github.com/hibiken/asynq"
)

// TaskHandler is a interface for handling asynchronous tasks
type TaskHandler interface {
	// Handle handles the task
	Handle(ctx context.Context, t *asynq.Task) error
}
