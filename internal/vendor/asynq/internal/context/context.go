// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package context

import (
	"context"
	"time"

	"github.com/hibiken/asynq/internal/base"
)

// A taskMetadata holds task scoped data to put in context.
type taskMetadata struct {
	id         string
	maxRetry   int
	retryCount int
	qname      string
}

// ctxKey type is unexported to prevent collisions with context keys defined in
// other packages.
type ctxKey int

// metadataCtxKey is the context key for the task metadata.
// Its value of zero is arbitrary.
const metadataCtxKey ctxKey = 0

// New returns a context and cancel function for a given task message.
func New(base context.Context, msg *base.TaskMessage, deadline time.Time) (context.Context, context.CancelFunc) {
	metadata := taskMetadata{
		id:         msg.ID,
		maxRetry:   msg.Retry,
		retryCount: msg.Retried,
		qname:      msg.Queue,
	}
	ctx := context.WithValue(base, metadataCtxKey, metadata)
	return context.WithDeadline(ctx, deadline)
}

// GetTaskID extracts a task ID from a context, if any.
//
// ID of a task is guaranteed to be unique.
// ID of a task doesn't change if the task is being retried.
func GetTaskID(ctx context.Context) (id string, ok bool) {
	metadata, ok := ctx.Value(metadataCtxKey).(taskMetadata)
	if !ok {
		return "", false
	}
	return metadata.id, true
}

// GetRetryCount extracts retry count from a context, if any.
//
// Return value n indicates the number of times associated task has been
// retried so far.
func GetRetryCount(ctx context.Context) (n int, ok bool) {
	metadata, ok := ctx.Value(metadataCtxKey).(taskMetadata)
	if !ok {
		return 0, false
	}
	return metadata.retryCount, true
}

// GetMaxRetry extracts maximum retry from a context, if any.
//
// Return value n indicates the maximum number of times the associated task
// can be retried if ProcessTask returns a non-nil error.
func GetMaxRetry(ctx context.Context) (n int, ok bool) {
	metadata, ok := ctx.Value(metadataCtxKey).(taskMetadata)
	if !ok {
		return 0, false
	}
	return metadata.maxRetry, true
}

// GetQueueName extracts queue name from a context, if any.
//
// Return value qname indicates which queue the task was pulled from.
func GetQueueName(ctx context.Context) (qname string, ok bool) {
	metadata, ok := ctx.Value(metadataCtxKey).(taskMetadata)
	if !ok {
		return "", false
	}
	return metadata.qname, true
}
