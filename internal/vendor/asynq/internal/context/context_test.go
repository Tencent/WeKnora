// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package context

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/base"
)

func TestCreateContextWithFutureDeadline(t *testing.T) {
	tests := []struct {
		deadline time.Time
	}{
		{time.Now().Add(time.Hour)},
	}

	for _, tc := range tests {
		msg := &base.TaskMessage{
			Type:    "something",
			ID:      uuid.NewString(),
			Payload: nil,
		}

		ctx, cancel := New(context.Background(), msg, tc.deadline)
		select {
		case x := <-ctx.Done():
			t.Errorf("<-ctx.Done() == %v, want nothing (it should block)", x)
		default:
		}

		got, ok := ctx.Deadline()
		if !ok {
			t.Errorf("ctx.Deadline() returned false, want deadline to be set")
		}
		if !cmp.Equal(tc.deadline, got) {
			t.Errorf("ctx.Deadline() returned %v, want %v", got, tc.deadline)
		}

		cancel()

		select {
		case <-ctx.Done():
		default:
			t.Errorf("ctx.Done() blocked, want it to be non-blocking")
		}
	}
}

func TestCreateContextWithBaseContext(t *testing.T) {
	type ctxKey string
	type ctxValue string
	var key ctxKey = "key"
	var value ctxValue = "value"

	tests := []struct {
		baseCtx  context.Context
		validate func(ctx context.Context, t *testing.T) error
	}{
		{
			baseCtx: context.WithValue(context.Background(), key, value),
			validate: func(ctx context.Context, t *testing.T) error {
				got, ok := ctx.Value(key).(ctxValue)
				if !ok {
					return fmt.Errorf("ctx.Value().(ctxValue) returned false, expected to be true")
				}
				if want := value; got != want {
					return fmt.Errorf("ctx.Value().(ctxValue) returned unknown value (%v), expected to be %s", got, value)
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		msg := &base.TaskMessage{
			Type:    "something",
			ID:      uuid.NewString(),
			Payload: nil,
		}

		ctx, cancel := New(tc.baseCtx, msg, time.Now().Add(30*time.Minute))
		defer cancel()

		select {
		case x := <-ctx.Done():
			t.Errorf("<-ctx.Done() == %v, want nothing (it should block)", x)
		default:
		}

		if err := tc.validate(ctx, t); err != nil {
			t.Errorf("%v", err)
		}
	}
}

func TestCreateContextWithPastDeadline(t *testing.T) {
	tests := []struct {
		deadline time.Time
	}{
		{time.Now().Add(-2 * time.Hour)},
	}

	for _, tc := range tests {
		msg := &base.TaskMessage{
			Type:    "something",
			ID:      uuid.NewString(),
			Payload: nil,
		}

		ctx, cancel := New(context.Background(), msg, tc.deadline)
		defer cancel()

		select {
		case <-ctx.Done():
		default:
			t.Errorf("ctx.Done() blocked, want it to be non-blocking")
		}

		got, ok := ctx.Deadline()
		if !ok {
			t.Errorf("ctx.Deadline() returned false, want deadline to be set")
		}
		if !cmp.Equal(tc.deadline, got) {
			t.Errorf("ctx.Deadline() returned %v, want %v", got, tc.deadline)
		}
	}
}

func TestGetTaskMetadataFromContext(t *testing.T) {
	tests := []struct {
		desc string
		msg  *base.TaskMessage
	}{
		{"with zero retried message", &base.TaskMessage{Type: "something", ID: uuid.NewString(), Retry: 25, Retried: 0, Timeout: 1800, Queue: "default"}},
		{"with non-zero retried message", &base.TaskMessage{Type: "something", ID: uuid.NewString(), Retry: 10, Retried: 5, Timeout: 1800, Queue: "default"}},
		{"with custom queue name", &base.TaskMessage{Type: "something", ID: uuid.NewString(), Retry: 25, Retried: 0, Timeout: 1800, Queue: "custom"}},
	}

	for _, tc := range tests {
		ctx, cancel := New(context.Background(), tc.msg, time.Now().Add(30*time.Minute))
		defer cancel()

		id, ok := GetTaskID(ctx)
		if !ok {
			t.Errorf("%s: GetTaskID(ctx) returned ok == false", tc.desc)
		}
		if ok && id != tc.msg.ID {
			t.Errorf("%s: GetTaskID(ctx) returned id == %q, want %q", tc.desc, id, tc.msg.ID)
		}

		retried, ok := GetRetryCount(ctx)
		if !ok {
			t.Errorf("%s: GetRetryCount(ctx) returned ok == false", tc.desc)
		}
		if ok && retried != tc.msg.Retried {
			t.Errorf("%s: GetRetryCount(ctx) returned n == %d want %d", tc.desc, retried, tc.msg.Retried)
		}

		maxRetry, ok := GetMaxRetry(ctx)
		if !ok {
			t.Errorf("%s: GetMaxRetry(ctx) returned ok == false", tc.desc)
		}
		if ok && maxRetry != tc.msg.Retry {
			t.Errorf("%s: GetMaxRetry(ctx) returned n == %d want %d", tc.desc, maxRetry, tc.msg.Retry)
		}

		qname, ok := GetQueueName(ctx)
		if !ok {
			t.Errorf("%s: GetQueueName(ctx) returned ok == false", tc.desc)
		}
		if ok && qname != tc.msg.Queue {
			t.Errorf("%s: GetQueueName(ctx) returned qname == %q, want %q", tc.desc, qname, tc.msg.Queue)
		}
	}
}

func TestGetTaskMetadataFromContextError(t *testing.T) {
	tests := []struct {
		desc string
		ctx  context.Context
	}{
		{"with background context", context.Background()},
	}

	for _, tc := range tests {
		if _, ok := GetTaskID(tc.ctx); ok {
			t.Errorf("%s: GetTaskID(ctx) returned ok == true", tc.desc)
		}
		if _, ok := GetRetryCount(tc.ctx); ok {
			t.Errorf("%s: GetRetryCount(ctx) returned ok == true", tc.desc)
		}
		if _, ok := GetMaxRetry(tc.ctx); ok {
			t.Errorf("%s: GetMaxRetry(ctx) returned ok == true", tc.desc)
		}
		if _, ok := GetQueueName(tc.ctx); ok {
			t.Errorf("%s: GetQueueName(ctx) returned ok == true", tc.desc)
		}
	}
}
