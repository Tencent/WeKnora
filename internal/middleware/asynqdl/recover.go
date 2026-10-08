package asynqdl

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/hibiken/asynq"
)

// RecoverMiddleware turns a handler panic into an ordinary task error.
// asynq recovers panics itself, but only in its processor, above every
// middleware — so a panic skips the dead-letter callback and a document on
// its last attempt stays "processing" until housekeeping. Install it right
// after MiddlewareWithCallback so that callback sees the error.
func RecoverMiddleware() asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			return CallRecovered(ctx, t, next.ProcessTask)
		})
	}
}

// CallRecovered runs handler, converting a panic into an error.
func CallRecovered(
	ctx context.Context, t *asynq.Task, handler func(context.Context, *asynq.Task) error,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf(ctx, "task %s panicked: %v\n%s", t.Type(), r, debug.Stack())
			err = fmt.Errorf("task %s panicked: %v", t.Type(), r)
		}
	}()
	return handler(ctx, t)
}
