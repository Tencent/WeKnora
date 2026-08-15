// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"

	asynqcontext "github.com/hibiken/asynq/internal/context"
)

// GetTaskID extracts a task ID from a context, if any.
//
// ID of a task is guaranteed to be unique.
// ID of a task doesn't change if the task is being retried.
func GetTaskID(ctx context.Context) (id string, ok bool) {
	return asynqcontext.GetTaskID(ctx)
}

// GetRetryCount extracts retry count from a context, if any.
//
// Return value n indicates the number of times associated task has been
// retried so far.
func GetRetryCount(ctx context.Context) (n int, ok bool) {
	return asynqcontext.GetRetryCount(ctx)
}

// GetMaxRetry extracts maximum retry from a context, if any.
//
// Return value n indicates the maximum number of times the associated task
// can be retried if ProcessTask returns a non-nil error.
func GetMaxRetry(ctx context.Context) (n int, ok bool) {
	return asynqcontext.GetMaxRetry(ctx)
}

// GetQueueName extracts queue name from a context, if any.
//
// Return value queue indicates which queue the task was pulled from.
func GetQueueName(ctx context.Context) (queue string, ok bool) {
	return asynqcontext.GetQueueName(ctx)
}
