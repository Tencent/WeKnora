// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/rdb"
	h "github.com/hibiken/asynq/internal/testutil"
	"github.com/hibiken/asynq/internal/timeutil"
	"github.com/redis/go-redis/v9"
)

func testInspectorQueues(t *testing.T, inspector *Inspector, r redis.UniversalClient) {
	tests := []struct {
		queues []string
	}{
		{queues: []string{"default"}},
		{queues: []string{"custom1", "custom2"}},
		{queues: []string{"default", "custom1", "custom2"}},
		{queues: []string{}},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		for _, qname := range tc.queues {
			if err := r.SAdd(context.Background(), base.AllQueues, qname).Err(); err != nil {
				t.Fatalf("could not initialize all queue set: %v", err)
			}
		}
		got, err := inspector.Queues()
		if err != nil {
			t.Errorf("Queues() returned an error: %v", err)
			continue
		}
		if diff := cmp.Diff(tc.queues, got, h.SortStringSliceOpt); diff != "" {
			t.Errorf("Queues() = %v, want %v; (-want, +got)\n%s", got, tc.queues, diff)
		}
	}
}

func TestInspectorQueues(t *testing.T) {
	r := setup(t)
	defer r.Close()
	inspector := NewInspector(getRedisConnOpt(t))
	testInspectorQueues(t, inspector, r)
}

func TestInspectorFromRedisClientQueues(t *testing.T) {
	r := setup(t)
	defer r.Close()
	redisClient := getRedisConnOpt(t).MakeRedisClient().(redis.UniversalClient)
	inspector := NewInspectorFromRedisClient(redisClient)
	testInspectorQueues(t, inspector, r)
}

func TestInspectorDeleteQueue(t *testing.T) {
	r := setup(t)
	defer r.Close()
	inspector := NewInspector(getRedisConnOpt(t))
	defer inspector.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

	tests := []struct {
		pending   map[string][]*base.TaskMessage
		active    map[string][]*base.TaskMessage
		scheduled map[string][]base.Z
		retry     map[string][]base.Z
		archived  map[string][]base.Z
		qname     string // queue to remove
		force     bool
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
			active: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {},
			},
			scheduled: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			retry: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			force: false,
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			active: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {},
			},
			scheduled: map[string][]base.Z{
				"default": {},
				"custom":  {{Message: m4, Score: time.Now().Unix()}},
			},
			retry: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			force: true, // allow removing non-empty queue
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		err := inspector.DeleteQueue(tc.qname, tc.force)
		if err != nil {
			t.Errorf("DeleteQueue(%q, %t) = %v, want nil",
				tc.qname, tc.force, err)
			continue
		}
		if r.SIsMember(context.Background(), base.AllQueues, tc.qname).Val() {
			t.Errorf("%q is a member of %q", tc.qname, base.AllQueues)
		}
	}
}

func TestInspectorDeleteQueueErrorQueueNotEmpty(t *testing.T) {
	r := setup(t)
	defer r.Close()
	inspector := NewInspector(getRedisConnOpt(t))
	defer inspector.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

	tests := []struct {
		pending   map[string][]*base.TaskMessage
		active    map[string][]*base.TaskMessage
		scheduled map[string][]base.Z
		retry     map[string][]base.Z
		archived  map[string][]base.Z
		qname     string // queue to remove
		force     bool
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
			},
			active: map[string][]*base.TaskMessage{
				"default": {m3, m4},
			},
			scheduled: map[string][]base.Z{
				"default": {},
			},
			retry: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			force: false,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		err := inspector.DeleteQueue(tc.qname, tc.force)
		if !errors.Is(err, ErrQueueNotEmpty) {
			t.Errorf("DeleteQueue(%v, %t) did not return ErrQueueNotEmpty",
				tc.qname, tc.force)
		}
	}
}

func TestInspectorDeleteQueueErrorQueueNotFound(t *testing.T) {
	r := setup(t)
	defer r.Close()
	inspector := NewInspector(getRedisConnOpt(t))
	defer inspector.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

	tests := []struct {
		pending   map[string][]*base.TaskMessage
		active    map[string][]*base.TaskMessage
		scheduled map[string][]base.Z
		retry     map[string][]base.Z
		archived  map[string][]base.Z
		qname     string // queue to remove
		force     bool
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
			},
			active: map[string][]*base.TaskMessage{
				"default": {m3, m4},
			},
			scheduled: map[string][]base.Z{
				"default": {},
			},
			retry: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "nonexistent",
			force: false,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		err := inspector.DeleteQueue(tc.qname, tc.force)
		if !errors.Is(err, ErrQueueNotFound) {
			t.Errorf("DeleteQueue(%v, %t) did not return ErrQueueNotFound",
				tc.qname, tc.force)
		}
	}
}

func TestInspectorGetQueueInfo(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessage("task4", nil)
	m5 := h.NewTaskMessageWithQueue("task5", nil, "critical")
	m6 := h.NewTaskMessageWithQueue("task6", nil, "low")
	now := time.Now()
	timeCmpOpt := cmpopts.EquateApproxTime(time.Second)
	ignoreMemUsg := cmpopts.IgnoreFields(QueueInfo{}, "MemoryUsage")

	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		pending                         map[string][]*base.TaskMessage
		active                          map[string][]*base.TaskMessage
		scheduled                       map[string][]base.Z
		retry                           map[string][]base.Z
		archived                        map[string][]base.Z
		completed                       map[string][]base.Z
		processed                       map[string]int
		failed                          map[string]int
		processedTotal                  map[string]int
		failedTotal                     map[string]int
		oldestPendingMessageEnqueueTime map[string]time.Time
		qname                           string
		want                            *QueueInfo
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default":  {m1},
				"critical": {m5},
				"low":      {m6},
			},
			active: map[string][]*base.TaskMessage{
				"default":  {m2},
				"critical": {},
				"low":      {},
			},
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m3, Score: now.Add(time.Hour).Unix()},
					{Message: m4, Score: now.Unix()},
				},
				"critical": {},
				"low":      {},
			},
			retry: map[string][]base.Z{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			archived: map[string][]base.Z{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			completed: map[string][]base.Z{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			processed: map[string]int{
				"default":  120,
				"critical": 100,
				"low":      42,
			},
			failed: map[string]int{
				"default":  2,
				"critical": 0,
				"low":      5,
			},
			processedTotal: map[string]int{
				"default":  11111,
				"critical": 22222,
				"low":      33333,
			},
			failedTotal: map[string]int{
				"default":  111,
				"critical": 222,
				"low":      333,
			},
			oldestPendingMessageEnqueueTime: map[string]time.Time{
				"default":  now.Add(-15 * time.Second),
				"critical": now.Add(-200 * time.Millisecond),
				"low":      now.Add(-30 * time.Second),
			},
			qname: "default",
			want: &QueueInfo{
				Queue:          "default",
				Latency:        15 * time.Second,
				Size:           4,
				Pending:        1,
				Active:         1,
				Scheduled:      2,
				Retry:          0,
				Archived:       0,
				Completed:      0,
				Processed:      120,
				Failed:         2,
				ProcessedTotal: 11111,
				FailedTotal:    111,
				Paused:         false,
				Timestamp:      now,
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)
		h.SeedAllCompletedQueues(t, r, tc.completed)
		ctx := context.Background()
		for qname, n := range tc.processed {
			r.Set(ctx, base.ProcessedKey(qname, now), n, 0)
		}
		for qname, n := range tc.failed {
			r.Set(ctx, base.FailedKey(qname, now), n, 0)
		}
		for qname, n := range tc.processedTotal {
			r.Set(ctx, base.ProcessedTotalKey(qname), n, 0)
		}
		for qname, n := range tc.failedTotal {
			r.Set(ctx, base.FailedTotalKey(qname), n, 0)
		}
		for qname, enqueueTime := range tc.oldestPendingMessageEnqueueTime {
			if enqueueTime.IsZero() {
				continue
			}
			oldestPendingMessageID := r.LRange(ctx, base.PendingKey(qname), -1, -1).Val()[0] // get the right most msg in the list
			r.HSet(ctx, base.TaskKey(qname, oldestPendingMessageID), "pending_since", enqueueTime.UnixNano())
		}

		got, err := inspector.GetQueueInfo(tc.qname)
		if err != nil {
			t.Errorf("r.GetQueueInfo(%q) = %v, %v, want %v, nil",
				tc.qname, got, err, tc.want)
			continue
		}
		if diff := cmp.Diff(tc.want, got, timeCmpOpt, ignoreMemUsg); diff != "" {
			t.Errorf("r.GetQueueInfo(%q) = %v, %v, want %v, nil; (-want, +got)\n%s",
				tc.qname, got, err, tc.want, diff)
			continue
		}
	}

}

func TestInspectorHistory(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now().UTC()
	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		qname string // queue of interest
		n     int    // number of days
	}{
		{"default", 90},
		{"custom", 7},
		{"default", 1},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		r.SAdd(context.Background(), base.AllQueues, tc.qname)
		// populate last n days data
		for i := 0; i < tc.n; i++ {
			ts := now.Add(-time.Duration(i) * 24 * time.Hour)
			processedKey := base.ProcessedKey(tc.qname, ts)
			failedKey := base.FailedKey(tc.qname, ts)
			r.Set(context.Background(), processedKey, (i+1)*1000, 0)
			r.Set(context.Background(), failedKey, (i+1)*10, 0)
		}

		got, err := inspector.History(tc.qname, tc.n)
		if err != nil {
			t.Errorf("Inspector.History(%q, %d) returned error: %v", tc.qname, tc.n, err)
			continue
		}
		if len(got) != tc.n {
			t.Errorf("Inspector.History(%q, %d) returned %d daily stats, want %d",
				tc.qname, tc.n, len(got), tc.n)
			continue
		}
		for i := 0; i < tc.n; i++ {
			want := &DailyStats{
				Queue:     tc.qname,
				Processed: (i + 1) * 1000,
				Failed:    (i + 1) * 10,
				Date:      now.Add(-time.Duration(i) * 24 * time.Hour),
			}
			// Allow 2 seconds difference in timestamp.
			timeCmpOpt := cmpopts.EquateApproxTime(2 * time.Second)
			if diff := cmp.Diff(want, got[i], timeCmpOpt); diff != "" {
				t.Errorf("Inspector.History %d days ago data; got %+v, want %+v; (-want,+got):\n%s",
					i, got[i], want, diff)
			}
		}
	}
}

func createPendingTask(msg *base.TaskMessage) *TaskInfo {
	return newTaskInfo(msg, base.TaskStatePending, time.Now(), nil)
}

func TestInspectorGetTaskInfo(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	m2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	m5 := h.NewTaskMessageWithQueue("task5", nil, "custom")

	now := time.Now()
	fiveMinsFromNow := now.Add(5 * time.Minute)
	oneHourFromNow := now.Add(1 * time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)

	fixtures := struct {
		active    map[string][]*base.TaskMessage
		pending   map[string][]*base.TaskMessage
		scheduled map[string][]base.Z
		retry     map[string][]base.Z
		archived  map[string][]base.Z
	}{
		active: map[string][]*base.TaskMessage{
			"default": {m1},
			"custom":  {},
		},
		pending: map[string][]*base.TaskMessage{
			"default": {},
			"custom":  {m5},
		},
		scheduled: map[string][]base.Z{
			"default": {{Message: m2, Score: fiveMinsFromNow.Unix()}},
			"custom":  {},
		},
		retry: map[string][]base.Z{
			"default": {},
			"custom":  {{Message: m3, Score: oneHourFromNow.Unix()}},
		},
		archived: map[string][]base.Z{
			"default": {},
			"custom":  {{Message: m4, Score: twoHoursAgo.Unix()}},
		},
	}

	h.SeedAllActiveQueues(t, r, fixtures.active)
	h.SeedAllPendingQueues(t, r, fixtures.pending)
	h.SeedAllScheduledQueues(t, r, fixtures.scheduled)
	h.SeedAllRetryQueues(t, r, fixtures.retry)
	h.SeedAllArchivedQueues(t, r, fixtures.archived)

	tests := []struct {
		qname string
		id    string
		want  *TaskInfo
	}{
		{
			qname: "default",
			id:    m1.ID,
			want: newTaskInfo(
				m1,
				base.TaskStateActive,
				time.Time{}, // zero value for n/a
				nil,
			),
		},
		{
			qname: "default",
			id:    m2.ID,
			want: newTaskInfo(
				m2,
				base.TaskStateScheduled,
				fiveMinsFromNow,
				nil,
			),
		},
		{
			qname: "custom",
			id:    m3.ID,
			want: newTaskInfo(
				m3,
				base.TaskStateRetry,
				oneHourFromNow,
				nil,
			),
		},
		{
			qname: "custom",
			id:    m4.ID,
			want: newTaskInfo(
				m4,
				base.TaskStateArchived,
				time.Time{}, // zero value for n/a
				nil,
			),
		},
		{
			qname: "custom",
			id:    m5.ID,
			want: newTaskInfo(
				m5,
				base.TaskStatePending,
				now,
				nil,
			),
		},
	}

	inspector := NewInspector(getRedisConnOpt(t))
	for _, tc := range tests {
		got, err := inspector.GetTaskInfo(tc.qname, tc.id)
		if err != nil {
			t.Errorf("GetTaskInfo(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		cmpOpts := []cmp.Option{
			cmp.AllowUnexported(TaskInfo{}),
			cmpopts.EquateApproxTime(2 * time.Second),
		}
		if diff := cmp.Diff(tc.want, got, cmpOpts...); diff != "" {
			t.Errorf("GetTaskInfo(%q, %q) = %v, want %v; (-want, +got)\n%s", tc.qname, tc.id, got, tc.want, diff)
		}
	}
}

func TestInspectorGetTaskInfoError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	m2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	m5 := h.NewTaskMessageWithQueue("task5", nil, "custom")

	now := time.Now()
	fiveMinsFromNow := now.Add(5 * time.Minute)
	oneHourFromNow := now.Add(1 * time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)

	fixtures := struct {
		active    map[string][]*base.TaskMessage
		pending   map[string][]*base.TaskMessage
		scheduled map[string][]base.Z
		retry     map[string][]base.Z
		archived  map[string][]base.Z
	}{
		active: map[string][]*base.TaskMessage{
			"default": {m1},
			"custom":  {},
		},
		pending: map[string][]*base.TaskMessage{
			"default": {},
			"custom":  {m5},
		},
		scheduled: map[string][]base.Z{
			"default": {{Message: m2, Score: fiveMinsFromNow.Unix()}},
			"custom":  {},
		},
		retry: map[string][]base.Z{
			"default": {},
			"custom":  {{Message: m3, Score: oneHourFromNow.Unix()}},
		},
		archived: map[string][]base.Z{
			"default": {},
			"custom":  {{Message: m4, Score: twoHoursAgo.Unix()}},
		},
	}

	h.SeedAllActiveQueues(t, r, fixtures.active)
	h.SeedAllPendingQueues(t, r, fixtures.pending)
	h.SeedAllScheduledQueues(t, r, fixtures.scheduled)
	h.SeedAllRetryQueues(t, r, fixtures.retry)
	h.SeedAllArchivedQueues(t, r, fixtures.archived)

	tests := []struct {
		qname   string
		id      string
		wantErr error
	}{
		{
			qname:   "nonexistent",
			id:      m1.ID,
			wantErr: ErrQueueNotFound,
		},
		{
			qname:   "default",
			id:      uuid.NewString(),
			wantErr: ErrTaskNotFound,
		},
	}

	inspector := NewInspector(getRedisConnOpt(t))

	for _, tc := range tests {
		info, err := inspector.GetTaskInfo(tc.qname, tc.id)
		if info != nil {
			t.Errorf("GetTaskInfo(%q, %q) returned info: %v", tc.qname, tc.id, info)
		}
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("GetTaskInfo(%q, %q) returned unexpected error: %v, want %v", tc.qname, tc.id, err, tc.wantErr)
		}
	}
}

func TestInspectorListPendingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "critical")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "low")

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		desc    string
		pending map[string][]*base.TaskMessage
		qname   string
		want    []*TaskInfo
	}{
		{
			desc: "with default queue",
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
			},
			qname: "default",
			want: []*TaskInfo{
				createPendingTask(m1),
				createPendingTask(m2),
			},
		},
		{
			desc: "with named queue",
			pending: map[string][]*base.TaskMessage{
				"default":  {m1, m2},
				"critical": {m3},
				"low":      {m4},
			},
			qname: "critical",
			want: []*TaskInfo{
				createPendingTask(m3),
			},
		},
		{
			desc: "with empty queue",
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			qname: "default",
			want:  []*TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		for q, msgs := range tc.pending {
			h.SeedPendingQueue(t, r, msgs, q)
		}

		got, err := inspector.ListPendingTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; ListPendingTasks(%q) returned error: %v",
				tc.desc, tc.qname, err)
			continue
		}
		cmpOpts := []cmp.Option{
			cmpopts.EquateApproxTime(2 * time.Second),
			cmp.AllowUnexported(TaskInfo{}),
		}
		if diff := cmp.Diff(tc.want, got, cmpOpts...); diff != "" {
			t.Errorf("%s; ListPendingTasks(%q) = %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.qname, got, tc.want, diff)
		}
	}
}

func newOrphanedTaskInfo(msg *base.TaskMessage) *TaskInfo {
	info := newTaskInfo(msg, base.TaskStateActive, time.Time{}, nil)
	info.IsOrphaned = true
	return info
}

func TestInspectorListActiveTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

	inspector := NewInspector(getRedisConnOpt(t))
	now := time.Now()

	tests := []struct {
		desc   string
		active map[string][]*base.TaskMessage
		lease  map[string][]base.Z
		qname  string
		want   []*TaskInfo
	}{
		{
			desc: "with a few active tasks",
			active: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3, m4},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Add(20 * time.Second).Unix()},
					{Message: m2, Score: now.Add(20 * time.Second).Unix()},
				},
				"custom": {
					{Message: m3, Score: now.Add(20 * time.Second).Unix()},
					{Message: m4, Score: now.Add(20 * time.Second).Unix()},
				},
			},
			qname: "custom",
			want: []*TaskInfo{
				newTaskInfo(m3, base.TaskStateActive, time.Time{}, nil),
				newTaskInfo(m4, base.TaskStateActive, time.Time{}, nil),
			},
		},
		{
			desc: "with an orphaned task",
			active: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3, m4},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Add(20 * time.Second).Unix()},
					{Message: m2, Score: now.Add(-10 * time.Second).Unix()}, // orphaned task
				},
				"custom": {
					{Message: m3, Score: now.Add(20 * time.Second).Unix()},
					{Message: m4, Score: now.Add(20 * time.Second).Unix()},
				},
			},
			qname: "default",
			want: []*TaskInfo{
				newTaskInfo(m1, base.TaskStateActive, time.Time{}, nil),
				newOrphanedTaskInfo(m2),
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllLease(t, r, tc.lease)

		got, err := inspector.ListActiveTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; ListActiveTasks(%q) returned error: %v", tc.qname, tc.desc, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(TaskInfo{})); diff != "" {
			t.Errorf("%s; ListActiveTask(%q) = %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.qname, got, tc.want, diff)
		}
	}
}

func createScheduledTask(z base.Z) *TaskInfo {
	return newTaskInfo(
		z.Message,
		base.TaskStateScheduled,
		time.Unix(z.Score, 0),
		nil,
	)
}

func TestInspectorListScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		desc      string
		scheduled map[string][]base.Z
		qname     string
		want      []*TaskInfo
	}{
		{
			desc: "with a few scheduled tasks",
			scheduled: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			// Should be sorted by NextProcessAt.
			want: []*TaskInfo{
				createScheduledTask(z3),
				createScheduledTask(z1),
				createScheduledTask(z2),
			},
		},
		{
			desc: "with empty scheduled queue",
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)

		got, err := inspector.ListScheduledTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; ListScheduledTasks(%q) returned error: %v", tc.desc, tc.qname, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(TaskInfo{})); diff != "" {
			t.Errorf("%s; ListScheduledTask(%q) = %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.qname, got, tc.want, diff)
		}
	}
}

func createRetryTask(z base.Z) *TaskInfo {
	return newTaskInfo(
		z.Message,
		base.TaskStateRetry,
		time.Unix(z.Score, 0),
		nil,
	)
}

func TestInspectorListRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		desc  string
		retry map[string][]base.Z
		qname string
		want  []*TaskInfo
	}{
		{
			desc: "with a few retry tasks",
			retry: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			// Should be sorted by NextProcessAt.
			want: []*TaskInfo{
				createRetryTask(z3),
				createRetryTask(z1),
				createRetryTask(z2),
			},
		},
		{
			desc: "with empty retry queue",
			retry: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*TaskInfo(nil),
		},
		// TODO(hibiken): ErrQueueNotFound when queue doesn't exist
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)

		got, err := inspector.ListRetryTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; ListRetryTasks(%q) returned error: %v", tc.desc, tc.qname, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(TaskInfo{})); diff != "" {
			t.Errorf("%s; ListRetryTask(%q) = %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.qname, got, tc.want, diff)
		}
	}
}

func createArchivedTask(z base.Z) *TaskInfo {
	return newTaskInfo(
		z.Message,
		base.TaskStateArchived,
		time.Time{}, // zero value for n/a
		nil,
	)
}

func TestInspectorListArchivedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(-5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(-15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(-2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(-2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		desc     string
		archived map[string][]base.Z
		qname    string
		want     []*TaskInfo
	}{
		{
			desc: "with a few archived tasks",
			archived: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			// Should be sorted by LastFailedAt.
			want: []*TaskInfo{
				createArchivedTask(z2),
				createArchivedTask(z1),
				createArchivedTask(z3),
			},
		},
		{
			desc: "with empty archived queue",
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		got, err := inspector.ListArchivedTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; ListArchivedTasks(%q) returned error: %v", tc.desc, tc.qname, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(TaskInfo{})); diff != "" {
			t.Errorf("%s; ListArchivedTask(%q) = %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.qname, got, tc.want, diff)
		}
	}
}

func newCompletedTaskMessage(typename, qname string, retention time.Duration, completedAt time.Time) *base.TaskMessage {
	msg := h.NewTaskMessageWithQueue(typename, nil, qname)
	msg.Retention = int64(retention.Seconds())
	msg.CompletedAt = completedAt.Unix()
	return msg
}

func createCompletedTask(z base.Z) *TaskInfo {
	return newTaskInfo(
		z.Message,
		base.TaskStateCompleted,
		time.Time{}, // zero value for n/a
		nil,         // TODO: Test with result data
	)
}

func TestInspectorListCompletedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	m1 := newCompletedTaskMessage("task1", "default", 1*time.Hour, now.Add(-3*time.Minute))     // Expires in 57 mins
	m2 := newCompletedTaskMessage("task2", "default", 30*time.Minute, now.Add(-10*time.Minute)) // Expires in 20 mins
	m3 := newCompletedTaskMessage("task3", "default", 2*time.Hour, now.Add(-30*time.Minute))    // Expires in 90 mins
	m4 := newCompletedTaskMessage("task4", "custom", 15*time.Minute, now.Add(-2*time.Minute))   // Expires in 13 mins
	z1 := base.Z{Message: m1, Score: m1.CompletedAt + m1.Retention}
	z2 := base.Z{Message: m2, Score: m2.CompletedAt + m2.Retention}
	z3 := base.Z{Message: m3, Score: m3.CompletedAt + m3.Retention}
	z4 := base.Z{Message: m4, Score: m4.CompletedAt + m4.Retention}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		desc      string
		completed map[string][]base.Z
		qname     string
		want      []*TaskInfo
	}{
		{
			desc: "with a few completed tasks",
			completed: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			// Should be sorted by expiration time (CompletedAt + Retention).
			want: []*TaskInfo{
				createCompletedTask(z2),
				createCompletedTask(z1),
				createCompletedTask(z3),
			},
		},
		{
			desc: "with empty completed queue",
			completed: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllCompletedQueues(t, r, tc.completed)

		got, err := inspector.ListCompletedTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; ListCompletedTasks(%q) returned error: %v", tc.desc, tc.qname, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(TaskInfo{})); diff != "" {
			t.Errorf("%s; ListCompletedTasks(%q) = %v, want %v; (-want,+got)\n%s",
				tc.desc, tc.qname, got, tc.want, diff)
		}
	}
}

func TestInspectorListAggregatingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()

	m1 := h.NewTaskMessageBuilder().SetType("task1").SetQueue("default").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetType("task2").SetQueue("default").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetType("task3").SetQueue("default").SetGroup("group1").Build()
	m4 := h.NewTaskMessageBuilder().SetType("task4").SetQueue("default").SetGroup("group2").Build()
	m5 := h.NewTaskMessageBuilder().SetType("task5").SetQueue("custom").SetGroup("group1").Build()

	inspector := NewInspector(getRedisConnOpt(t))

	fxt := struct {
		tasks     []*h.TaskSeedData
		allQueues []string
		allGroups map[string][]string
		groups    map[string][]redis.Z
	}{
		tasks: []*h.TaskSeedData{
			{Msg: m1, State: base.TaskStateAggregating},
			{Msg: m2, State: base.TaskStateAggregating},
			{Msg: m3, State: base.TaskStateAggregating},
			{Msg: m4, State: base.TaskStateAggregating},
			{Msg: m5, State: base.TaskStateAggregating},
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1", "group2"},
			base.AllGroups("custom"):  {"group1"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-30 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m3.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
			},
			base.GroupKey("default", "group2"): {
				{Member: m4.ID, Score: float64(now.Add(-30 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group1"): {
				{Member: m5.ID, Score: float64(now.Add(-30 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc  string
		qname string
		gname string
		want  []*TaskInfo
	}{
		{
			desc:  "default queue group1",
			qname: "default",
			gname: "group1",
			want: []*TaskInfo{
				createAggregatingTaskInfo(m1),
				createAggregatingTaskInfo(m2),
				createAggregatingTaskInfo(m3),
			},
		},
		{
			desc:  "custom queue group1",
			qname: "custom",
			gname: "group1",
			want: []*TaskInfo{
				createAggregatingTaskInfo(m5),
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedTasks(t, r, fxt.tasks)
		h.SeedRedisSet(t, r, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r, fxt.allGroups)
		h.SeedRedisZSets(t, r, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := inspector.ListAggregatingTasks(tc.qname, tc.gname)
			if err != nil {
				t.Fatalf("ListAggregatingTasks returned error: %v", err)
			}

			cmpOpts := []cmp.Option{
				cmpopts.EquateApproxTime(2 * time.Second),
				cmp.AllowUnexported(TaskInfo{}),
			}
			if diff := cmp.Diff(tc.want, got, cmpOpts...); diff != "" {
				t.Errorf("ListAggregatingTasks = %v, want = %v; (-want,+got)\n%s", got, tc.want, diff)
			}
		})
	}
}

func createAggregatingTaskInfo(msg *base.TaskMessage) *TaskInfo {
	return newTaskInfo(msg, base.TaskStateAggregating, time.Time{}, nil)
}

func TestInspectorListPagination(t *testing.T) {
	// Create 100 tasks.
	var msgs []*base.TaskMessage
	for i := 0; i <= 99; i++ {
		msgs = append(msgs,
			h.NewTaskMessage(fmt.Sprintf("task%d", i), nil))
	}
	r := setup(t)
	defer r.Close()
	h.SeedPendingQueue(t, r, msgs, base.DefaultQueueName)

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		page     int
		pageSize int
		want     []*TaskInfo
	}{
		{
			page:     1,
			pageSize: 5,
			want: []*TaskInfo{
				createPendingTask(msgs[0]),
				createPendingTask(msgs[1]),
				createPendingTask(msgs[2]),
				createPendingTask(msgs[3]),
				createPendingTask(msgs[4]),
			},
		},
		{
			page:     3,
			pageSize: 10,
			want: []*TaskInfo{
				createPendingTask(msgs[20]),
				createPendingTask(msgs[21]),
				createPendingTask(msgs[22]),
				createPendingTask(msgs[23]),
				createPendingTask(msgs[24]),
				createPendingTask(msgs[25]),
				createPendingTask(msgs[26]),
				createPendingTask(msgs[27]),
				createPendingTask(msgs[28]),
				createPendingTask(msgs[29]),
			},
		},
	}

	for _, tc := range tests {
		got, err := inspector.ListPendingTasks("default", Page(tc.page), PageSize(tc.pageSize))
		if err != nil {
			t.Errorf("ListPendingTask('default') returned error: %v", err)
			continue
		}
		cmpOpts := []cmp.Option{
			cmpopts.EquateApproxTime(2 * time.Second),
			cmp.AllowUnexported(TaskInfo{}),
		}
		if diff := cmp.Diff(tc.want, got, cmpOpts...); diff != "" {
			t.Errorf("ListPendingTask('default') = %v, want %v; (-want,+got)\n%s",
				got, tc.want, diff)
		}
	}
}

func TestInspectorListTasksQueueNotFoundError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		qname   string
		wantErr error
	}{
		{
			qname:   "nonexistent",
			wantErr: ErrQueueNotFound,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		if _, err := inspector.ListActiveTasks(tc.qname); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListActiveTasks(%q) returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
		if _, err := inspector.ListPendingTasks(tc.qname); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListPendingTasks(%q) returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
		if _, err := inspector.ListScheduledTasks(tc.qname); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListScheduledTasks(%q) returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
		if _, err := inspector.ListRetryTasks(tc.qname); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListRetryTasks(%q) returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
		if _, err := inspector.ListArchivedTasks(tc.qname); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListArchivedTasks(%q) returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
		if _, err := inspector.ListCompletedTasks(tc.qname); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListCompletedTasks(%q) returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
		if _, err := inspector.ListAggregatingTasks(tc.qname, "mygroup"); !errors.Is(err, tc.wantErr) {
			t.Errorf("ListAggregatingTasks(%q, \"mygroup\") returned error %v, want %v", tc.qname, err, tc.wantErr)
		}
	}
}

func TestInspectorDeleteAllPendingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		pending     map[string][]*base.TaskMessage
		qname       string
		want        int
		wantPending map[string][]*base.TaskMessage
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2, m3},
				"custom":  {m4},
			},
			qname: "default",
			want:  3,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m4},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2, m3},
				"custom":  {m4},
			},
			qname: "custom",
			want:  1,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m2, m3},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)

		got, err := inspector.DeleteAllPendingTasks(tc.qname)
		if err != nil {
			t.Errorf("DeleteAllPendingTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DeleteAllPendingTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorDeleteAllScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		want          int
		wantScheduled map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			want:  3,
			wantScheduled: map[string][]base.Z{
				"default": {},
				"custom":  {z4},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)

		got, err := inspector.DeleteAllScheduledTasks(tc.qname)
		if err != nil {
			t.Errorf("DeleteAllScheduledTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DeleteAllScheduledTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorDeleteAllRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		retry     map[string][]base.Z
		qname     string
		want      int
		wantRetry map[string][]base.Z
	}{
		{
			retry: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			want:  3,
			wantRetry: map[string][]base.Z{
				"default": {},
				"custom":  {z4},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)

		got, err := inspector.DeleteAllRetryTasks(tc.qname)
		if err != nil {
			t.Errorf("DeleteAllRetryTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DeleteAllRetryTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorDeleteAllArchivedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		want         int
		wantArchived map[string][]base.Z
	}{
		{
			archived: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			want:  3,
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom":  {z4},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantArchived: map[string][]base.Z{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		got, err := inspector.DeleteAllArchivedTasks(tc.qname)
		if err != nil {
			t.Errorf("DeleteAllArchivedTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DeleteAllArchivedTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorDeleteAllCompletedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	m1 := newCompletedTaskMessage("task1", "default", 30*time.Minute, now.Add(-2*time.Minute))
	m2 := newCompletedTaskMessage("task2", "default", 30*time.Minute, now.Add(-5*time.Minute))
	m3 := newCompletedTaskMessage("task3", "default", 30*time.Minute, now.Add(-10*time.Minute))
	m4 := newCompletedTaskMessage("task4", "custom", 30*time.Minute, now.Add(-3*time.Minute))
	z1 := base.Z{Message: m1, Score: m1.CompletedAt + m1.Retention}
	z2 := base.Z{Message: m2, Score: m2.CompletedAt + m2.Retention}
	z3 := base.Z{Message: m3, Score: m3.CompletedAt + m3.Retention}
	z4 := base.Z{Message: m4, Score: m4.CompletedAt + m4.Retention}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		completed     map[string][]base.Z
		qname         string
		want          int
		wantCompleted map[string][]base.Z
	}{
		{
			completed: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			qname: "default",
			want:  3,
			wantCompleted: map[string][]base.Z{
				"default": {},
				"custom":  {z4},
			},
		},
		{
			completed: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantCompleted: map[string][]base.Z{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllCompletedQueues(t, r, tc.completed)

		got, err := inspector.DeleteAllCompletedTasks(tc.qname)
		if err != nil {
			t.Errorf("DeleteAllCompletedTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DeleteAllCompletedTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantCompleted {
			gotCompleted := h.GetCompletedEntries(t, r, qname)
			if diff := cmp.Diff(want, gotCompleted, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected completed tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorArchiveAllPendingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		pending      map[string][]*base.TaskMessage
		archived     map[string][]base.Z
		qname        string
		want         int
		wantPending  map[string][]*base.TaskMessage
		wantArchived map[string][]base.Z
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2, m3},
				"custom":  {m4},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "default",
			want:  3,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m4},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					base.Z{Message: m1, Score: now.Unix()},
					base.Z{Message: m2, Score: now.Unix()},
					base.Z{Message: m3, Score: now.Unix()},
				},
				"custom": {},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m3},
			},
			archived: map[string][]base.Z{
				"default": {z1, z2},
			},
			qname: "default",
			want:  1,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					z1,
					z2,
					base.Z{Message: m3, Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		got, err := inspector.ArchiveAllPendingTasks(tc.qname)
		if err != nil {
			t.Errorf("ArchiveAllPendingTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ArchiveAllPendingTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorArchiveAllScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		scheduled     map[string][]base.Z
		archived      map[string][]base.Z
		qname         string
		want          int
		wantScheduled map[string][]base.Z
		wantArchived  map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "default",
			want:  3,
			wantScheduled: map[string][]base.Z{
				"default": {},
				"custom":  {z4},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					base.Z{Message: m1, Score: now.Unix()},
					base.Z{Message: m2, Score: now.Unix()},
					base.Z{Message: m3, Score: now.Unix()},
				},
				"custom": {},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {z1, z2},
			},
			archived: map[string][]base.Z{
				"default": {z3},
			},
			qname: "default",
			want:  2,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					z3,
					base.Z{Message: m1, Score: now.Unix()},
					base.Z{Message: m2, Score: now.Unix()},
				},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {z1, z2},
			},
			qname: "default",
			want:  0,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {z1, z2},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		got, err := inspector.ArchiveAllScheduledTasks(tc.qname)
		if err != nil {
			t.Errorf("ArchiveAllScheduledTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ArchiveAllScheduledTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorArchiveAllRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		retry        map[string][]base.Z
		archived     map[string][]base.Z
		qname        string
		want         int
		wantRetry    map[string][]base.Z
		wantArchived map[string][]base.Z
	}{
		{
			retry: map[string][]base.Z{
				"default": {z1, z2, z3},
				"custom":  {z4},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "default",
			want:  3,
			wantRetry: map[string][]base.Z{
				"default": {},
				"custom":  {z4},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					base.Z{Message: m1, Score: now.Unix()},
					base.Z{Message: m2, Score: now.Unix()},
					base.Z{Message: m3, Score: now.Unix()},
				},
				"custom": {},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {z1, z2},
			},
			archived: map[string][]base.Z{
				"default": {z3},
			},
			qname: "default",
			want:  2,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					z3,
					base.Z{Message: m1, Score: now.Unix()},
					base.Z{Message: m2, Score: now.Unix()},
				},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {z1, z2},
			},
			qname: "default",
			want:  0,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {z1, z2},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		got, err := inspector.ArchiveAllRetryTasks(tc.qname)
		if err != nil {
			t.Errorf("ArchiveAllRetryTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ArchiveAllRetryTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorRunAllScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "critical")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "low")
	m4 := h.NewTaskMessage("task4", nil)
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		scheduled     map[string][]base.Z
		pending       map[string][]*base.TaskMessage
		qname         string
		want          int
		wantScheduled map[string][]base.Z
		wantPending   map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default":  {z1, z4},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qname: "default",
			want:  2,
			wantScheduled: map[string][]base.Z{
				"default":  {},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {m1, m4},
				"critical": {},
				"low":      {},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {m4},
				"critical": {},
				"low":      {},
			},
			qname: "default",
			want:  1,
			wantScheduled: map[string][]base.Z{
				"default":  {},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {m4, m1},
				"critical": {},
				"low":      {},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m4},
			},
			qname: "default",
			want:  0,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m4},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllPendingQueues(t, r, tc.pending)

		got, err := inspector.RunAllScheduledTasks(tc.qname)
		if err != nil {
			t.Errorf("RunAllScheduledTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("RunAllScheduledTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorRunAllRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "critical")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "low")
	m4 := h.NewTaskMessage("task2", nil)
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		retry       map[string][]base.Z
		pending     map[string][]*base.TaskMessage
		qname       string
		want        int
		wantRetry   map[string][]base.Z
		wantPending map[string][]*base.TaskMessage
	}{
		{
			retry: map[string][]base.Z{
				"default":  {z1, z4},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qname: "default",
			want:  2,
			wantRetry: map[string][]base.Z{
				"default":  {},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {m1, m4},
				"critical": {},
				"low":      {},
			},
		},
		{
			retry: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {m4},
				"critical": {},
				"low":      {},
			},
			qname: "default",
			want:  1,
			wantRetry: map[string][]base.Z{
				"default":  {},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {m4, m1},
				"critical": {},
				"low":      {},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m4},
			},
			qname: "default",
			want:  0,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m4},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllPendingQueues(t, r, tc.pending)

		got, err := inspector.RunAllRetryTasks(tc.qname)
		if err != nil {
			t.Errorf("RunAllRetryTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("RunAllRetryTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorRunAllArchivedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "critical")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "low")
	m4 := h.NewTaskMessage("task2", nil)
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(-5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(-15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(-2 * time.Minute).Unix()}
	z4 := base.Z{Message: m4, Score: now.Add(-2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		archived     map[string][]base.Z
		pending      map[string][]*base.TaskMessage
		qname        string
		want         int
		wantArchived map[string][]base.Z
		wantPending  map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default":  {z1, z4},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qname: "default",
			want:  2,
			wantArchived: map[string][]base.Z{
				"default":  {},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {m1, m4},
				"critical": {},
				"low":      {},
			},
		},
		{
			archived: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {m4},
				"critical": {},
			},
			qname: "default",
			want:  1,
			wantArchived: map[string][]base.Z{
				"default":  {},
				"critical": {z2},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {m4, m1},
				"critical": {},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m4},
			},
			qname: "default",
			want:  0,
			wantArchived: map[string][]base.Z{
				"default": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m4},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)
		h.SeedAllPendingQueues(t, r, tc.pending)

		got, err := inspector.RunAllArchivedTasks(tc.qname)
		if err != nil {
			t.Errorf("RunAllArchivedTasks(%q) returned error: %v", tc.qname, err)
			continue
		}
		if got != tc.want {
			t.Errorf("RunAllArchivedTasks(%q) = %d, want %d", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}

		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorUpdateTaskPayloadUpdatesScheduledTaskPayload(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1_old := h.NewTaskMessage("task1", []byte("m1_old"))
	m1_new := h.NewTaskMessage("task1", nil)
	m1_new.ID = m1_old.ID
	m2_old := h.NewTaskMessage("task2", nil)
	m2_new := h.NewTaskMessage("task2", []byte("m2_new"))
	m2_new.ID = m2_old.ID
	m3_old := h.NewTaskMessageWithQueue("task3", []byte("m3_old"), "custom")
	m3_new := h.NewTaskMessageWithQueue("task3", []byte("m3_new"), "custom")
	m3_new.ID = m3_old.ID

	now := time.Now()
	z1_old := base.Z{Message: m1_old, Score: now.Add(5 * time.Minute).Unix()}
	z1_new := base.Z{Message: m1_new, Score: now.Add(5 * time.Minute).Unix()}
	z2_old := base.Z{Message: m2_old, Score: now.Add(15 * time.Minute).Unix()}
	z2_new := base.Z{Message: m2_new, Score: now.Add(15 * time.Minute).Unix()}
	z3_old := base.Z{Message: m3_old, Score: now.Add(2 * time.Minute).Unix()}
	z3_new := base.Z{Message: m3_new, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		id            string
		newPayload    []byte
		wantScheduled map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {z1_old, z2_old},
				"custom":  {z3_old},
			},
			qname:      "default",
			id:         createScheduledTask(z2_old).ID,
			newPayload: m2_new.Payload,
			wantScheduled: map[string][]base.Z{
				"default": {z1_old, z2_new},
				"custom":  {z3_old},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {z1_old, z2_old},
				"custom":  {z3_old},
			},
			qname:      "default",
			id:         createScheduledTask(z1_old).ID,
			newPayload: m1_new.Payload,
			wantScheduled: map[string][]base.Z{
				"default": {z1_new, z2_old},
				"custom":  {z3_old},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {z1_old, z2_old},
				"custom":  {z3_old},
			},
			qname:      "custom",
			id:         createScheduledTask(z3_old).ID,
			newPayload: m3_new.Payload,
			wantScheduled: map[string][]base.Z{
				"default": {z1_old, z2_old},
				"custom":  {z3_new},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)

		if err := inspector.UpdateTaskPayload(tc.qname, tc.id, tc.newPayload); err != nil {
			t.Errorf("UpdateTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}

		}
	}
}

func TestInspectorUpdateTaskPayloadError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		tasks      map[string][]base.Z
		qname      string
		id         string
		newPayload []byte
		wantErr    error
	}{
		{
			tasks: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname:      "nonexistent",
			id:         createScheduledTask(z2).ID,
			newPayload: nil,
			wantErr:    ErrQueueNotFound,
		},
		{
			tasks: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname:      "default",
			id:         uuid.NewString(),
			newPayload: nil,
			wantErr:    ErrTaskNotFound,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.tasks)

		if err := inspector.UpdateTaskPayload(tc.qname, tc.id, tc.newPayload); !errors.Is(err, tc.wantErr) {
			t.Errorf("UpdateTask(%q, %q) = %v, want %v", tc.qname, tc.id, err, tc.wantErr)
			continue
		}
	}
}

func TestInspectorDeleteTaskDeletesPendingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		pending     map[string][]*base.TaskMessage
		qname       string
		id          string
		wantPending map[string][]*base.TaskMessage
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			qname: "default",
			id:    createPendingTask(m2).ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1},
				"custom":  {m3},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			qname: "custom",
			id:    createPendingTask(m3).ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)

		if err := inspector.DeleteTask(tc.qname, tc.id); err != nil {
			t.Errorf("DeleteTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}

		for qname, want := range tc.wantPending {
			got := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, got, h.SortMsgOpt); diff != "" {
				t.Errorf("unspected pending tasks in queue %q: (-want,+got):\n%s",
					qname, diff)
				continue
			}
		}
	}
}

func TestInspectorDeleteTaskDeletesScheduledTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		id            string
		wantScheduled map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname: "default",
			id:    createScheduledTask(z2).ID,
			wantScheduled: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)

		if err := inspector.DeleteTask(tc.qname, tc.id); err != nil {
			t.Errorf("DeleteTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}

		}
	}
}

func TestInspectorDeleteTaskDeletesRetryTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		retry     map[string][]base.Z
		qname     string
		id        string
		wantRetry map[string][]base.Z
	}{
		{
			retry: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname: "default",
			id:    createRetryTask(z2).ID,
			wantRetry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)

		if err := inspector.DeleteTask(tc.qname, tc.id); err != nil {
			t.Errorf("DeleteTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorDeleteTaskDeletesArchivedTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(-5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(-15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(-2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		id           string
		wantArchived map[string][]base.Z
	}{
		{
			archived: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname: "default",
			id:    createArchivedTask(z2).ID,
			wantArchived: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		if err := inspector.DeleteTask(tc.qname, tc.id); err != nil {
			t.Errorf("DeleteTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorDeleteTaskError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(-5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(-15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(-2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		id           string
		wantErr      error
		wantArchived map[string][]base.Z
	}{
		{
			archived: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname:   "nonexistent",
			id:      createArchivedTask(z2).ID,
			wantErr: ErrQueueNotFound,
			wantArchived: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			qname:   "default",
			id:      uuid.NewString(),
			wantErr: ErrTaskNotFound,
			wantArchived: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		if err := inspector.DeleteTask(tc.qname, tc.id); !errors.Is(err, tc.wantErr) {
			t.Errorf("DeleteTask(%q, %q) = %v, want %v", tc.qname, tc.id, err, tc.wantErr)
			continue
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s", qname, diff)
			}
		}
	}
}

func TestInspectorRunTaskRunsScheduledTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		scheduled     map[string][]base.Z
		pending       map[string][]*base.TaskMessage
		qname         string
		id            string
		wantScheduled map[string][]base.Z
		wantPending   map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {z1, z2},
				"custom":  {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {},
			},
			qname: "default",
			id:    createScheduledTask(z2).ID,
			wantScheduled: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {m2},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllPendingQueues(t, r, tc.pending)

		if err := inspector.RunTask(tc.qname, tc.id); err != nil {
			t.Errorf("RunTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}

		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorRunTaskRunsRetryTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "custom")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		retry       map[string][]base.Z
		pending     map[string][]*base.TaskMessage
		qname       string
		id          string
		wantRetry   map[string][]base.Z
		wantPending map[string][]*base.TaskMessage
	}{
		{
			retry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    createRetryTask(z2).ID,
			wantRetry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m2},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllPendingQueues(t, r, tc.pending)

		if err := inspector.RunTask(tc.qname, tc.id); err != nil {
			t.Errorf("RunTaskBy(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorRunTaskRunsArchivedTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "critical")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "low")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(-5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(-15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(-2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		archived     map[string][]base.Z
		pending      map[string][]*base.TaskMessage
		qname        string
		id           string
		wantArchived map[string][]base.Z
		wantPending  map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qname: "critical",
			id:    createArchivedTask(z2).ID,
			wantArchived: map[string][]base.Z{
				"default":  {z1},
				"critical": {},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {m2},
				"low":      {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)
		h.SeedAllPendingQueues(t, r, tc.pending)

		if err := inspector.RunTask(tc.qname, tc.id); err != nil {
			t.Errorf("RunTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorRunTaskError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "critical")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "low")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(-5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(-15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(-2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))

	tests := []struct {
		archived     map[string][]base.Z
		pending      map[string][]*base.TaskMessage
		qname        string
		id           string
		wantErr      error
		wantArchived map[string][]base.Z
		wantPending  map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qname:   "nonexistent",
			id:      createArchivedTask(z2).ID,
			wantErr: ErrQueueNotFound,
			wantArchived: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
		},
		{
			archived: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qname:   "default",
			id:      uuid.NewString(),
			wantErr: ErrTaskNotFound,
			wantArchived: map[string][]base.Z{
				"default":  {z1},
				"critical": {z2},
				"low":      {z3},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllArchivedQueues(t, r, tc.archived)
		h.SeedAllPendingQueues(t, r, tc.pending)

		if err := inspector.RunTask(tc.qname, tc.id); !errors.Is(err, tc.wantErr) {
			t.Errorf("RunTask(%q, %q) = %v, want %v", tc.qname, tc.id, err, tc.wantErr)
			continue
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorArchiveTaskArchivesPendingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "custom")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		pending      map[string][]*base.TaskMessage
		archived     map[string][]base.Z
		qname        string
		id           string
		wantPending  map[string][]*base.TaskMessage
		wantArchived map[string][]base.Z
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1},
				"custom":  {m2, m3},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "default",
			id:    createPendingTask(m1).ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m2, m3},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
				},
				"custom": {},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1},
				"custom":  {m2, m3},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    createPendingTask(m2).ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1},
				"custom":  {m3},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: m2, Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		if err := inspector.ArchiveTask(tc.qname, tc.id); err != nil {
			t.Errorf("ArchiveTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("unexpected pending tasks in queue %q: (-want,+got)\n%s",
					qname, diff)
			}

		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want,+got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorArchiveTaskArchivesScheduledTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "custom")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		scheduled     map[string][]base.Z
		archived      map[string][]base.Z
		qname         string
		id            string
		want          string
		wantScheduled map[string][]base.Z
		wantArchived  map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    createScheduledTask(z2).ID,
			wantScheduled: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{
						Message: m2,
						Score:   now.Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllScheduledQueues(t, r, tc.scheduled)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		if err := inspector.ArchiveTask(tc.qname, tc.id); err != nil {
			t.Errorf("ArchiveTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected scheduled tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}

		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorArchiveTaskArchivesRetryTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "custom")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		retry        map[string][]base.Z
		archived     map[string][]base.Z
		qname        string
		id           string
		wantRetry    map[string][]base.Z
		wantArchived map[string][]base.Z
	}{
		{
			retry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    createRetryTask(z2).ID,
			wantRetry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z3},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{
						Message: m2,
						Score:   now.Unix(),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		if err := inspector.ArchiveTask(tc.qname, tc.id); err != nil {
			t.Errorf("ArchiveTask(%q, %q) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

func TestInspectorArchiveTaskError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessageWithQueue("task2", nil, "custom")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	now := time.Now()
	z1 := base.Z{Message: m1, Score: now.Add(5 * time.Minute).Unix()}
	z2 := base.Z{Message: m2, Score: now.Add(15 * time.Minute).Unix()}
	z3 := base.Z{Message: m3, Score: now.Add(2 * time.Minute).Unix()}

	inspector := NewInspector(getRedisConnOpt(t))
	inspector.rdb.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		retry        map[string][]base.Z
		archived     map[string][]base.Z
		qname        string
		id           string
		wantErr      error
		wantRetry    map[string][]base.Z
		wantArchived map[string][]base.Z
	}{
		{
			retry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname:   "nonexistent",
			id:      createRetryTask(z2).ID,
			wantErr: ErrQueueNotFound,
			wantRetry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname:   "custom",
			id:      uuid.NewString(),
			wantErr: ErrTaskNotFound,
			wantRetry: map[string][]base.Z{
				"default": {z1},
				"custom":  {z2, z3},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		if err := inspector.ArchiveTask(tc.qname, tc.id); !errors.Is(err, tc.wantErr) {
			t.Errorf("ArchiveTask(%q, %q) = %v, want %v", tc.qname, tc.id, err, tc.wantErr)
			continue
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected retry tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
		for qname, want := range tc.wantArchived {
			wantArchived := h.GetArchivedEntries(t, r, qname)
			if diff := cmp.Diff(want, wantArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("unexpected archived tasks in queue %q: (-want, +got)\n%s",
					qname, diff)
			}
		}
	}
}

var sortSchedulerEntry = cmp.Transformer("SortSchedulerEntry", func(in []*SchedulerEntry) []*SchedulerEntry {
	out := append([]*SchedulerEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Spec < out[j].Spec
	})
	return out
})

func TestInspectorSchedulerEntries(t *testing.T) {
	r := setup(t)
	rdbClient := rdb.NewRDB(r)
	inspector := NewInspector(getRedisConnOpt(t))

	now := time.Now().UTC()
	schedulerID := "127.0.0.1:9876:abc123"

	tests := []struct {
		data []*base.SchedulerEntry // data to seed redis
		want []*SchedulerEntry
	}{
		{
			data: []*base.SchedulerEntry{
				{
					Spec:    "* * * * *",
					Type:    "foo",
					Payload: nil,
					Opts:    nil,
					Next:    now.Add(5 * time.Hour),
					Prev:    now.Add(-2 * time.Hour),
				},
				{
					Spec:    "@every 20m",
					Type:    "bar",
					Payload: h.JSON(map[string]interface{}{"fiz": "baz"}),
					Opts:    []string{`Queue("bar")`, `MaxRetry(20)`},
					Next:    now.Add(1 * time.Minute),
					Prev:    now.Add(-19 * time.Minute),
				},
			},
			want: []*SchedulerEntry{
				{
					Spec: "* * * * *",
					Task: NewTask("foo", nil),
					Opts: nil,
					Next: now.Add(5 * time.Hour),
					Prev: now.Add(-2 * time.Hour),
				},
				{
					Spec: "@every 20m",
					Task: NewTask("bar", h.JSON(map[string]interface{}{"fiz": "baz"})),
					Opts: []Option{Queue("bar"), MaxRetry(20)},
					Next: now.Add(1 * time.Minute),
					Prev: now.Add(-19 * time.Minute),
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		err := rdbClient.WriteSchedulerEntries(schedulerID, tc.data, time.Minute)
		if err != nil {
			t.Fatalf("could not write data: %v", err)
		}
		got, err := inspector.SchedulerEntries()
		if err != nil {
			t.Errorf("SchedulerEntries() returned error: %v", err)
			continue
		}
		ignoreOpt := cmpopts.IgnoreUnexported(Task{})
		if diff := cmp.Diff(tc.want, got, sortSchedulerEntry, ignoreOpt); diff != "" {
			t.Errorf("SchedulerEntries() = %v, want %v; (-want,+got)\n%s",
				got, tc.want, diff)
		}
	}
}

func TestParseOption(t *testing.T) {
	oneHourFromNow := time.Now().Add(1 * time.Hour)
	tests := []struct {
		s        string
		wantType OptionType
		wantVal  interface{}
	}{
		{`MaxRetry(10)`, MaxRetryOpt, 10},
		{`Queue("email")`, QueueOpt, "email"},
		{`Timeout(3m)`, TimeoutOpt, 3 * time.Minute},
		{Deadline(oneHourFromNow).String(), DeadlineOpt, oneHourFromNow},
		{`Unique(1h)`, UniqueOpt, 1 * time.Hour},
		{ProcessAt(oneHourFromNow).String(), ProcessAtOpt, oneHourFromNow},
		{`ProcessIn(10m)`, ProcessInOpt, 10 * time.Minute},
		{`Retention(24h)`, RetentionOpt, 24 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.s, func(t *testing.T) {
			got, err := parseOption(tc.s)
			if err != nil {
				t.Fatalf("returned error: %v", err)
			}
			if got == nil {
				t.Fatal("returned nil")
			}
			if got.Type() != tc.wantType {
				t.Fatalf("got type %v, want type %v ", got.Type(), tc.wantType)
			}
			switch tc.wantType {
			case QueueOpt:
				gotVal, ok := got.Value().(string)
				if !ok {
					t.Fatal("returned Option with non-string value")
				}
				if gotVal != tc.wantVal.(string) {
					t.Fatalf("got value %v, want %v", gotVal, tc.wantVal)
				}
			case MaxRetryOpt:
				gotVal, ok := got.Value().(int)
				if !ok {
					t.Fatal("returned Option with non-int value")
				}
				if gotVal != tc.wantVal.(int) {
					t.Fatalf("got value %v, want %v", gotVal, tc.wantVal)
				}
			case TimeoutOpt, UniqueOpt, ProcessInOpt, RetentionOpt:
				gotVal, ok := got.Value().(time.Duration)
				if !ok {
					t.Fatal("returned Option with non duration value")
				}
				if gotVal != tc.wantVal.(time.Duration) {
					t.Fatalf("got value %v, want %v", gotVal, tc.wantVal)
				}
			case DeadlineOpt, ProcessAtOpt:
				gotVal, ok := got.Value().(time.Time)
				if !ok {
					t.Fatal("returned Option with non time value")
				}
				if cmp.Equal(gotVal, tc.wantVal.(time.Time)) {
					t.Fatalf("got value %v, want %v", gotVal, tc.wantVal)
				}
			default:
				t.Fatalf("returned Option with unexpected type: %v", got.Type())
			}
		})
	}
}

func TestInspectorGroups(t *testing.T) {
	r := setup(t)
	defer r.Close()
	inspector := NewInspector(getRedisConnOpt(t))

	m1 := h.NewTaskMessageBuilder().SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetGroup("group1").Build()
	m4 := h.NewTaskMessageBuilder().SetGroup("group2").Build()
	m5 := h.NewTaskMessageBuilder().SetQueue("custom").SetGroup("group1").Build()
	m6 := h.NewTaskMessageBuilder().SetQueue("custom").SetGroup("group1").Build()

	now := time.Now()

	fixtures := struct {
		tasks     []*h.TaskSeedData
		allGroups map[string][]string
		groups    map[string][]redis.Z
	}{
		tasks: []*h.TaskSeedData{
			{Msg: m1, State: base.TaskStateAggregating},
			{Msg: m2, State: base.TaskStateAggregating},
			{Msg: m3, State: base.TaskStateAggregating},
			{Msg: m4, State: base.TaskStateAggregating},
			{Msg: m5, State: base.TaskStateAggregating},
		},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1", "group2"},
			base.AllGroups("custom"):  {"group1"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m3.ID, Score: float64(now.Add(-30 * time.Second).Unix())},
			},
			base.GroupKey("default", "group2"): {
				{Member: m4.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group1"): {
				{Member: m5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				{Member: m6.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc  string
		qname string
		want  []*GroupInfo
	}{
		{
			desc:  "default queue groups",
			qname: "default",
			want: []*GroupInfo{
				{Group: "group1", Size: 3},
				{Group: "group2", Size: 1},
			},
		},
		{
			desc:  "custom queue groups",
			qname: "custom",
			want: []*GroupInfo{
				{Group: "group1", Size: 2},
			},
		},
	}

	var sortGroupInfosOpt = cmp.Transformer(
		"SortGroupInfos",
		func(in []*GroupInfo) []*GroupInfo {
			out := append([]*GroupInfo(nil), in...)
			sort.Slice(out, func(i, j int) bool {
				return out[i].Group < out[j].Group
			})
			return out
		})

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedTasks(t, r, fixtures.tasks)
		h.SeedRedisSets(t, r, fixtures.allGroups)
		h.SeedRedisZSets(t, r, fixtures.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := inspector.Groups(tc.qname)
			if err != nil {
				t.Fatalf("Groups returned error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got, sortGroupInfosOpt); diff != "" {
				t.Errorf("Groups = %v, want %v; (-want,+got)\n%s", got, tc.want, diff)
			}
		})
	}
}
