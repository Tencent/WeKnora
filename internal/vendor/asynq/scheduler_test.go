// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/redis/go-redis/v9"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/testutil"
)

func TestSchedulerRegister(t *testing.T) {
	tests := []struct {
		cronspec string
		task     *Task
		opts     []Option
		wait     time.Duration
		queue    string
		want     []*base.TaskMessage
	}{
		{
			cronspec: "@every 3s",
			task:     NewTask("task1", nil),
			opts:     []Option{MaxRetry(10)},
			wait:     10 * time.Second,
			queue:    "default",
			want: []*base.TaskMessage{
				{
					Type:    "task1",
					Payload: nil,
					Retry:   10,
					Timeout: int64(defaultTimeout.Seconds()),
					Queue:   "default",
				},
				{
					Type:    "task1",
					Payload: nil,
					Retry:   10,
					Timeout: int64(defaultTimeout.Seconds()),
					Queue:   "default",
				},
				{
					Type:    "task1",
					Payload: nil,
					Retry:   10,
					Timeout: int64(defaultTimeout.Seconds()),
					Queue:   "default",
				},
			},
		},
	}

	r := setup(t)

	// Tests for new redis connection.
	for _, tc := range tests {
		scheduler := NewScheduler(getRedisConnOpt(t), nil)
		if _, err := scheduler.Register(tc.cronspec, tc.task, tc.opts...); err != nil {
			t.Fatal(err)
		}

		if err := scheduler.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(tc.wait)
		scheduler.Shutdown()

		got := testutil.GetPendingMessages(t, r, tc.queue)
		if diff := cmp.Diff(tc.want, got, testutil.IgnoreIDOpt); diff != "" {
			t.Errorf("mismatch found in queue %q: (-want,+got)\n%s", tc.queue, diff)
		}
	}

	r = setup(t)

	// Tests for existing redis connection.
	for _, tc := range tests {
		redisClient := getRedisConnOpt(t).MakeRedisClient().(redis.UniversalClient)
		scheduler := NewSchedulerFromRedisClient(redisClient, nil)
		if _, err := scheduler.Register(tc.cronspec, tc.task, tc.opts...); err != nil {
			t.Fatal(err)
		}

		if err := scheduler.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(tc.wait)
		scheduler.Shutdown()

		got := testutil.GetPendingMessages(t, r, tc.queue)
		if diff := cmp.Diff(tc.want, got, testutil.IgnoreIDOpt); diff != "" {
			t.Errorf("mismatch found in queue %q: (-want,+got)\n%s", tc.queue, diff)
		}
	}
}

func TestSchedulerWhenRedisDown(t *testing.T) {
	var (
		mu      sync.Mutex
		counter int
	)
	errorHandler := func(task *Task, opts []Option, err error) {
		mu.Lock()
		counter++
		mu.Unlock()
	}

	// Connect to non-existent redis instance to simulate a redis server being down.
	scheduler := NewScheduler(
		RedisClientOpt{Addr: ":9876"}, // no Redis listening to this port.
		&SchedulerOpts{EnqueueErrorHandler: errorHandler},
	)

	task := NewTask("test", nil)

	if _, err := scheduler.Register("@every 3s", task); err != nil {
		t.Fatal(err)
	}

	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	// Scheduler should attempt to enqueue the task three times (every 3s).
	time.Sleep(10 * time.Second)
	scheduler.Shutdown()

	mu.Lock()
	if counter != 3 {
		t.Errorf("EnqueueErrorHandler was called %d times, want 3", counter)
	}
	mu.Unlock()
}

func TestSchedulerUnregister(t *testing.T) {
	tests := []struct {
		cronspec string
		task     *Task
		opts     []Option
		wait     time.Duration
		queue    string
	}{
		{
			cronspec: "@every 3s",
			task:     NewTask("task1", nil),
			opts:     []Option{MaxRetry(10)},
			wait:     10 * time.Second,
			queue:    "default",
		},
	}

	r := setup(t)

	for _, tc := range tests {
		scheduler := NewScheduler(getRedisConnOpt(t), nil)
		entryID, err := scheduler.Register(tc.cronspec, tc.task, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err := scheduler.Unregister(entryID); err != nil {
			t.Fatal(err)
		}

		if err := scheduler.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(tc.wait)
		scheduler.Shutdown()

		got := testutil.GetPendingMessages(t, r, tc.queue)
		if len(got) != 0 {
			t.Errorf("%d tasks were enqueued, want zero", len(got))
		}
	}
}

func TestSchedulerPostAndPreEnqueueHandler(t *testing.T) {
	var (
		preMu       sync.Mutex
		preCounter  int
		postMu      sync.Mutex
		postCounter int
	)
	preHandler := func(task *Task, opts []Option) {
		preMu.Lock()
		preCounter++
		preMu.Unlock()
	}
	postHandler := func(info *TaskInfo, err error) {
		postMu.Lock()
		postCounter++
		postMu.Unlock()
	}

	// Connect to non-existent redis instance to simulate a redis server being down.
	scheduler := NewScheduler(
		getRedisConnOpt(t),
		&SchedulerOpts{
			PreEnqueueFunc:  preHandler,
			PostEnqueueFunc: postHandler,
		},
	)

	task := NewTask("test", nil)

	if _, err := scheduler.Register("@every 3s", task); err != nil {
		t.Fatal(err)
	}

	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	// Scheduler should attempt to enqueue the task three times (every 3s).
	time.Sleep(10 * time.Second)
	scheduler.Shutdown()

	preMu.Lock()
	if preCounter != 3 {
		t.Errorf("PreEnqueueFunc was called %d times, want 3", preCounter)
	}
	preMu.Unlock()

	postMu.Lock()
	if postCounter != 3 {
		t.Errorf("PostEnqueueFunc was called %d times, want 3", postCounter)
	}
	postMu.Unlock()
}
