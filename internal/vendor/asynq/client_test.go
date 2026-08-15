// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hibiken/asynq/internal/base"
	h "github.com/hibiken/asynq/internal/testutil"
	"github.com/redis/go-redis/v9"
)

func TestClientEnqueueWithProcessAtOption(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	task := NewTask("send_email", h.JSON(map[string]interface{}{"to": "customer@gmail.com", "from": "merchant@example.com"}))

	var (
		now          = time.Now()
		oneHourLater = now.Add(time.Hour)
	)

	tests := []struct {
		desc          string
		task          *Task
		processAt     time.Time // value for ProcessAt option
		opts          []Option  // other options
		wantInfo      *TaskInfo
		wantPending   map[string][]*base.TaskMessage
		wantScheduled map[string][]base.Z
	}{
		{
			desc:      "Process task immediately",
			task:      task,
			processAt: now,
			opts:      []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
		},
		{
			desc:      "Schedule task to be processed in the future",
			task:      task,
			processAt: oneHourLater,
			opts:      []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStateScheduled,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: oneHourLater,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]base.Z{
				"default": {
					{
						Message: &base.TaskMessage{
							Type:     task.Type(),
							Payload:  task.Payload(),
							Retry:    defaultMaxRetry,
							Queue:    "default",
							Timeout:  int64(defaultTimeout.Seconds()),
							Deadline: noDeadline.Unix(),
						},
						Score: oneHourLater.Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		opts := append(tc.opts, ProcessAt(tc.processAt))
		gotInfo, err := client.Enqueue(tc.task, opts...)
		if err != nil {
			t.Error(err)
			continue
		}
		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task, ProcessAt(%v)) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.processAt, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.ScheduledKey(qname), diff)
			}
		}
	}
}

func testClientEnqueue(t *testing.T, client *Client, r redis.UniversalClient) {
	task := NewTask("send_email", h.JSON(map[string]interface{}{"to": "customer@gmail.com", "from": "merchant@example.com"}))
	now := time.Now()

	tests := []struct {
		desc        string
		task        *Task
		opts        []Option
		wantInfo    *TaskInfo
		wantPending map[string][]*base.TaskMessage
	}{
		{
			desc: "Process task immediately with a custom retry count",
			task: task,
			opts: []Option{
				MaxRetry(3),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      3,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    3,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "Negative retry count",
			task: task,
			opts: []Option{
				MaxRetry(-2),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      0, // Retry count should be set to zero
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    0, // Retry count should be set to zero
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "Conflicting options",
			task: task,
			opts: []Option{
				MaxRetry(2),
				MaxRetry(10),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      10, // Last option takes precedence
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    10, // Last option takes precedence
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "With queue option",
			task: task,
			opts: []Option{
				Queue("custom"),
			},
			wantInfo: &TaskInfo{
				Queue:         "custom",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"custom": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "custom",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "Queue option should be case sensitive",
			task: task,
			opts: []Option{
				Queue("MyQueue"),
			},
			wantInfo: &TaskInfo{
				Queue:         "MyQueue",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"MyQueue": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "MyQueue",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "With timeout option",
			task: task,
			opts: []Option{
				Timeout(20 * time.Second),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       20 * time.Second,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  20,
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "With deadline option",
			task: task,
			opts: []Option{
				Deadline(time.Date(2020, time.June, 24, 0, 0, 0, 0, time.UTC)),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       noTimeout,
				Deadline:      time.Date(2020, time.June, 24, 0, 0, 0, 0, time.UTC),
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(noTimeout.Seconds()),
						Deadline: time.Date(2020, time.June, 24, 0, 0, 0, 0, time.UTC).Unix(),
					},
				},
			},
		},
		{
			desc: "With both deadline and timeout options",
			task: task,
			opts: []Option{
				Timeout(20 * time.Second),
				Deadline(time.Date(2020, time.June, 24, 0, 0, 0, 0, time.UTC)),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       20 * time.Second,
				Deadline:      time.Date(2020, time.June, 24, 0, 0, 0, 0, time.UTC),
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  20,
						Deadline: time.Date(2020, time.June, 24, 0, 0, 0, 0, time.UTC).Unix(),
					},
				},
			},
		},
		{
			desc: "With Retention option",
			task: task,
			opts: []Option{
				Retention(24 * time.Hour),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
				Retention:     24 * time.Hour,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:      task.Type(),
						Payload:   task.Payload(),
						Retry:     defaultMaxRetry,
						Queue:     "default",
						Timeout:   int64(defaultTimeout.Seconds()),
						Deadline:  noDeadline.Unix(),
						Retention: int64((24 * time.Hour).Seconds()),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		gotInfo, err := client.Enqueue(tc.task, tc.opts...)
		if err != nil {
			t.Error(err)
			continue
		}
		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantPending {
			got := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, got, h.IgnoreIDOpt); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
	}
}

func TestClientEnqueue(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()
	testClientEnqueue(t, client, r)
}

func TestClientFromRedisClientEnqueue(t *testing.T) {
	r := setup(t)
	redisClient := getRedisConnOpt(t).MakeRedisClient().(redis.UniversalClient)
	client := NewClientFromRedisClient(redisClient)
	testClientEnqueue(t, client, r)
	err := client.Close()
	if err == nil {
		t.Error("client.Close() should have failed because of a shared client but it didn't")
	}
}

func TestClientEnqueueWithGroupOption(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	task := NewTask("mytask", []byte("foo"))
	now := time.Now()

	tests := []struct {
		desc          string
		task          *Task
		opts          []Option
		wantInfo      *TaskInfo
		wantPending   map[string][]*base.TaskMessage
		wantGroups    map[string]map[string][]base.Z // map queue name to a set of groups
		wantScheduled map[string][]base.Z
	}{
		{
			desc: "With only Group option",
			task: task,
			opts: []Option{
				Group("mygroup"),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Group:         "mygroup",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStateAggregating,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: time.Time{},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {}, // should not be pending
			},
			wantGroups: map[string]map[string][]base.Z{
				"default": {
					"mygroup": {
						{
							Message: &base.TaskMessage{
								Type:     task.Type(),
								Payload:  task.Payload(),
								Retry:    defaultMaxRetry,
								Queue:    "default",
								Timeout:  int64(defaultTimeout.Seconds()),
								Deadline: noDeadline.Unix(),
								GroupKey: "mygroup",
							},
							Score: now.Unix(),
						},
					},
				},
			},
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
		},
		{
			desc: "With Group and ProcessAt options",
			task: task,
			opts: []Option{
				Group("mygroup"),
				ProcessAt(now.Add(30 * time.Minute)),
			},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Group:         "mygroup",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStateScheduled,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now.Add(30 * time.Minute),
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {}, // should not be pending
			},
			wantGroups: map[string]map[string][]base.Z{
				"default": {
					"mygroup": {}, // should not be added to the group yet
				},
			},
			wantScheduled: map[string][]base.Z{
				"default": {
					{
						Message: &base.TaskMessage{
							Type:     task.Type(),
							Payload:  task.Payload(),
							Retry:    defaultMaxRetry,
							Queue:    "default",
							Timeout:  int64(defaultTimeout.Seconds()),
							Deadline: noDeadline.Unix(),
							GroupKey: "mygroup",
						},
						Score: now.Add(30 * time.Minute).Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		gotInfo, err := client.Enqueue(tc.task, tc.opts...)
		if err != nil {
			t.Error(err)
			continue
		}
		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantPending {
			got := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, got, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}

		for qname, groups := range tc.wantGroups {
			for groupKey, want := range groups {
				got := h.GetGroupEntries(t, r, qname, groupKey)
				if diff := cmp.Diff(want, got, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.GroupKey(qname, groupKey), diff)
				}
			}
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestClientEnqueueWithTaskIDOption(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	task := NewTask("send_email", nil)
	now := time.Now()

	tests := []struct {
		desc        string
		task        *Task
		opts        []Option
		wantInfo    *TaskInfo
		wantPending map[string][]*base.TaskMessage
	}{
		{
			desc: "With a valid TaskID option",
			task: task,
			opts: []Option{
				TaskID("custom_id"),
			},
			wantInfo: &TaskInfo{
				ID:            "custom_id",
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						ID:       "custom_id",
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		gotInfo, err := client.Enqueue(tc.task, tc.opts...)
		if err != nil {
			t.Errorf("got non-nil error %v, want nil", err)
			continue
		}

		cmpOptions := []cmp.Option{
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantPending {
			got := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
	}
}

func TestClientEnqueueWithConflictingTaskID(t *testing.T) {
	setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	const taskID = "custom_id"
	task := NewTask("foo", nil)

	if _, err := client.Enqueue(task, TaskID(taskID)); err != nil {
		t.Fatalf("First task: Enqueue failed: %v", err)
	}
	_, err := client.Enqueue(task, TaskID(taskID))
	if !errors.Is(err, ErrTaskIDConflict) {
		t.Errorf("Second task: Enqueue returned %v, want %v", err, ErrTaskIDConflict)
	}
}

func TestClientEnqueueWithProcessInOption(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	task := NewTask("send_email", h.JSON(map[string]interface{}{"to": "customer@gmail.com", "from": "merchant@example.com"}))
	now := time.Now()

	tests := []struct {
		desc          string
		task          *Task
		delay         time.Duration // value for ProcessIn option
		opts          []Option      // other options
		wantInfo      *TaskInfo
		wantPending   map[string][]*base.TaskMessage
		wantScheduled map[string][]base.Z
	}{
		{
			desc:  "schedule a task to be processed in one hour",
			task:  task,
			delay: 1 * time.Hour,
			opts:  []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStateScheduled,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: time.Now().Add(1 * time.Hour),
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]base.Z{
				"default": {
					{
						Message: &base.TaskMessage{
							Type:     task.Type(),
							Payload:  task.Payload(),
							Retry:    defaultMaxRetry,
							Queue:    "default",
							Timeout:  int64(defaultTimeout.Seconds()),
							Deadline: noDeadline.Unix(),
						},
						Score: time.Now().Add(time.Hour).Unix(),
					},
				},
			},
		},
		{
			desc:  "Zero delay",
			task:  task,
			delay: 0,
			opts:  []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          task.Type(),
				Payload:       task.Payload(),
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     task.Type(),
						Payload:  task.Payload(),
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		opts := append(tc.opts, ProcessIn(tc.delay))
		gotInfo, err := client.Enqueue(tc.task, opts...)
		if err != nil {
			t.Error(err)
			continue
		}
		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task, ProcessIn(%v)) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.delay, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestClientEnqueueError(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	task := NewTask("send_email", h.JSON(map[string]interface{}{"to": "customer@gmail.com", "from": "merchant@example.com"}))

	tests := []struct {
		desc string
		task *Task
		opts []Option
	}{
		{
			desc: "With nil task",
			task: nil,
			opts: []Option{},
		},
		{
			desc: "With empty queue name",
			task: task,
			opts: []Option{
				Queue(""),
			},
		},
		{
			desc: "With empty task typename",
			task: NewTask("", h.JSON(map[string]interface{}{})),
			opts: []Option{},
		},
		{
			desc: "With blank task typename",
			task: NewTask("    ", h.JSON(map[string]interface{}{})),
			opts: []Option{},
		},
		{
			desc: "With empty task ID",
			task: NewTask("foo", nil),
			opts: []Option{TaskID("")},
		},
		{
			desc: "With blank task ID",
			task: NewTask("foo", nil),
			opts: []Option{TaskID("  ")},
		},
		{
			desc: "With unique option less than 1s",
			task: NewTask("foo", nil),
			opts: []Option{Unique(300 * time.Millisecond)},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		_, err := client.Enqueue(tc.task, tc.opts...)
		if err == nil {
			t.Errorf("%s; client.Enqueue(task, opts...) did not return non-nil error", tc.desc)
		}
	}
}

func TestClientWithDefaultOptions(t *testing.T) {
	r := setup(t)

	now := time.Now()

	tests := []struct {
		desc        string
		defaultOpts []Option // options set at task initialization time
		opts        []Option // options used at enqueue time.
		tasktype    string
		payload     []byte
		wantInfo    *TaskInfo
		queue       string // queue that the message should go into.
		want        *base.TaskMessage
	}{
		{
			desc:        "With queue routing option",
			defaultOpts: []Option{Queue("feed")},
			opts:        []Option{},
			tasktype:    "feed:import",
			payload:     nil,
			wantInfo: &TaskInfo{
				Queue:         "feed",
				Type:          "feed:import",
				Payload:       nil,
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			queue: "feed",
			want: &base.TaskMessage{
				Type:     "feed:import",
				Payload:  nil,
				Retry:    defaultMaxRetry,
				Queue:    "feed",
				Timeout:  int64(defaultTimeout.Seconds()),
				Deadline: noDeadline.Unix(),
			},
		},
		{
			desc:        "With multiple options",
			defaultOpts: []Option{Queue("feed"), MaxRetry(5)},
			opts:        []Option{},
			tasktype:    "feed:import",
			payload:     nil,
			wantInfo: &TaskInfo{
				Queue:         "feed",
				Type:          "feed:import",
				Payload:       nil,
				State:         TaskStatePending,
				MaxRetry:      5,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			queue: "feed",
			want: &base.TaskMessage{
				Type:     "feed:import",
				Payload:  nil,
				Retry:    5,
				Queue:    "feed",
				Timeout:  int64(defaultTimeout.Seconds()),
				Deadline: noDeadline.Unix(),
			},
		},
		{
			desc:        "With overriding options at enqueue time",
			defaultOpts: []Option{Queue("feed"), MaxRetry(5)},
			opts:        []Option{Queue("critical")},
			tasktype:    "feed:import",
			payload:     nil,
			wantInfo: &TaskInfo{
				Queue:         "critical",
				Type:          "feed:import",
				Payload:       nil,
				State:         TaskStatePending,
				MaxRetry:      5,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			queue: "critical",
			want: &base.TaskMessage{
				Type:     "feed:import",
				Payload:  nil,
				Retry:    5,
				Queue:    "critical",
				Timeout:  int64(defaultTimeout.Seconds()),
				Deadline: noDeadline.Unix(),
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		c := NewClient(getRedisConnOpt(t))
		defer c.Close()
		task := NewTask(tc.tasktype, tc.payload, tc.defaultOpts...)
		gotInfo, err := c.Enqueue(task, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task, opts...) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, gotInfo, tc.wantInfo, diff)
		}
		pending := h.GetPendingMessages(t, r, tc.queue)
		if len(pending) != 1 {
			t.Errorf("%s;\nexpected queue %q to have one message; got %d messages in the queue.",
				tc.desc, tc.queue, len(pending))
			continue
		}
		got := pending[0]
		if diff := cmp.Diff(tc.want, got, h.IgnoreIDOpt); diff != "" {
			t.Errorf("%s;\nmismatch found in pending task message; (-want,+got)\n%s",
				tc.desc, diff)
		}
	}
}

func TestClientEnqueueUnique(t *testing.T) {
	r := setup(t)
	c := NewClient(getRedisConnOpt(t))
	defer c.Close()

	tests := []struct {
		task *Task
		ttl  time.Duration
	}{
		{
			NewTask("email", h.JSON(map[string]interface{}{"user_id": 123})),
			time.Hour,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		// Enqueue the task first. It should succeed.
		_, err := c.Enqueue(tc.task, Unique(tc.ttl))
		if err != nil {
			t.Fatal(err)
		}

		gotTTL := r.TTL(context.Background(), base.UniqueKey(base.DefaultQueueName, tc.task.Type(), tc.task.Payload())).Val()
		if !cmp.Equal(tc.ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
			t.Errorf("TTL = %v, want %v", gotTTL, tc.ttl)
			continue
		}

		// Enqueue the task again. It should fail.
		_, err = c.Enqueue(tc.task, Unique(tc.ttl))
		if err == nil {
			t.Errorf("Enqueueing %+v did not return an error", tc.task)
			continue
		}
		if !errors.Is(err, ErrDuplicateTask) {
			t.Errorf("Enqueueing %+v returned an error that is not ErrDuplicateTask", tc.task)
			continue
		}
	}
}

func TestClientEnqueueUniqueWithProcessInOption(t *testing.T) {
	r := setup(t)
	c := NewClient(getRedisConnOpt(t))
	defer c.Close()

	tests := []struct {
		task *Task
		d    time.Duration
		ttl  time.Duration
	}{
		{
			NewTask("reindex", nil),
			time.Hour,
			10 * time.Minute,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		// Enqueue the task first. It should succeed.
		_, err := c.Enqueue(tc.task, ProcessIn(tc.d), Unique(tc.ttl))
		if err != nil {
			t.Fatal(err)
		}

		gotTTL := r.TTL(context.Background(), base.UniqueKey(base.DefaultQueueName, tc.task.Type(), tc.task.Payload())).Val()
		wantTTL := time.Duration(tc.ttl.Seconds()+tc.d.Seconds()) * time.Second
		if !cmp.Equal(wantTTL.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
			t.Errorf("TTL = %v, want %v", gotTTL, wantTTL)
			continue
		}

		// Enqueue the task again. It should fail.
		_, err = c.Enqueue(tc.task, ProcessIn(tc.d), Unique(tc.ttl))
		if err == nil {
			t.Errorf("Enqueueing %+v did not return an error", tc.task)
			continue
		}
		if !errors.Is(err, ErrDuplicateTask) {
			t.Errorf("Enqueueing %+v returned an error that is not ErrDuplicateTask", tc.task)
			continue
		}
	}
}

func TestClientEnqueueUniqueWithProcessAtOption(t *testing.T) {
	r := setup(t)
	c := NewClient(getRedisConnOpt(t))
	defer c.Close()

	tests := []struct {
		task *Task
		at   time.Time
		ttl  time.Duration
	}{
		{
			NewTask("reindex", nil),
			time.Now().Add(time.Hour),
			10 * time.Minute,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.

		// Enqueue the task first. It should succeed.
		_, err := c.Enqueue(tc.task, ProcessAt(tc.at), Unique(tc.ttl))
		if err != nil {
			t.Fatal(err)
		}

		gotTTL := r.TTL(context.Background(), base.UniqueKey(base.DefaultQueueName, tc.task.Type(), tc.task.Payload())).Val()
		wantTTL := time.Until(tc.at.Add(tc.ttl))
		if !cmp.Equal(wantTTL.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
			t.Errorf("TTL = %v, want %v", gotTTL, wantTTL)
			continue
		}

		// Enqueue the task again. It should fail.
		_, err = c.Enqueue(tc.task, ProcessAt(tc.at), Unique(tc.ttl))
		if err == nil {
			t.Errorf("Enqueueing %+v did not return an error", tc.task)
			continue
		}
		if !errors.Is(err, ErrDuplicateTask) {
			t.Errorf("Enqueueing %+v returned an error that is not ErrDuplicateTask", tc.task)
			continue
		}
	}
}

func TestClientEnqueueWithHeaders(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	now := time.Now()
	headers := map[string]string{
		"user-id":    "123",
		"request-id": "abc-def-ghi",
		"priority":   "high",
	}

	tests := []struct {
		desc        string
		task        *Task
		opts        []Option
		wantInfo    *TaskInfo
		wantPending map[string][]*base.TaskMessage
	}{
		{
			desc: "Task with headers",
			task: NewTaskWithHeaders("send_email", h.JSON(map[string]interface{}{"to": "user@example.com"}), headers),
			opts: []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          "send_email",
				Payload:       h.JSON(map[string]interface{}{"to": "user@example.com"}),
				Headers:       headers,
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     "send_email",
						Payload:  h.JSON(map[string]interface{}{"to": "user@example.com"}),
						Headers:  headers,
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "Task with empty headers",
			task: NewTaskWithHeaders("process_data", []byte("data"), map[string]string{}),
			opts: []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          "process_data",
				Payload:       []byte("data"),
				Headers:       map[string]string{},
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     "process_data",
						Payload:  []byte("data"),
						Headers:  nil,
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "Task with nil headers",
			task: NewTaskWithHeaders("cleanup", nil, nil),
			opts: []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          "cleanup",
				Payload:       nil,
				Headers:       nil,
				State:         TaskStatePending,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					{
						Type:     "cleanup",
						Payload:  nil,
						Headers:  nil,
						Retry:    defaultMaxRetry,
						Queue:    "default",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
		{
			desc: "Task with headers and custom options",
			task: NewTaskWithHeaders("notify", []byte("notification"), map[string]string{"channel": "email"}),
			opts: []Option{MaxRetry(5), Queue("notifications")},
			wantInfo: &TaskInfo{
				Queue:         "notifications",
				Type:          "notify",
				Payload:       []byte("notification"),
				Headers:       map[string]string{"channel": "email"},
				State:         TaskStatePending,
				MaxRetry:      5,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: now,
			},
			wantPending: map[string][]*base.TaskMessage{
				"notifications": {
					{
						Type:     "notify",
						Payload:  []byte("notification"),
						Headers:  map[string]string{"channel": "email"},
						Retry:    5,
						Queue:    "notifications",
						Timeout:  int64(defaultTimeout.Seconds()),
						Deadline: noDeadline.Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		gotInfo, err := client.Enqueue(tc.task, tc.opts...)
		if err != nil {
			t.Errorf("%s: Enqueue failed: %v", tc.desc, err)
			continue
		}

		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantPending {
			got := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, got, h.IgnoreIDOpt); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
	}
}

func TestClientEnqueueWithHeadersScheduled(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	now := time.Now()
	oneHourLater := now.Add(time.Hour)
	headers := map[string]string{
		"correlation-id": "xyz-123",
		"source":         "api",
	}

	tests := []struct {
		desc          string
		task          *Task
		processAt     time.Time
		opts          []Option
		wantInfo      *TaskInfo
		wantScheduled map[string][]base.Z
	}{
		{
			desc:      "Schedule task with headers",
			task:      NewTaskWithHeaders("scheduled_task", []byte("payload"), headers),
			processAt: oneHourLater,
			opts:      []Option{},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Type:          "scheduled_task",
				Payload:       []byte("payload"),
				Headers:       headers,
				State:         TaskStateScheduled,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: oneHourLater,
			},
			wantScheduled: map[string][]base.Z{
				"default": {
					{
						Message: &base.TaskMessage{
							Type:     "scheduled_task",
							Payload:  []byte("payload"),
							Headers:  headers,
							Retry:    defaultMaxRetry,
							Queue:    "default",
							Timeout:  int64(defaultTimeout.Seconds()),
							Deadline: noDeadline.Unix(),
						},
						Score: oneHourLater.Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		opts := append(tc.opts, ProcessAt(tc.processAt))
		gotInfo, err := client.Enqueue(tc.task, opts...)
		if err != nil {
			t.Errorf("%s: Enqueue failed: %v", tc.desc, err)
			continue
		}

		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task, ProcessAt(%v)) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.processAt, gotInfo, tc.wantInfo, diff)
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestNewTaskWithHeaders(t *testing.T) {
	tests := []struct {
		desc     string
		typename string
		payload  []byte
		headers  map[string]string
		opts     []Option
		want     *Task
	}{
		{
			desc:     "Task with headers",
			typename: "test_task",
			payload:  []byte("test payload"),
			headers:  map[string]string{"key1": "value1", "key2": "value2"},
			opts:     []Option{MaxRetry(3)},
			want: &Task{
				typename: "test_task",
				payload:  []byte("test payload"),
				headers:  map[string]string{"key1": "value1", "key2": "value2"},
				opts:     []Option{MaxRetry(3)},
			},
		},
		{
			desc:     "Task with empty headers",
			typename: "empty_headers",
			payload:  nil,
			headers:  map[string]string{},
			opts:     nil,
			want: &Task{
				typename: "empty_headers",
				payload:  nil,
				headers:  map[string]string{},
				opts:     nil,
			},
		},
		{
			desc:     "Task with nil headers",
			typename: "nil_headers",
			payload:  []byte("data"),
			headers:  nil,
			opts:     []Option{Queue("test")},
			want: &Task{
				typename: "nil_headers",
				payload:  []byte("data"),
				headers:  nil,
				opts:     []Option{Queue("test")},
			},
		},
	}

	for _, tc := range tests {
		got := NewTaskWithHeaders(tc.typename, tc.payload, tc.headers, tc.opts...)

		if got.Type() != tc.want.typename {
			t.Errorf("%s: Type() = %q, want %q", tc.desc, got.Type(), tc.want.typename)
		}

		if diff := cmp.Diff(tc.want.payload, got.Payload()); diff != "" {
			t.Errorf("%s: Payload() mismatch (-want,+got)\n%s", tc.desc, diff)
		}

		if diff := cmp.Diff(tc.want.headers, got.Headers()); diff != "" {
			t.Errorf("%s: Headers() mismatch (-want,+got)\n%s", tc.desc, diff)
		}

		if tc.headers != nil && got.Headers() != nil {
			tc.headers["modified"] = "test"
			if _, exists := got.Headers()["modified"]; exists {
				t.Errorf("%s: Headers should be cloned, but modification affected task headers", tc.desc)
			}
		}
	}
}

func TestTaskHeadersMethod(t *testing.T) {
	tests := []struct {
		desc    string
		task    *Task
		want    map[string]string
		wantNil bool
	}{
		{
			desc:    "Task created with NewTask has nil headers",
			task:    NewTask("test", []byte("data")),
			want:    nil,
			wantNil: true,
		},
		{
			desc: "Task created with NewTaskWithHeaders has headers",
			task: NewTaskWithHeaders("test", []byte("data"), map[string]string{"key": "value"}),
			want: map[string]string{"key": "value"},
		},
		{
			desc: "Task created with empty headers",
			task: NewTaskWithHeaders("test", []byte("data"), map[string]string{}),
			want: map[string]string{},
		},
		{
			desc:    "Task created with nil headers",
			task:    NewTaskWithHeaders("test", []byte("data"), nil),
			want:    nil,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		got := tc.task.Headers()

		if tc.wantNil {
			if got != nil {
				t.Errorf("%s: Headers() = %v, want nil", tc.desc, got)
			}
		} else {
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s: Headers() mismatch (-want,+got)\n%s", tc.desc, diff)
			}
		}
	}
}

func TestClientEnqueueWithHeadersAndGroup(t *testing.T) {
	r := setup(t)
	client := NewClient(getRedisConnOpt(t))
	defer client.Close()

	now := time.Now()
	headers := map[string]string{
		"batch-id": "batch-123",
		"priority": "high",
	}

	tests := []struct {
		desc       string
		task       *Task
		opts       []Option
		wantInfo   *TaskInfo
		wantGroups map[string]map[string][]base.Z
	}{
		{
			desc: "Task with headers and group",
			task: NewTaskWithHeaders("batch_process", []byte("item1"), headers),
			opts: []Option{Group("batch-123")},
			wantInfo: &TaskInfo{
				Queue:         "default",
				Group:         "batch-123",
				Type:          "batch_process",
				Payload:       []byte("item1"),
				Headers:       headers,
				State:         TaskStateAggregating,
				MaxRetry:      defaultMaxRetry,
				Retried:       0,
				LastErr:       "",
				LastFailedAt:  time.Time{},
				Timeout:       defaultTimeout,
				Deadline:      time.Time{},
				NextProcessAt: time.Time{},
			},
			wantGroups: map[string]map[string][]base.Z{
				"default": {
					"batch-123": {
						{
							Message: &base.TaskMessage{
								Type:     "batch_process",
								Payload:  []byte("item1"),
								Headers:  headers,
								Retry:    defaultMaxRetry,
								Queue:    "default",
								Timeout:  int64(defaultTimeout.Seconds()),
								Deadline: noDeadline.Unix(),
								GroupKey: "batch-123",
							},
							Score: now.Unix(),
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		gotInfo, err := client.Enqueue(tc.task, tc.opts...)
		if err != nil {
			t.Errorf("%s: Enqueue failed: %v", tc.desc, err)
			continue
		}

		cmpOptions := []cmp.Option{
			cmpopts.IgnoreFields(TaskInfo{}, "ID"),
			cmpopts.EquateApproxTime(500 * time.Millisecond),
		}
		if diff := cmp.Diff(tc.wantInfo, gotInfo, cmpOptions...); diff != "" {
			t.Errorf("%s;\nEnqueue(task) returned %v, want %v; (-want,+got)\n%s",
				tc.desc, gotInfo, tc.wantInfo, diff)
		}

		for qname, groups := range tc.wantGroups {
			for groupKey, want := range groups {
				got := h.GetGroupEntries(t, r, qname, groupKey)
				if diff := cmp.Diff(want, got, h.IgnoreIDOpt, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("%s;\nmismatch found in %q; (-want,+got)\n%s", tc.desc, base.GroupKey(qname, groupKey), diff)
				}
			}
		}
	}
}
