package interfaces

import (
	"context"

	"github.com/hibiken/asynq"
)

type Extracter interface {
	Extract(ctx context.Context, t *asynq.Task) error
}
