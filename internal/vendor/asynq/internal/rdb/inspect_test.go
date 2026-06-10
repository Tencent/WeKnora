// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package rdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/errors"
	h "github.com/hibiken/asynq/internal/testutil"
	"github.com/hibiken/asynq/internal/timeutil"
	"github.com/redis/go-redis/v9"
)

func TestAllQueues(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		queues []string
	}{
		{queues: []string{"default"}},
		{queues: []string{"custom1", "custom2"}},
		{queues: []string{"default", "custom1", "custom2"}},
		{queues: []string{}},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		for _, qname := range tc.queues {
			if err := r.client.SAdd(context.Background(), base.AllQueues, qname).Err(); err != nil {
				t.Fatalf("could not initialize all queue set: %v", err)
			}
		}
		got, err := r.AllQueues()
		if err != nil {
			t.Errorf("AllQueues() returned an error: %v", err)
			continue
		}
		if diff := cmp.Diff(tc.queues, got, h.SortStringSliceOpt); diff != "" {
			t.Errorf("AllQueues() = %v, want %v; (-want, +got)\n%s", got, tc.queues, diff)
		}
	}
}

func TestCurrentStats(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := h.NewTaskMessageBuilder().SetType("send_email").Build()
	m2 := h.NewTaskMessageBuilder().SetType("reindex").Build()
	m3 := h.NewTaskMessageBuilder().SetType("gen_thumbnail").Build()
	m4 := h.NewTaskMessageBuilder().SetType("sync").Build()
	m5 := h.NewTaskMessageBuilder().SetType("important_notification").SetQueue("critical").Build()
	m6 := h.NewTaskMessageBuilder().SetType("minor_notification").SetQueue("low").Build()
	m7 := h.NewTaskMessageBuilder().SetType("send_sms").Build()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		tasks                           []*h.TaskSeedData
		allQueues                       []string
		allGroups                       map[string][]string
		pending                         map[string][]string
		active                          map[string][]string
		scheduled                       map[string][]redis.Z
		retry                           map[string][]redis.Z
		archived                        map[string][]redis.Z
		completed                       map[string][]redis.Z
		groups                          map[string][]redis.Z
		processed                       map[string]int
		failed                          map[string]int
		processedTotal                  map[string]int
		failedTotal                     map[string]int
		paused                          []string
		oldestPendingMessageEnqueueTime map[string]time.Time
		qname                           string
		want                            *Stats
	}{
		{
			tasks: []*h.TaskSeedData{
				{Msg: m1, State: base.TaskStatePending},
				{Msg: m2, State: base.TaskStateActive},
				{Msg: m3, State: base.TaskStateScheduled},
				{Msg: m4, State: base.TaskStateScheduled},
				{Msg: m5, State: base.TaskStatePending},
				{Msg: m6, State: base.TaskStatePending},
				{Msg: m7, State: base.TaskStateAggregating},
			},
			allQueues: []string{"default", "critical", "low"},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"sms:user1"},
			},
			pending: map[string][]string{
				base.PendingKey("default"):  {m1.ID},
				base.PendingKey("critical"): {m5.ID},
				base.PendingKey("low"):      {m6.ID},
			},
			active: map[string][]string{
				base.ActiveKey("default"):  {m2.ID},
				base.ActiveKey("critical"): {},
				base.ActiveKey("low"):      {},
			},
			scheduled: map[string][]redis.Z{
				base.ScheduledKey("default"): {
					{Member: m3.ID, Score: float64(now.Add(time.Hour).Unix())},
					{Member: m4.ID, Score: float64(now.Unix())},
				},
				base.ScheduledKey("critical"): {},
				base.ScheduledKey("low"):      {},
			},
			retry: map[string][]redis.Z{
				base.RetryKey("default"):  {},
				base.RetryKey("critical"): {},
				base.RetryKey("low"):      {},
			},
			archived: map[string][]redis.Z{
				base.ArchivedKey("default"):  {},
				base.ArchivedKey("critical"): {},
				base.ArchivedKey("low"):      {},
			},
			completed: map[string][]redis.Z{
				base.CompletedKey("default"):  {},
				base.CompletedKey("critical"): {},
				base.CompletedKey("low"):      {},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "sms:user1"): {
					{Member: m7.ID, Score: float64(now.Add(-3 * time.Second).Unix())},
				},
			},
			processed: map[string]int{
				"default":  120,
				"critical": 100,
				"low":      50,
			},
			failed: map[string]int{
				"default":  2,
				"critical": 0,
				"low":      1,
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
			paused: []string{},
			qname:  "default",
			want: &Stats{
				Queue:          "default",
				Paused:         false,
				Size:           5,
				Groups:         1,
				Pending:        1,
				Active:         1,
				Scheduled:      2,
				Retry:          0,
				Archived:       0,
				Completed:      0,
				Aggregating:    1,
				Processed:      120,
				Failed:         2,
				ProcessedTotal: 11111,
				FailedTotal:    111,
				Latency:        15 * time.Second,
				Timestamp:      now,
			},
		},
		{
			tasks: []*h.TaskSeedData{
				{Msg: m1, State: base.TaskStatePending},
				{Msg: m2, State: base.TaskStateActive},
				{Msg: m3, State: base.TaskStateScheduled},
				{Msg: m4, State: base.TaskStateScheduled},
				{Msg: m6, State: base.TaskStatePending},
			},
			allQueues: []string{"default", "critical", "low"},
			pending: map[string][]string{
				base.PendingKey("default"):  {m1.ID},
				base.PendingKey("critical"): {},
				base.PendingKey("low"):      {m6.ID},
			},
			active: map[string][]string{
				base.ActiveKey("default"):  {m2.ID},
				base.ActiveKey("critical"): {},
				base.ActiveKey("low"):      {},
			},
			scheduled: map[string][]redis.Z{
				base.ScheduledKey("default"): {
					{Member: m3.ID, Score: float64(now.Add(time.Hour).Unix())},
					{Member: m4.ID, Score: float64(now.Unix())},
				},
				base.ScheduledKey("critical"): {},
				base.ScheduledKey("low"):      {},
			},
			retry: map[string][]redis.Z{
				base.RetryKey("default"):  {},
				base.RetryKey("critical"): {},
				base.RetryKey("low"):      {},
			},
			archived: map[string][]redis.Z{
				base.ArchivedKey("default"):  {},
				base.ArchivedKey("critical"): {},
				base.ArchivedKey("low"):      {},
			},
			completed: map[string][]redis.Z{
				base.CompletedKey("default"):  {},
				base.CompletedKey("critical"): {},
				base.CompletedKey("low"):      {},
			},
			processed: map[string]int{
				"default":  120,
				"critical": 100,
				"low":      50,
			},
			failed: map[string]int{
				"default":  2,
				"critical": 0,
				"low":      1,
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
				"critical": {}, // zero value since there's no pending task in this queue
				"low":      now.Add(-30 * time.Second),
			},
			paused: []string{"critical", "low"},
			qname:  "critical",
			want: &Stats{
				Queue:          "critical",
				Paused:         true,
				Size:           0,
				Groups:         0,
				Pending:        0,
				Active:         0,
				Scheduled:      0,
				Retry:          0,
				Archived:       0,
				Completed:      0,
				Aggregating:    0,
				Processed:      100,
				Failed:         0,
				ProcessedTotal: 22222,
				FailedTotal:    222,
				Latency:        0,
				Timestamp:      now,
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		for _, qname := range tc.paused {
			if err := r.Pause(qname); err != nil {
				t.Fatal(err)
			}
		}
		h.SeedRedisSet(t, r.client, base.AllQueues, tc.allQueues)
		h.SeedRedisSets(t, r.client, tc.allGroups)
		h.SeedTasks(t, r.client, tc.tasks)
		h.SeedRedisLists(t, r.client, tc.pending)
		h.SeedRedisLists(t, r.client, tc.active)
		h.SeedRedisZSets(t, r.client, tc.scheduled)
		h.SeedRedisZSets(t, r.client, tc.retry)
		h.SeedRedisZSets(t, r.client, tc.archived)
		h.SeedRedisZSets(t, r.client, tc.completed)
		h.SeedRedisZSets(t, r.client, tc.groups)
		ctx := context.Background()
		for qname, n := range tc.processed {
			r.client.Set(ctx, base.ProcessedKey(qname, now), n, 0)
		}
		for qname, n := range tc.failed {
			r.client.Set(ctx, base.FailedKey(qname, now), n, 0)
		}
		for qname, n := range tc.processedTotal {
			r.client.Set(ctx, base.ProcessedTotalKey(qname), n, 0)
		}
		for qname, n := range tc.failedTotal {
			r.client.Set(ctx, base.FailedTotalKey(qname), n, 0)
		}
		for qname, enqueueTime := range tc.oldestPendingMessageEnqueueTime {
			if enqueueTime.IsZero() {
				continue
			}
			oldestPendingMessageID := r.client.LRange(ctx, base.PendingKey(qname), -1, -1).Val()[0] // get the right most msg in the list
			r.client.HSet(ctx, base.TaskKey(qname, oldestPendingMessageID), "pending_since", enqueueTime.UnixNano())
		}

		got, err := r.CurrentStats(tc.qname)
		if err != nil {
			t.Errorf("r.CurrentStats(%q) = %v, %v, want %v, nil", tc.qname, got, err, tc.want)
			continue
		}

		ignoreMemUsg := cmpopts.IgnoreFields(Stats{}, "MemoryUsage")
		if diff := cmp.Diff(tc.want, got, timeCmpOpt, ignoreMemUsg); diff != "" {
			t.Errorf("r.CurrentStats(%q) = %v, %v, want %v, nil; (-want, +got)\n%s", tc.qname, got, err, tc.want, diff)
			continue
		}
	}
}

func TestCurrentStatsWithNonExistentQueue(t *testing.T) {
	r := setup(t)
	defer r.Close()

	qname := "non-existent"
	got, err := r.CurrentStats(qname)
	if !errors.IsQueueNotFound(err) {
		t.Fatalf("r.CurrentStats(%q) = %v, %v, want nil, %v", qname, got, err, &errors.QueueNotFoundError{Queue: qname})
	}
}

func TestHistoricalStats(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now().UTC()

	tests := []struct {
		qname string // queue of interest
		n     int    // number of days
	}{
		{"default", 90},
		{"custom", 7},
		{"default", 1},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		r.client.SAdd(context.Background(), base.AllQueues, tc.qname)
		// populate last n days data
		for i := 0; i < tc.n; i++ {
			ts := now.Add(-time.Duration(i) * 24 * time.Hour)
			processedKey := base.ProcessedKey(tc.qname, ts)
			failedKey := base.FailedKey(tc.qname, ts)
			r.client.Set(context.Background(), processedKey, (i+1)*1000, 0)
			r.client.Set(context.Background(), failedKey, (i+1)*10, 0)
		}

		got, err := r.HistoricalStats(tc.qname, tc.n)
		if err != nil {
			t.Errorf("RDB.HistoricalStats(%q, %d) returned error: %v", tc.qname, tc.n, err)
			continue
		}

		if len(got) != tc.n {
			t.Errorf("RDB.HistorycalStats(%q, %d) returned %d daily stats, want %d", tc.qname, tc.n, len(got), tc.n)
			continue
		}

		for i := 0; i < tc.n; i++ {
			want := &DailyStats{
				Queue:     tc.qname,
				Processed: (i + 1) * 1000,
				Failed:    (i + 1) * 10,
				Time:      now.Add(-time.Duration(i) * 24 * time.Hour),
			}
			// Allow 2 seconds difference in timestamp.
			cmpOpt := cmpopts.EquateApproxTime(2 * time.Second)
			if diff := cmp.Diff(want, got[i], cmpOpt); diff != "" {
				t.Errorf("RDB.HistoricalStats for the last %d days; got %+v, want %+v; (-want,+got):\n%s", i, got[i], want, diff)
			}
		}
	}
}

func TestRedisInfo(t *testing.T) {
	r := setup(t)
	defer r.Close()

	info, err := r.RedisInfo()
	if err != nil {
		t.Fatalf("RDB.RedisInfo() returned error: %v", err)
	}

	wantKeys := []string{
		"redis_version",
		"uptime_in_days",
		"connected_clients",
		"used_memory_human",
		"used_memory_peak_human",
		"used_memory_peak_perc",
	}

	for _, key := range wantKeys {
		if _, ok := info[key]; !ok {
			t.Errorf("RDB.RedisInfo() = %v is missing entry for %q", info, key)
		}
	}
}

func TestGroupStats(t *testing.T) {
	r := setup(t)
	defer r.Close()

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
		want  []*GroupStat
	}{
		{
			desc:  "default queue groups",
			qname: "default",
			want: []*GroupStat{
				{Group: "group1", Size: 3},
				{Group: "group2", Size: 1},
			},
		},
		{
			desc:  "custom queue groups",
			qname: "custom",
			want: []*GroupStat{
				{Group: "group1", Size: 2},
			},
		},
	}

	sortGroupStatsOpt := cmp.Transformer(
		"SortGroupStats",
		func(in []*GroupStat) []*GroupStat {
			out := append([]*GroupStat(nil), in...)
			sort.Slice(out, func(i, j int) bool {
				return out[i].Group < out[j].Group
			})
			return out
		})

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fixtures.tasks)
		h.SeedRedisSets(t, r.client, fixtures.allGroups)
		h.SeedRedisZSets(t, r.client, fixtures.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := r.GroupStats(tc.qname)
			if err != nil {
				t.Fatalf("GroupStats returned error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got, sortGroupStatsOpt); diff != "" {
				t.Errorf("GroupStats = %v, want %v; (-want,+got)\n%s", got, tc.want, diff)
			}
		})
	}
}

func TestGetTaskInfo(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	fiveMinsFromNow := now.Add(5 * time.Minute)
	oneHourFromNow := now.Add(1 * time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)

	m1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	m2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	m5 := h.NewTaskMessageWithQueue("task5", nil, "custom")
	m6 := h.NewTaskMessageWithQueue("task5", nil, "custom")
	m6.CompletedAt = twoHoursAgo.Unix()
	m6.Retention = int64((24 * time.Hour).Seconds())

	fixtures := struct {
		active    map[string][]*base.TaskMessage
		pending   map[string][]*base.TaskMessage
		scheduled map[string][]base.Z
		retry     map[string][]base.Z
		archived  map[string][]base.Z
		completed map[string][]base.Z
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
		completed: map[string][]base.Z{
			"default": {},
			"custom":  {{Message: m6, Score: m6.CompletedAt + m6.Retention}},
		},
	}

	h.SeedAllActiveQueues(t, r.client, fixtures.active)
	h.SeedAllPendingQueues(t, r.client, fixtures.pending)
	h.SeedAllScheduledQueues(t, r.client, fixtures.scheduled)
	h.SeedAllRetryQueues(t, r.client, fixtures.retry)
	h.SeedAllArchivedQueues(t, r.client, fixtures.archived)
	h.SeedAllCompletedQueues(t, r.client, fixtures.completed)
	// Write result data for the completed task.
	if err := r.client.HSet(context.Background(), base.TaskKey(m6.Queue, m6.ID), "result", "foobar").Err(); err != nil {
		t.Fatalf("Failed to write result data under task key: %v", err)
	}

	tests := []struct {
		qname string
		id    string
		want  *base.TaskInfo
	}{
		{
			qname: "default",
			id:    m1.ID,
			want: &base.TaskInfo{
				Message:       m1,
				State:         base.TaskStateActive,
				NextProcessAt: time.Time{}, // zero value for N/A
				Result:        nil,
			},
		},
		{
			qname: "default",
			id:    m2.ID,
			want: &base.TaskInfo{
				Message:       m2,
				State:         base.TaskStateScheduled,
				NextProcessAt: fiveMinsFromNow,
				Result:        nil,
			},
		},
		{
			qname: "custom",
			id:    m3.ID,
			want: &base.TaskInfo{
				Message:       m3,
				State:         base.TaskStateRetry,
				NextProcessAt: oneHourFromNow,
				Result:        nil,
			},
		},
		{
			qname: "custom",
			id:    m4.ID,
			want: &base.TaskInfo{
				Message:       m4,
				State:         base.TaskStateArchived,
				NextProcessAt: time.Time{}, // zero value for N/A
				Result:        nil,
			},
		},
		{
			qname: "custom",
			id:    m5.ID,
			want: &base.TaskInfo{
				Message:       m5,
				State:         base.TaskStatePending,
				NextProcessAt: now,
				Result:        nil,
			},
		},
		{
			qname: "custom",
			id:    m6.ID,
			want: &base.TaskInfo{
				Message:       m6,
				State:         base.TaskStateCompleted,
				NextProcessAt: time.Time{}, // zero value for N/A
				Result:        []byte("foobar"),
			},
		},
	}

	for _, tc := range tests {
		got, err := r.GetTaskInfo(tc.qname, tc.id)
		if err != nil {
			t.Errorf("GetTaskInfo(%q, %v) returned error: %v", tc.qname, tc.id, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmpopts.EquateApproxTime(2*time.Second)); diff != "" {
			t.Errorf("GetTaskInfo(%q, %v) = %v, want %v; (-want,+got)\n%s",
				tc.qname, tc.id, got, tc.want, diff)
		}
	}
}

func TestGetTaskInfoError(t *testing.T) {
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

	h.SeedAllActiveQueues(t, r.client, fixtures.active)
	h.SeedAllPendingQueues(t, r.client, fixtures.pending)
	h.SeedAllScheduledQueues(t, r.client, fixtures.scheduled)
	h.SeedAllRetryQueues(t, r.client, fixtures.retry)
	h.SeedAllArchivedQueues(t, r.client, fixtures.archived)

	tests := []struct {
		qname string
		id    string
		match func(err error) bool
	}{
		{
			qname: "nonexistent",
			id:    m1.ID,
			match: errors.IsQueueNotFound,
		},
		{
			qname: "default",
			id:    uuid.NewString(),
			match: errors.IsTaskNotFound,
		},
	}

	for _, tc := range tests {
		info, err := r.GetTaskInfo(tc.qname, tc.id)
		if info != nil {
			t.Errorf("GetTaskInfo(%q, %v) returned info: %v", tc.qname, tc.id, info)
		}
		if !tc.match(err) {
			t.Errorf("GetTaskInfo(%q, %v) returned unexpected error: %v", tc.qname, tc.id, err)
		}
	}
}

func TestListPending(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := h.NewTaskMessage("send_email", h.JSON(map[string]interface{}{"subject": "hello"}))
	m2 := h.NewTaskMessage("reindex", nil)
	m3 := h.NewTaskMessageWithQueue("important_notification", nil, "critical")
	m4 := h.NewTaskMessageWithQueue("minor_notification", nil, "low")

	tests := []struct {
		pending map[string][]*base.TaskMessage
		qname   string
		want    []*base.TaskInfo
	}{
		{
			pending: map[string][]*base.TaskMessage{
				base.DefaultQueueName: {m1, m2},
			},
			qname: base.DefaultQueueName,
			want: []*base.TaskInfo{
				{Message: m1, State: base.TaskStatePending, NextProcessAt: time.Now(), Result: nil},
				{Message: m2, State: base.TaskStatePending, NextProcessAt: time.Now(), Result: nil},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				base.DefaultQueueName: nil,
			},
			qname: base.DefaultQueueName,
			want:  []*base.TaskInfo(nil),
		},
		{
			pending: map[string][]*base.TaskMessage{
				base.DefaultQueueName: {m1, m2},
				"critical":            {m3},
				"low":                 {m4},
			},
			qname: base.DefaultQueueName,
			want: []*base.TaskInfo{
				{Message: m1, State: base.TaskStatePending, NextProcessAt: time.Now(), Result: nil},
				{Message: m2, State: base.TaskStatePending, NextProcessAt: time.Now(), Result: nil},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				base.DefaultQueueName: {m1, m2},
				"critical":            {m3},
				"low":                 {m4},
			},
			qname: "critical",
			want: []*base.TaskInfo{
				{Message: m3, State: base.TaskStatePending, NextProcessAt: time.Now(), Result: nil},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllPendingQueues(t, r.client, tc.pending)

		got, err := r.ListPending(tc.qname, Pagination{Size: 20, Page: 0})
		op := fmt.Sprintf("r.ListPending(%q, Pagination{Size: 20, Page: 0})", tc.qname)
		if err != nil {
			t.Errorf("%s = %v, %v, want %v, nil", op, got, err, tc.want)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmpopts.EquateApproxTime(2*time.Second)); diff != "" {
			t.Errorf("%s = %v, %v, want %v, nil; (-want, +got)\n%s", op, got, err, tc.want, diff)
			continue
		}
	}
}

func TestListPendingPagination(t *testing.T) {
	r := setup(t)
	defer r.Close()
	var msgs []*base.TaskMessage
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessage(fmt.Sprintf("task %d", i), nil)
		msgs = append(msgs, msg)
	}
	// create 100 tasks in default queue
	h.SeedPendingQueue(t, r.client, msgs, "default")

	msgs = []*base.TaskMessage(nil) // empty list
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessageWithQueue(fmt.Sprintf("custom %d", i), nil, "custom")
		msgs = append(msgs, msg)
	}
	// create 100 tasks in custom queue
	h.SeedPendingQueue(t, r.client, msgs, "custom")

	tests := []struct {
		desc      string
		qname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{"first page", "default", 0, 20, 20, "task 0", "task 19"},
		{"second page", "default", 1, 20, 20, "task 20", "task 39"},
		{"different page size", "default", 2, 30, 30, "task 60", "task 89"},
		{"last page", "default", 3, 30, 10, "task 90", "task 99"},
		{"out of range", "default", 4, 30, 0, "", ""},
		{"second page with custom queue", "custom", 1, 20, 20, "custom 20", "custom 39"},
	}

	for _, tc := range tests {
		got, err := r.ListPending(tc.qname, Pagination{Size: tc.size, Page: tc.page})
		op := fmt.Sprintf("r.ListPending(%q, Pagination{Size: %d, Page: %d})", tc.qname, tc.size, tc.page)
		if err != nil {
			t.Errorf("%s; %s returned error %v", tc.desc, op, err)
			continue
		}

		if len(got) != tc.wantSize {
			t.Errorf("%s; %s returned a list of size %d, want %d", tc.desc, op, len(got), tc.wantSize)
			continue
		}

		if tc.wantSize == 0 {
			continue
		}

		first := got[0].Message
		if first.Type != tc.wantFirst {
			t.Errorf("%s; %s returned a list with first message %q, want %q",
				tc.desc, op, first.Type, tc.wantFirst)
		}

		last := got[len(got)-1].Message
		if last.Type != tc.wantLast {
			t.Errorf("%s; %s returned a list with the last message %q, want %q",
				tc.desc, op, last.Type, tc.wantLast)
		}
	}
}

func TestListActive(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task2", nil, "critical")
	m4 := h.NewTaskMessageWithQueue("task2", nil, "low")

	tests := []struct {
		inProgress map[string][]*base.TaskMessage
		qname      string
		want       []*base.TaskInfo
	}{
		{
			inProgress: map[string][]*base.TaskMessage{
				"default":  {m1, m2},
				"critical": {m3},
				"low":      {m4},
			},
			qname: "default",
			want: []*base.TaskInfo{
				{Message: m1, State: base.TaskStateActive, NextProcessAt: time.Time{}, Result: nil},
				{Message: m2, State: base.TaskStateActive, NextProcessAt: time.Time{}, Result: nil},
			},
		},
		{
			inProgress: map[string][]*base.TaskMessage{
				"default": {},
			},
			qname: "default",
			want:  []*base.TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllActiveQueues(t, r.client, tc.inProgress)

		got, err := r.ListActive(tc.qname, Pagination{Size: 20, Page: 0})
		op := fmt.Sprintf("r.ListActive(%q, Pagination{Size: 20, Page: 0})", tc.qname)
		if err != nil {
			t.Errorf("%s = %v, %v, want %v, nil", op, got, err, tc.inProgress)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmpopts.EquateApproxTime(1*time.Second)); diff != "" {
			t.Errorf("%s = %v, %v, want %v, nil; (-want, +got)\n%s", op, got, err, tc.want, diff)
			continue
		}
	}
}

func TestListActivePagination(t *testing.T) {
	r := setup(t)
	defer r.Close()
	var msgs []*base.TaskMessage
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessage(fmt.Sprintf("task %d", i), nil)
		msgs = append(msgs, msg)
	}
	h.SeedActiveQueue(t, r.client, msgs, "default")

	tests := []struct {
		desc      string
		qname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{"first page", "default", 0, 20, 20, "task 0", "task 19"},
		{"second page", "default", 1, 20, 20, "task 20", "task 39"},
		{"different page size", "default", 2, 30, 30, "task 60", "task 89"},
		{"last page", "default", 3, 30, 10, "task 90", "task 99"},
		{"out of range", "default", 4, 30, 0, "", ""},
	}

	for _, tc := range tests {
		got, err := r.ListActive(tc.qname, Pagination{Size: tc.size, Page: tc.page})
		op := fmt.Sprintf("r.ListActive(%q, Pagination{Size: %d, Page: %d})", tc.qname, tc.size, tc.page)
		if err != nil {
			t.Errorf("%s; %s returned error %v", tc.desc, op, err)
			continue
		}

		if len(got) != tc.wantSize {
			t.Errorf("%s; %s returned list of size %d, want %d", tc.desc, op, len(got), tc.wantSize)
			continue
		}

		if tc.wantSize == 0 {
			continue
		}

		first := got[0].Message
		if first.Type != tc.wantFirst {
			t.Errorf("%s; %s returned a list with first message %q, want %q",
				tc.desc, op, first.Type, tc.wantFirst)
		}

		last := got[len(got)-1].Message
		if last.Type != tc.wantLast {
			t.Errorf("%s; %s returned a list with the last message %q, want %q",
				tc.desc, op, last.Type, tc.wantLast)
		}
	}
}

func TestListScheduled(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	p1 := time.Now().Add(30 * time.Minute)
	p2 := time.Now().Add(24 * time.Hour)
	p3 := time.Now().Add(5 * time.Minute)
	p4 := time.Now().Add(2 * time.Minute)

	tests := []struct {
		scheduled map[string][]base.Z
		qname     string
		want      []*base.TaskInfo
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: p1.Unix()},
					{Message: m2, Score: p2.Unix()},
					{Message: m3, Score: p3.Unix()},
				},
				"custom": {
					{Message: m4, Score: p4.Unix()},
				},
			},
			qname: "default",
			// should be sorted by score in ascending order
			want: []*base.TaskInfo{
				{Message: m3, NextProcessAt: p3, State: base.TaskStateScheduled, Result: nil},
				{Message: m1, NextProcessAt: p1, State: base.TaskStateScheduled, Result: nil},
				{Message: m2, NextProcessAt: p2, State: base.TaskStateScheduled, Result: nil},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: p1.Unix()},
					{Message: m2, Score: p2.Unix()},
					{Message: m3, Score: p3.Unix()},
				},
				"custom": {
					{Message: m4, Score: p4.Unix()},
				},
			},
			qname: "custom",
			want: []*base.TaskInfo{
				{Message: m4, NextProcessAt: p4, State: base.TaskStateScheduled, Result: nil},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*base.TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		got, err := r.ListScheduled(tc.qname, Pagination{Size: 20, Page: 0})
		op := fmt.Sprintf("r.ListScheduled(%q, Pagination{Size: 20, Page: 0})", tc.qname)
		if err != nil {
			t.Errorf("%s = %v, %v, want %v, nil", op, got, err, tc.want)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmpopts.EquateApproxTime(1*time.Second)); diff != "" {
			t.Errorf("%s = %v, %v, want %v, nil; (-want, +got)\n%s", op, got, err, tc.want, diff)
			continue
		}
	}
}

func TestListScheduledPagination(t *testing.T) {
	r := setup(t)
	defer r.Close()
	// create 100 tasks with an increasing number of wait time.
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessage(fmt.Sprintf("task %d", i), nil)
		if err := r.Schedule(context.Background(), msg, time.Now().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		desc      string
		qname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{"first page", "default", 0, 20, 20, "task 0", "task 19"},
		{"second page", "default", 1, 20, 20, "task 20", "task 39"},
		{"different page size", "default", 2, 30, 30, "task 60", "task 89"},
		{"last page", "default", 3, 30, 10, "task 90", "task 99"},
		{"out of range", "default", 4, 30, 0, "", ""},
	}

	for _, tc := range tests {
		got, err := r.ListScheduled(tc.qname, Pagination{Size: tc.size, Page: tc.page})
		op := fmt.Sprintf("r.ListScheduled(%q, Pagination{Size: %d, Page: %d})", tc.qname, tc.size, tc.page)
		if err != nil {
			t.Errorf("%s; %s returned error %v", tc.desc, op, err)
			continue
		}

		if len(got) != tc.wantSize {
			t.Errorf("%s; %s returned list of size %d, want %d", tc.desc, op, len(got), tc.wantSize)
			continue
		}

		if tc.wantSize == 0 {
			continue
		}

		first := got[0].Message
		if first.Type != tc.wantFirst {
			t.Errorf("%s; %s returned a list with first message %q, want %q",
				tc.desc, op, first.Type, tc.wantFirst)
		}

		last := got[len(got)-1].Message
		if last.Type != tc.wantLast {
			t.Errorf("%s; %s returned a list with the last message %q, want %q",
				tc.desc, op, last.Type, tc.wantLast)
		}
	}
}

func TestListRetry(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "task1",
		Queue:    "default",
		Payload:  nil,
		ErrorMsg: "some error occurred",
		Retry:    25,
		Retried:  10,
	}
	m2 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "task2",
		Queue:    "default",
		Payload:  nil,
		ErrorMsg: "some error occurred",
		Retry:    25,
		Retried:  2,
	}
	m3 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "task3",
		Queue:    "custom",
		Payload:  nil,
		ErrorMsg: "some error occurred",
		Retry:    25,
		Retried:  3,
	}
	p1 := time.Now().Add(5 * time.Minute)
	p2 := time.Now().Add(24 * time.Hour)
	p3 := time.Now().Add(24 * time.Hour)

	tests := []struct {
		retry map[string][]base.Z
		qname string
		want  []*base.TaskInfo
	}{
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: p1.Unix()},
					{Message: m2, Score: p2.Unix()},
				},
				"custom": {
					{Message: m3, Score: p3.Unix()},
				},
			},
			qname: "default",
			want: []*base.TaskInfo{
				{Message: m1, NextProcessAt: p1, State: base.TaskStateRetry, Result: nil},
				{Message: m2, NextProcessAt: p2, State: base.TaskStateRetry, Result: nil},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: p1.Unix()},
					{Message: m2, Score: p2.Unix()},
				},
				"custom": {
					{Message: m3, Score: p3.Unix()},
				},
			},
			qname: "custom",
			want: []*base.TaskInfo{
				{Message: m3, NextProcessAt: p3, State: base.TaskStateRetry, Result: nil},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*base.TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		got, err := r.ListRetry(tc.qname, Pagination{Size: 20, Page: 0})
		op := fmt.Sprintf("r.ListRetry(%q, Pagination{Size: 20, Page: 0})", tc.qname)
		if err != nil {
			t.Errorf("%s = %v, %v, want %v, nil", op, got, err, tc.want)
			continue
		}
		if diff := cmp.Diff(tc.want, got, cmpopts.EquateApproxTime(1*time.Second)); diff != "" {
			t.Errorf("%s = %v, %v, want %v, nil; (-want, +got)\n%s",
				op, got, err, tc.want, diff)
			continue
		}
	}
}

func TestListRetryPagination(t *testing.T) {
	r := setup(t)
	defer r.Close()
	// create 100 tasks with an increasing number of wait time.
	now := time.Now()
	var seed []base.Z
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessage(fmt.Sprintf("task %d", i), nil)
		processAt := now.Add(time.Duration(i) * time.Second)
		seed = append(seed, base.Z{Message: msg, Score: processAt.Unix()})
	}
	h.SeedRetryQueue(t, r.client, seed, "default")

	tests := []struct {
		desc      string
		qname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{"first page", "default", 0, 20, 20, "task 0", "task 19"},
		{"second page", "default", 1, 20, 20, "task 20", "task 39"},
		{"different page size", "default", 2, 30, 30, "task 60", "task 89"},
		{"last page", "default", 3, 30, 10, "task 90", "task 99"},
		{"out of range", "default", 4, 30, 0, "", ""},
	}

	for _, tc := range tests {
		got, err := r.ListRetry(tc.qname, Pagination{Size: tc.size, Page: tc.page})
		op := fmt.Sprintf("r.ListRetry(%q, Pagination{Size: %d, Page: %d})",
			tc.qname, tc.size, tc.page)
		if err != nil {
			t.Errorf("%s; %s returned error %v", tc.desc, op, err)
			continue
		}

		if len(got) != tc.wantSize {
			t.Errorf("%s; %s returned list of size %d, want %d",
				tc.desc, op, len(got), tc.wantSize)
			continue
		}

		if tc.wantSize == 0 {
			continue
		}

		first := got[0].Message
		if first.Type != tc.wantFirst {
			t.Errorf("%s; %s returned a list with first message %q, want %q",
				tc.desc, op, first.Type, tc.wantFirst)
		}

		last := got[len(got)-1].Message
		if last.Type != tc.wantLast {
			t.Errorf("%s; %s returned a list with the last message %q, want %q",
				tc.desc, op, last.Type, tc.wantLast)
		}
	}
}

func TestListArchived(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "task1",
		Queue:    "default",
		Payload:  nil,
		ErrorMsg: "some error occurred",
	}
	m2 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "task2",
		Queue:    "default",
		Payload:  nil,
		ErrorMsg: "some error occurred",
	}
	m3 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "task3",
		Queue:    "custom",
		Payload:  nil,
		ErrorMsg: "some error occurred",
	}
	f1 := time.Now().Add(-5 * time.Minute)
	f2 := time.Now().Add(-24 * time.Hour)
	f3 := time.Now().Add(-4 * time.Hour)

	tests := []struct {
		archived map[string][]base.Z
		qname    string
		want     []*base.TaskInfo
	}{
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: f1.Unix()},
					{Message: m2, Score: f2.Unix()},
				},
				"custom": {
					{Message: m3, Score: f3.Unix()},
				},
			},
			qname: "default",
			want: []*base.TaskInfo{
				{Message: m2, NextProcessAt: time.Time{}, State: base.TaskStateArchived, Result: nil}, // FIXME: shouldn't be sorted in the other order?
				{Message: m1, NextProcessAt: time.Time{}, State: base.TaskStateArchived, Result: nil},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: f1.Unix()},
					{Message: m2, Score: f2.Unix()},
				},
				"custom": {
					{Message: m3, Score: f3.Unix()},
				},
			},
			qname: "custom",
			want: []*base.TaskInfo{
				{Message: m3, NextProcessAt: time.Time{}, State: base.TaskStateArchived, Result: nil},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  []*base.TaskInfo(nil),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.ListArchived(tc.qname, Pagination{Size: 20, Page: 0})
		op := fmt.Sprintf("r.ListArchived(%q, Pagination{Size: 20, Page: 0})", tc.qname)
		if err != nil {
			t.Errorf("%s = %v, %v, want %v, nil", op, got, err, tc.want)
			continue
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("%s = %v, %v, want %v, nil; (-want, +got)\n%s",
				op, got, err, tc.want, diff)
			continue
		}
	}
}

func TestListArchivedPagination(t *testing.T) {
	r := setup(t)
	defer r.Close()
	var entries []base.Z
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessage(fmt.Sprintf("task %d", i), nil)
		entries = append(entries, base.Z{Message: msg, Score: int64(i)})
	}
	h.SeedArchivedQueue(t, r.client, entries, "default")

	tests := []struct {
		desc      string
		qname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{"first page", "default", 0, 20, 20, "task 0", "task 19"},
		{"second page", "default", 1, 20, 20, "task 20", "task 39"},
		{"different page size", "default", 2, 30, 30, "task 60", "task 89"},
		{"last page", "default", 3, 30, 10, "task 90", "task 99"},
		{"out of range", "default", 4, 30, 0, "", ""},
	}

	for _, tc := range tests {
		got, err := r.ListArchived(tc.qname, Pagination{Size: tc.size, Page: tc.page})
		op := fmt.Sprintf("r.ListArchived(Pagination{Size: %d, Page: %d})",
			tc.size, tc.page)
		if err != nil {
			t.Errorf("%s; %s returned error %v", tc.desc, op, err)
			continue
		}

		if len(got) != tc.wantSize {
			t.Errorf("%s; %s returned list of size %d, want %d",
				tc.desc, op, len(got), tc.wantSize)
			continue
		}

		if tc.wantSize == 0 {
			continue
		}

		first := got[0].Message
		if first.Type != tc.wantFirst {
			t.Errorf("%s; %s returned a list with first message %q, want %q",
				tc.desc, op, first.Type, tc.wantFirst)
		}

		last := got[len(got)-1].Message
		if last.Type != tc.wantLast {
			t.Errorf("%s; %s returned a list with the last message %q, want %q",
				tc.desc, op, last.Type, tc.wantLast)
		}
	}
}

func TestListCompleted(t *testing.T) {
	r := setup(t)
	defer r.Close()
	msg1 := &base.TaskMessage{
		ID:          uuid.NewString(),
		Type:        "foo",
		Queue:       "default",
		CompletedAt: time.Now().Add(-2 * time.Hour).Unix(),
	}
	msg2 := &base.TaskMessage{
		ID:          uuid.NewString(),
		Type:        "foo",
		Queue:       "default",
		CompletedAt: time.Now().Add(-5 * time.Hour).Unix(),
	}
	msg3 := &base.TaskMessage{
		ID:          uuid.NewString(),
		Type:        "foo",
		Queue:       "custom",
		CompletedAt: time.Now().Add(-5 * time.Hour).Unix(),
	}
	expireAt1 := time.Now().Add(3 * time.Hour)
	expireAt2 := time.Now().Add(4 * time.Hour)
	expireAt3 := time.Now().Add(5 * time.Hour)

	tests := []struct {
		completed map[string][]base.Z
		qname     string
		want      []*base.TaskInfo
	}{
		{
			completed: map[string][]base.Z{
				"default": {
					{Message: msg1, Score: expireAt1.Unix()},
					{Message: msg2, Score: expireAt2.Unix()},
				},
				"custom": {
					{Message: msg3, Score: expireAt3.Unix()},
				},
			},
			qname: "default",
			want: []*base.TaskInfo{
				{Message: msg1, NextProcessAt: time.Time{}, State: base.TaskStateCompleted, Result: nil},
				{Message: msg2, NextProcessAt: time.Time{}, State: base.TaskStateCompleted, Result: nil},
			},
		},
		{
			completed: map[string][]base.Z{
				"default": {
					{Message: msg1, Score: expireAt1.Unix()},
					{Message: msg2, Score: expireAt2.Unix()},
				},
				"custom": {
					{Message: msg3, Score: expireAt3.Unix()},
				},
			},
			qname: "custom",
			want: []*base.TaskInfo{
				{Message: msg3, NextProcessAt: time.Time{}, State: base.TaskStateCompleted, Result: nil},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllCompletedQueues(t, r.client, tc.completed)

		got, err := r.ListCompleted(tc.qname, Pagination{Size: 20, Page: 0})
		op := fmt.Sprintf("r.ListCompleted(%q, Pagination{Size: 20, Page: 0})", tc.qname)
		if err != nil {
			t.Errorf("%s = %v, %v, want %v, nil", op, got, err, tc.want)
			continue
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("%s = %v, %v, want %v, nil; (-want, +got)\n%s",
				op, got, err, tc.want, diff)
			continue
		}
	}
}

func TestListCompletedPagination(t *testing.T) {
	r := setup(t)
	defer r.Close()
	var entries []base.Z
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessage(fmt.Sprintf("task %d", i), nil)
		entries = append(entries, base.Z{Message: msg, Score: int64(i)})
	}
	h.SeedCompletedQueue(t, r.client, entries, "default")

	tests := []struct {
		desc      string
		qname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{"first page", "default", 0, 20, 20, "task 0", "task 19"},
		{"second page", "default", 1, 20, 20, "task 20", "task 39"},
		{"different page size", "default", 2, 30, 30, "task 60", "task 89"},
		{"last page", "default", 3, 30, 10, "task 90", "task 99"},
		{"out of range", "default", 4, 30, 0, "", ""},
	}

	for _, tc := range tests {
		got, err := r.ListCompleted(tc.qname, Pagination{Size: tc.size, Page: tc.page})
		op := fmt.Sprintf("r.ListCompleted(Pagination{Size: %d, Page: %d})",
			tc.size, tc.page)
		if err != nil {
			t.Errorf("%s; %s returned error %v", tc.desc, op, err)
			continue
		}

		if len(got) != tc.wantSize {
			t.Errorf("%s; %s returned list of size %d, want %d",
				tc.desc, op, len(got), tc.wantSize)
			continue
		}

		if tc.wantSize == 0 {
			continue
		}

		first := got[0].Message
		if first.Type != tc.wantFirst {
			t.Errorf("%s; %s returned a list with first message %q, want %q",
				tc.desc, op, first.Type, tc.wantFirst)
		}

		last := got[len(got)-1].Message
		if last.Type != tc.wantLast {
			t.Errorf("%s; %s returned a list with the last message %q, want %q",
				tc.desc, op, last.Type, tc.wantLast)
		}
	}
}

func TestListAggregating(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	m1 := h.NewTaskMessageBuilder().SetType("task1").SetQueue("default").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetType("task2").SetQueue("default").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetType("task3").SetQueue("default").SetGroup("group2").Build()
	m4 := h.NewTaskMessageBuilder().SetType("task4").SetQueue("custom").SetGroup("group3").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1", "group2"},
			base.AllGroups("custom"):  {"group3"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-30 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
			base.GroupKey("default", "group2"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group3"): {
				{Member: m4.ID, Score: float64(now.Add(-40 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc  string
		qname string
		gname string
		want  []*base.TaskInfo
	}{
		{
			desc:  "with group1 in default queue",
			qname: "default",
			gname: "group1",
			want: []*base.TaskInfo{
				{Message: m1, State: base.TaskStateAggregating, NextProcessAt: time.Time{}, Result: nil},
				{Message: m2, State: base.TaskStateAggregating, NextProcessAt: time.Time{}, Result: nil},
			},
		},
		{
			desc:  "with group3 in custom queue",
			qname: "custom",
			gname: "group3",
			want: []*base.TaskInfo{
				{Message: m4, State: base.TaskStateAggregating, NextProcessAt: time.Time{}, Result: nil},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := r.ListAggregating(tc.qname, tc.gname, Pagination{})
			if err != nil {
				t.Fatalf("ListAggregating returned error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ListAggregating = %v, want %v; (-want,+got)\n%s", got, tc.want, diff)
			}
		})
	}
}

func TestListAggregatingPagination(t *testing.T) {
	r := setup(t)
	defer r.Close()

	groupkey := base.GroupKey("default", "mygroup")
	fxt := struct {
		tasks     []*h.TaskSeedData
		allQueues []string
		allGroups map[string][]string
		groups    map[string][]redis.Z
	}{
		tasks:     []*h.TaskSeedData{}, // will be populated below
		allQueues: []string{"default"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"mygroup"},
		},
		groups: map[string][]redis.Z{
			groupkey: {}, // will be populated below
		},
	}

	now := time.Now()
	for i := 0; i < 100; i++ {
		msg := h.NewTaskMessageBuilder().SetType(fmt.Sprintf("task%d", i)).SetGroup("mygroup").Build()
		fxt.tasks = append(fxt.tasks, &h.TaskSeedData{
			Msg: msg, State: base.TaskStateAggregating,
		})
		fxt.groups[groupkey] = append(fxt.groups[groupkey], redis.Z{
			Member: msg.ID,
			Score:  float64(now.Add(-time.Duration(100-i) * time.Second).Unix()),
		})
	}

	tests := []struct {
		desc      string
		qname     string
		gname     string
		page      int
		size      int
		wantSize  int
		wantFirst string
		wantLast  string
	}{
		{
			desc:      "first page",
			qname:     "default",
			gname:     "mygroup",
			page:      0,
			size:      20,
			wantSize:  20,
			wantFirst: "task0",
			wantLast:  "task19",
		},
		{
			desc:      "second page",
			qname:     "default",
			gname:     "mygroup",
			page:      1,
			size:      20,
			wantSize:  20,
			wantFirst: "task20",
			wantLast:  "task39",
		},
		{
			desc:      "with different page size",
			qname:     "default",
			gname:     "mygroup",
			page:      2,
			size:      30,
			wantSize:  30,
			wantFirst: "task60",
			wantLast:  "task89",
		},
		{
			desc:      "last page",
			qname:     "default",
			gname:     "mygroup",
			page:      3,
			size:      30,
			wantSize:  10,
			wantFirst: "task90",
			wantLast:  "task99",
		},
		{
			desc:      "out of range",
			qname:     "default",
			gname:     "mygroup",
			page:      4,
			size:      30,
			wantSize:  0,
			wantFirst: "",
			wantLast:  "",
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := r.ListAggregating(tc.qname, tc.gname, Pagination{Page: tc.page, Size: tc.size})
			if err != nil {
				t.Fatalf("ListAggregating returned error: %v", err)
			}

			if len(got) != tc.wantSize {
				t.Errorf("got %d results, want %d", len(got), tc.wantSize)
			}

			if len(got) == 0 {
				return
			}

			first := got[0].Message
			if first.Type != tc.wantFirst {
				t.Errorf("First message %q, want %q", first.Type, tc.wantFirst)
			}

			last := got[len(got)-1].Message
			if last.Type != tc.wantLast {
				t.Errorf("Last message %q, want %q", last.Type, tc.wantLast)
			}
		})
	}
}

func TestListTasksError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		desc  string
		qname string
		match func(err error) bool
	}{
		{
			desc:  "It returns QueueNotFoundError if queue doesn't exist",
			qname: "nonexistent",
			match: errors.IsQueueNotFound,
		},
	}

	for _, tc := range tests {
		pgn := Pagination{Page: 0, Size: 20}
		if _, got := r.ListActive(tc.qname, pgn); !tc.match(got) {
			t.Errorf("%s: ListActive returned %v", tc.desc, got)
		}
		if _, got := r.ListPending(tc.qname, pgn); !tc.match(got) {
			t.Errorf("%s: ListPending returned %v", tc.desc, got)
		}
		if _, got := r.ListScheduled(tc.qname, pgn); !tc.match(got) {
			t.Errorf("%s: ListScheduled returned %v", tc.desc, got)
		}
		if _, got := r.ListRetry(tc.qname, pgn); !tc.match(got) {
			t.Errorf("%s: ListRetry returned %v", tc.desc, got)
		}
		if _, got := r.ListArchived(tc.qname, pgn); !tc.match(got) {
			t.Errorf("%s: ListArchived returned %v", tc.desc, got)
		}
	}
}

var (
	timeCmpOpt   = cmpopts.EquateApproxTime(2 * time.Second) // allow for 2 seconds margin in time.Time
	zScoreCmpOpt = h.EquateInt64Approx(2)                    // allow for 2 seconds margin in Z.Score
)

func TestRunArchivedTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("gen_thumbnail", nil)
	t3 := h.NewTaskMessageWithQueue("send_notification", nil, "critical")
	s1 := time.Now().Add(-5 * time.Minute).Unix()
	s2 := time.Now().Add(-time.Hour).Unix()

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		id           string
		wantArchived map[string][]*base.TaskMessage
		wantPending  map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
					{Message: t2, Score: s2},
				},
			},
			qname: "default",
			id:    t2.ID,
			wantArchived: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t2},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
					{Message: t2, Score: s2},
				},
				"critical": {
					{Message: t3, Score: s1},
				},
			},
			qname: "critical",
			id:    t3.ID,
			wantArchived: map[string][]*base.TaskMessage{
				"default":  {t1, t2},
				"critical": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {t3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		if got := r.RunTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.RunTask(%q, %s) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q, (-want, +got)\n%s", base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestRunRetryTask(t *testing.T) {
	r := setup(t)
	defer r.Close()

	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("gen_thumbnail", nil)
	t3 := h.NewTaskMessageWithQueue("send_notification", nil, "low")
	s1 := time.Now().Add(-5 * time.Minute).Unix()
	s2 := time.Now().Add(-time.Hour).Unix()
	tests := []struct {
		retry       map[string][]base.Z
		qname       string
		id          string
		wantRetry   map[string][]*base.TaskMessage
		wantPending map[string][]*base.TaskMessage
	}{
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
					{Message: t2, Score: s2},
				},
			},
			qname: "default",
			id:    t2.ID,
			wantRetry: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t2},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
					{Message: t2, Score: s2},
				},
				"low": {
					{Message: t3, Score: s2},
				},
			},
			qname: "low",
			id:    t3.ID,
			wantRetry: map[string][]*base.TaskMessage{
				"default": {t1, t2},
				"low":     {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"low":     {t3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)                      // clean up db before each test case
		h.SeedAllRetryQueues(t, r.client, tc.retry) // initialize retry queue

		if got := r.RunTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.RunTask(%q, %s) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}

		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q, (-want, +got)\n%s", base.RetryKey(qname), diff)
			}
		}
	}
}

func TestRunAggregatingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task1").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task2").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetType("task3").SetGroup("group1").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1"},
			base.AllGroups("custom"):  {"group1"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group1"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc          string
		qname         string
		id            string
		wantPending   map[string][]string
		wantAllGroups map[string][]string
		wantGroups    map[string][]redis.Z
	}{
		{
			desc:  "schedules task from a group with multiple tasks",
			qname: "default",
			id:    m1.ID,
			wantPending: map[string][]string{
				base.PendingKey("default"): {m1.ID},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {"group1"},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): {
					{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				},
			},
		},
		{
			desc:  "schedules task from a group with a single task",
			qname: "custom",
			id:    m3.ID,
			wantPending: map[string][]string{
				base.PendingKey("custom"): {m3.ID},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			err := r.RunTask(tc.qname, tc.id)
			if err != nil {
				t.Fatalf("RunTask returned error: %v", err)
			}

			h.AssertRedisLists(t, r.client, tc.wantPending)
			h.AssertRedisZSets(t, r.client, tc.wantGroups)
			h.AssertRedisSets(t, r.client, tc.wantAllGroups)
		})
	}
}

func TestRunScheduledTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("gen_thumbnail", nil)
	t3 := h.NewTaskMessageWithQueue("send_notification", nil, "notifications")
	s1 := time.Now().Add(-5 * time.Minute).Unix()
	s2 := time.Now().Add(-time.Hour).Unix()

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		id            string
		wantScheduled map[string][]*base.TaskMessage
		wantPending   map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
					{Message: t2, Score: s2},
				},
			},
			qname: "default",
			id:    t2.ID,
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t2},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
					{Message: t2, Score: s2},
				},
				"notifications": {
					{Message: t3, Score: s1},
				},
			},
			qname: "notifications",
			id:    t3.ID,
			wantScheduled: map[string][]*base.TaskMessage{
				"default":       {t1, t2},
				"notifications": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default":       {},
				"notifications": {t3},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		if got := r.RunTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.RunTask(%q, %s) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q, (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestRunTaskError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	s1 := time.Now().Add(-5 * time.Minute).Unix()

	tests := []struct {
		desc          string
		active        map[string][]*base.TaskMessage
		pending       map[string][]*base.TaskMessage
		scheduled     map[string][]base.Z
		qname         string
		id            string
		match         func(err error) bool
		wantActive    map[string][]*base.TaskMessage
		wantPending   map[string][]*base.TaskMessage
		wantScheduled map[string][]*base.TaskMessage
	}{
		{
			desc: "It should return QueueNotFoundError if the queue doesn't exist",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
				},
			},
			qname: "nonexistent",
			id:    t1.ID,
			match: errors.IsQueueNotFound,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {t1},
			},
		},
		{
			desc: "It should return TaskNotFound if the task is not found in the queue",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: s1},
				},
			},
			qname: "default",
			id:    uuid.NewString(),
			match: errors.IsTaskNotFound,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {t1},
			},
		},
		{
			desc: "It should return FailedPrecondition error if task is already active",
			active: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    t1.ID,
			match: func(err error) bool { return errors.CanonicalCode(err) == errors.FailedPrecondition },
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "It should return FailedPrecondition error if task is already pending",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			pending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    t1.ID,
			match: func(err error) bool { return errors.CanonicalCode(err) == errors.FailedPrecondition },
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllPendingQueues(t, r.client, tc.pending)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		got := r.RunTask(tc.qname, tc.id)
		if !tc.match(got) {
			t.Errorf("%s: unexpected return value %v", tc.desc, got)
			continue
		}

		for qname, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want, +got)\n%s", base.ActiveKey(qname), diff)
			}
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q, (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestRunAllScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("gen_thumbnail", nil)
	t3 := h.NewTaskMessage("reindex", nil)
	t4 := h.NewTaskMessageWithQueue("important_notification", nil, "custom")
	t5 := h.NewTaskMessageWithQueue("minor_notification", nil, "custom")

	tests := []struct {
		desc          string
		scheduled     map[string][]base.Z
		qname         string
		want          int64
		wantPending   map[string][]*base.TaskMessage
		wantScheduled map[string][]*base.TaskMessage
	}{
		{
			desc: "with tasks in scheduled queue",
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t2, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t3, Score: time.Now().Add(time.Hour).Unix()},
				},
			},
			qname: "default",
			want:  3,
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with empty scheduled queue",
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with custom queues",
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t2, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t3, Score: time.Now().Add(time.Hour).Unix()},
				},
				"custom": {
					{Message: t4, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t5, Score: time.Now().Add(time.Hour).Unix()},
				},
			},
			qname: "custom",
			want:  2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {t4, t5},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		got, err := r.RunAllScheduledTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; r.RunAllScheduledTasks(%q) = %v, %v; want %v, nil",
				tc.desc, tc.qname, got, err, tc.want)
			continue
		}

		if got != tc.want {
			t.Errorf("%s; r.RunAllScheduledTasks(%q) = %v, %v; want %v, nil",
				tc.desc, tc.qname, got, err, tc.want)
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want, +got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want, +got)\n%s", tc.desc, base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestRunAllRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("gen_thumbnail", nil)
	t3 := h.NewTaskMessage("reindex", nil)
	t4 := h.NewTaskMessageWithQueue("important_notification", nil, "custom")
	t5 := h.NewTaskMessageWithQueue("minor_notification", nil, "custom")

	tests := []struct {
		desc        string
		retry       map[string][]base.Z
		qname       string
		want        int64
		wantPending map[string][]*base.TaskMessage
		wantRetry   map[string][]*base.TaskMessage
	}{
		{
			desc: "with tasks in retry queue",
			retry: map[string][]base.Z{
				"default": {
					{Message: t1, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t2, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t3, Score: time.Now().Add(time.Hour).Unix()},
				},
			},
			qname: "default",
			want:  3,
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with empty retry queue",
			retry: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with custom queues",
			retry: map[string][]base.Z{
				"default": {
					{Message: t1, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t2, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t3, Score: time.Now().Add(time.Hour).Unix()},
				},
				"custom": {
					{Message: t4, Score: time.Now().Add(time.Hour).Unix()},
					{Message: t5, Score: time.Now().Add(time.Hour).Unix()},
				},
			},
			qname: "custom",
			want:  2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {t4, t5},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		got, err := r.RunAllRetryTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; r.RunAllRetryTasks(%q) = %v, %v; want %v, nil",
				tc.desc, tc.qname, got, err, tc.want)
			continue
		}

		if got != tc.want {
			t.Errorf("%s; r.RunAllRetryTasks(%q) = %v, %v; want %v, nil",
				tc.desc, tc.qname, got, err, tc.want)
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want, +got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want, +got)\n%s", tc.desc, base.RetryKey(qname), diff)
			}
		}
	}
}

func TestRunAllArchivedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("gen_thumbnail", nil)
	t3 := h.NewTaskMessage("reindex", nil)
	t4 := h.NewTaskMessageWithQueue("important_notification", nil, "custom")
	t5 := h.NewTaskMessageWithQueue("minor_notification", nil, "custom")

	tests := []struct {
		desc         string
		archived     map[string][]base.Z
		qname        string
		want         int64
		wantPending  map[string][]*base.TaskMessage
		wantArchived map[string][]*base.TaskMessage
	}{
		{
			desc: "with tasks in archived queue",
			archived: map[string][]base.Z{
				"default": {
					{Message: t1, Score: time.Now().Add(-time.Minute).Unix()},
					{Message: t2, Score: time.Now().Add(-time.Minute).Unix()},
					{Message: t3, Score: time.Now().Add(-time.Minute).Unix()},
				},
			},
			qname: "default",
			want:  3,
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with empty archived queue",
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with custom queues",
			archived: map[string][]base.Z{
				"default": {
					{Message: t1, Score: time.Now().Add(-time.Minute).Unix()},
					{Message: t2, Score: time.Now().Add(-time.Minute).Unix()},
					{Message: t3, Score: time.Now().Add(-time.Minute).Unix()},
				},
				"custom": {
					{Message: t4, Score: time.Now().Add(-time.Minute).Unix()},
					{Message: t5, Score: time.Now().Add(-time.Minute).Unix()},
				},
			},
			qname: "custom",
			want:  2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {t4, t5},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.RunAllArchivedTasks(tc.qname)
		if err != nil {
			t.Errorf("%s; r.RunAllArchivedTasks(%q) = %v, %v; want %v, nil",
				tc.desc, tc.qname, got, err, tc.want)
			continue
		}

		if got != tc.want {
			t.Errorf("%s; r.RunAllArchivedTasks(%q) = %v, %v; want %v, nil",
				tc.desc, tc.qname, got, err, tc.want)
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want, +got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want, +got)\n%s", tc.desc, base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestRunAllTasksError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		desc  string
		qname string
		match func(err error) bool
	}{
		{
			desc:  "It returns QueueNotFoundError if queue doesn't exist",
			qname: "nonexistent",
			match: errors.IsQueueNotFound,
		},
	}

	for _, tc := range tests {
		if _, got := r.RunAllScheduledTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: RunAllScheduledTasks returned %v", tc.desc, got)
		}
		if _, got := r.RunAllRetryTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: RunAllRetryTasks returned %v", tc.desc, got)
		}
		if _, got := r.RunAllArchivedTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: RunAllArchivedTasks returned %v", tc.desc, got)
		}
		if _, got := r.RunAllAggregatingTasks(tc.qname, "mygroup"); !tc.match(got) {
			t.Errorf("%s: RunAllAggregatingTasks returned %v", tc.desc, got)
		}
	}
}

func TestRunAllAggregatingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task1").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task2").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetType("task3").SetGroup("group2").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1"},
			base.AllGroups("custom"):  {"group2"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group2"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc          string
		qname         string
		gname         string
		want          int64
		wantPending   map[string][]string
		wantGroups    map[string][]redis.Z
		wantAllGroups map[string][]string
	}{
		{
			desc:  "schedules tasks in a group with multiple tasks",
			qname: "default",
			gname: "group1",
			want:  2,
			wantPending: map[string][]string{
				base.PendingKey("default"): {m1.ID, m2.ID},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {},
				base.GroupKey("custom", "group2"): {
					{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {},
				base.AllGroups("custom"):  {"group2"},
			},
		},
		{
			desc:  "schedules tasks in a group with a single task",
			qname: "custom",
			gname: "group2",
			want:  1,
			wantPending: map[string][]string{
				base.PendingKey("custom"): {m3.ID},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group2"): {},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := r.RunAllAggregatingTasks(tc.qname, tc.gname)
			if err != nil {
				t.Fatalf("RunAllAggregatingTasks returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("RunAllAggregatingTasks = %d, want %d", got, tc.want)
			}
			h.AssertRedisLists(t, r.client, tc.wantPending)
			h.AssertRedisZSets(t, r.client, tc.wantGroups)
			h.AssertRedisSets(t, r.client, tc.wantAllGroups)
		})
	}
}

func TestArchiveRetryTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	t1 := time.Now().Add(1 * time.Minute)
	t2 := time.Now().Add(1 * time.Hour)
	t3 := time.Now().Add(2 * time.Hour)
	t4 := time.Now().Add(3 * time.Hour)

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
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    m1.ID,
			wantRetry: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			wantArchived: map[string][]base.Z{
				"default": {{Message: m1, Score: time.Now().Unix()}},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
					{Message: m4, Score: t4.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    m3.ID,
			wantRetry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m4, Score: t4.Unix()},
				},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom":  {{Message: m3, Score: time.Now().Unix()}},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllRetryQueues(t, r.client, tc.retry)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		if got := r.ArchiveTask(tc.qname, tc.id); got != nil {
			t.Errorf("(*RDB).ArchiveTask(%q, %v) returned error: %v",
				tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.RetryKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveScheduledTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	t1 := time.Now().Add(1 * time.Minute)
	t2 := time.Now().Add(1 * time.Hour)
	t3 := time.Now().Add(2 * time.Hour)
	t4 := time.Now().Add(3 * time.Hour)

	tests := []struct {
		scheduled     map[string][]base.Z
		archived      map[string][]base.Z
		qname         string
		id            string
		wantScheduled map[string][]base.Z
		wantArchived  map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    m1.ID,
			wantScheduled: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			wantArchived: map[string][]base.Z{
				"default": {{Message: m1, Score: time.Now().Unix()}},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
					{Message: m4, Score: t4.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    m3.ID,
			wantScheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m4, Score: t4.Unix()},
				},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom":  {{Message: m3, Score: time.Now().Unix()}},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		if got := r.ArchiveTask(tc.qname, tc.id); got != nil {
			t.Errorf("(*RDB).ArchiveTask(%q, %v) returned error: %v",
				tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ScheduledKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveAggregatingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task1").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task2").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetType("task3").SetGroup("group1").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1"},
			base.AllGroups("custom"):  {"group1"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group1"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc          string
		qname         string
		id            string
		wantArchived  map[string][]redis.Z
		wantAllGroups map[string][]string
		wantGroups    map[string][]redis.Z
	}{
		{
			desc:  "archive task from a group with multiple tasks",
			qname: "default",
			id:    m1.ID,
			wantArchived: map[string][]redis.Z{
				base.ArchivedKey("default"): {
					{Member: m1.ID, Score: float64(now.Unix())},
				},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {"group1"},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): {
					{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				},
			},
		},
		{
			desc:  "archive task from a group with a single task",
			qname: "custom",
			id:    m3.ID,
			wantArchived: map[string][]redis.Z{
				base.ArchivedKey("custom"): {
					{Member: m3.ID, Score: float64(now.Unix())},
				},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			err := r.ArchiveTask(tc.qname, tc.id)
			if err != nil {
				t.Fatalf("ArchiveTask returned error: %v", err)
			}

			h.AssertRedisZSets(t, r.client, tc.wantArchived)
			h.AssertRedisZSets(t, r.client, tc.wantGroups)
			h.AssertRedisSets(t, r.client, tc.wantAllGroups)
		})
	}
}

func TestArchivePendingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

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
				"default": {m1, m2},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    m1.ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m2},
			},
			wantArchived: map[string][]base.Z{
				"default": {{Message: m1, Score: time.Now().Unix()}},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3, m4},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			id:    m3.ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m4},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom":  {{Message: m3, Score: time.Now().Unix()}},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllPendingQueues(t, r.client, tc.pending)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		if got := r.ArchiveTask(tc.qname, tc.id); got != nil {
			t.Errorf("(*RDB).ArchiveTask(%q, %v) returned error: %v",
				tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.PendingKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveTaskError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	t1 := time.Now().Add(1 * time.Minute)
	t2 := time.Now().Add(1 * time.Hour)

	tests := []struct {
		desc          string
		active        map[string][]*base.TaskMessage
		scheduled     map[string][]base.Z
		archived      map[string][]base.Z
		qname         string
		id            string
		match         func(err error) bool
		wantActive    map[string][]*base.TaskMessage
		wantScheduled map[string][]base.Z
		wantArchived  map[string][]base.Z
	}{
		{
			desc: "It should return QueueNotFoundError if provided queue name doesn't exist",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			archived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			qname: "nonexistent",
			id:    m2.ID,
			match: errors.IsQueueNotFound,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			wantArchived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
		},
		{
			desc: "It should return TaskNotFoundError if provided task ID doesn't exist in the queue",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			archived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			qname: "default",
			id:    uuid.NewString(),
			match: errors.IsTaskNotFound,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			wantArchived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
		},
		{
			desc: "It should return TaskAlreadyArchivedError if task is already in archived state",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			archived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			qname: "default",
			id:    m2.ID,
			match: errors.IsTaskAlreadyArchived,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			wantArchived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
		},
		{
			desc: "It should return FailedPrecondition error if task is active",
			active: map[string][]*base.TaskMessage{
				"default": {m1},
			},
			scheduled: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    m1.ID,
			match: func(err error) bool { return errors.CanonicalCode(err) == errors.FailedPrecondition },
			wantActive: map[string][]*base.TaskMessage{
				"default": {m1},
			},
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got := r.ArchiveTask(tc.qname, tc.id)
		if !tc.match(got) {
			t.Errorf("%s: returned error didn't match: got=%v", tc.desc, got)
			continue
		}

		for qname, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s",
					base.ActiveKey(qname), diff)
			}
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ScheduledKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt, zScoreCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveAllPendingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	t1 := now.Add(1 * time.Minute)
	t2 := now.Add(1 * time.Hour)

	r.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		pending      map[string][]*base.TaskMessage
		archived     map[string][]base.Z
		qname        string
		want         int64
		wantPending  map[string][]*base.TaskMessage
		wantArchived map[string][]base.Z
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
					{Message: m2, Score: now.Unix()},
				},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1},
			},
			archived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			qname: "default",
			want:  1,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			qname: "default",
			want:  0,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3, m4},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			want:  2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: m3, Score: now.Unix()},
					{Message: m4, Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllPendingQueues(t, r.client, tc.pending)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.ArchiveAllPendingTasks(tc.qname)
		if got != tc.want || err != nil {
			t.Errorf("(*RDB).KillAllRetryTasks(%q) = %v, %v; want %v, nil",
				tc.qname, got, err, tc.want)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.PendingKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveAllAggregatingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task1").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task2").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetType("task3").SetGroup("group2").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1"},
			base.AllGroups("custom"):  {"group2"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group2"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc          string
		qname         string
		gname         string
		want          int64
		wantArchived  map[string][]redis.Z
		wantGroups    map[string][]redis.Z
		wantAllGroups map[string][]string
	}{
		{
			desc:  "archive tasks in a group with multiple tasks",
			qname: "default",
			gname: "group1",
			want:  2,
			wantArchived: map[string][]redis.Z{
				base.ArchivedKey("default"): {
					{Member: m1.ID, Score: float64(now.Unix())},
					{Member: m2.ID, Score: float64(now.Unix())},
				},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {},
				base.GroupKey("custom", "group2"): {
					{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {},
				base.AllGroups("custom"):  {"group2"},
			},
		},
		{
			desc:  "archive tasks in a group with a single task",
			qname: "custom",
			gname: "group2",
			want:  1,
			wantArchived: map[string][]redis.Z{
				base.ArchivedKey("custom"): {
					{Member: m3.ID, Score: float64(now.Unix())},
				},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group2"): {},
			},
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := r.ArchiveAllAggregatingTasks(tc.qname, tc.gname)
			if err != nil {
				t.Fatalf("ArchiveAllAggregatingTasks returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ArchiveAllAggregatingTasks = %d, want %d", got, tc.want)
			}
			h.AssertRedisZSets(t, r.client, tc.wantArchived)
			h.AssertRedisZSets(t, r.client, tc.wantGroups)
			h.AssertRedisSets(t, r.client, tc.wantAllGroups)
		})
	}
}

func TestArchiveAllRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	t1 := now.Add(1 * time.Minute)
	t2 := now.Add(1 * time.Hour)
	t3 := now.Add(2 * time.Hour)
	t4 := now.Add(3 * time.Hour)

	r.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		retry        map[string][]base.Z
		archived     map[string][]base.Z
		qname        string
		want         int64
		wantRetry    map[string][]base.Z
		wantArchived map[string][]base.Z
	}{
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  2,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
					{Message: m2, Score: now.Unix()},
				},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			archived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			qname: "default",
			want:  1,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			qname: "default",
			want:  0,
			wantRetry: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
					{Message: m4, Score: t4.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			want:  2,
			wantRetry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: m3, Score: now.Unix()},
					{Message: m4, Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllRetryQueues(t, r.client, tc.retry)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.ArchiveAllRetryTasks(tc.qname)
		if got != tc.want || err != nil {
			t.Errorf("(*RDB).KillAllRetryTasks(%q) = %v, %v; want %v, nil",
				tc.qname, got, err, tc.want)
			continue
		}

		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.RetryKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveAllScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	now := time.Now()
	t1 := now.Add(time.Minute)
	t2 := now.Add(time.Hour)
	t3 := now.Add(time.Hour)
	t4 := now.Add(time.Hour)

	r.SetClock(timeutil.NewSimulatedClock(now))

	tests := []struct {
		scheduled     map[string][]base.Z
		archived      map[string][]base.Z
		qname         string
		want          int64
		wantScheduled map[string][]base.Z
		wantArchived  map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  2,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
					{Message: m2, Score: now.Unix()},
				},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			archived: map[string][]base.Z{
				"default": {{Message: m2, Score: t2.Unix()}},
			},
			qname: "default",
			want:  1,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: now.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			qname: "default",
			want:  0,
			wantScheduled: map[string][]base.Z{
				"default": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
					{Message: m4, Score: t4.Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			qname: "custom",
			want:  2,
			wantScheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: m3, Score: now.Unix()},
					{Message: m4, Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.ArchiveAllScheduledTasks(tc.qname)
		if got != tc.want || err != nil {
			t.Errorf("(*RDB).KillAllScheduledTasks(%q) = %v, %v; want %v, nil",
				tc.qname, got, err, tc.want)
			continue
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ScheduledKey(qname), diff)
			}
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want,+got)\n%s",
					base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestArchiveAllTasksError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		desc  string
		qname string
		match func(err error) bool
	}{
		{
			desc:  "It returns QueueNotFoundError if queue doesn't exist",
			qname: "nonexistent",
			match: errors.IsQueueNotFound,
		},
	}

	for _, tc := range tests {
		if _, got := r.ArchiveAllPendingTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: ArchiveAllPendingTasks returned %v", tc.desc, got)
		}
		if _, got := r.ArchiveAllScheduledTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: ArchiveAllScheduledTasks returned %v", tc.desc, got)
		}
		if _, got := r.ArchiveAllRetryTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: ArchiveAllRetryTasks returned %v", tc.desc, got)
		}
	}
}

func TestDeleteArchivedTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	t1 := time.Now().Add(-5 * time.Minute)
	t2 := time.Now().Add(-time.Hour)
	t3 := time.Now().Add(-time.Hour)

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		id           string
		wantArchived map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			qname: "default",
			id:    m1.ID,
			wantArchived: map[string][]*base.TaskMessage{
				"default": {m2},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
				},
			},
			qname: "custom",
			id:    m3.ID,
			wantArchived: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		if got := r.DeleteTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.DeleteTask(%q, %v) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestDeleteRetryTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	t1 := time.Now().Add(5 * time.Minute)
	t2 := time.Now().Add(time.Hour)
	t3 := time.Now().Add(time.Hour)

	tests := []struct {
		retry     map[string][]base.Z
		qname     string
		id        string
		wantRetry map[string][]*base.TaskMessage
	}{
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			qname: "default",
			id:    m1.ID,
			wantRetry: map[string][]*base.TaskMessage{
				"default": {m2},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
				},
			},
			qname: "custom",
			id:    m3.ID,
			wantRetry: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		if got := r.DeleteTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.DeleteTask(%q, %v) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.RetryKey(qname), diff)
			}
		}
	}
}

func TestDeleteScheduledTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	t1 := time.Now().Add(5 * time.Minute)
	t2 := time.Now().Add(time.Hour)
	t3 := time.Now().Add(time.Hour)

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		id            string
		wantScheduled map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
			},
			qname: "default",
			id:    m1.ID,
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {m2},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
					{Message: m2, Score: t2.Unix()},
				},
				"custom": {
					{Message: m3, Score: t3.Unix()},
				},
			},
			qname: "custom",
			id:    m3.ID,
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		if got := r.DeleteTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.DeleteTask(%q, %v) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestDeleteAggregatingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task1").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task2").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetType("task3").SetGroup("group1").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1"},
			base.AllGroups("custom"):  {"group1"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group1"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc          string
		qname         string
		id            string
		wantAllGroups map[string][]string
		wantGroups    map[string][]redis.Z
	}{
		{
			desc:  "deletes a task from group with multiple tasks",
			qname: "default",
			id:    m1.ID,
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {"group1"},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): {
					{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				},
			},
		},
		{
			desc:  "deletes a task from group with single task",
			qname: "custom",
			id:    m3.ID,
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {}, // should be clear out group from all-groups set
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			err := r.DeleteTask(tc.qname, tc.id)
			if err != nil {
				t.Fatalf("DeleteTask returned error: %v", err)
			}
			h.AssertRedisSets(t, r.client, tc.wantAllGroups)
			h.AssertRedisZSets(t, r.client, tc.wantGroups)
		})
	}
}

func TestDeletePendingTask(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	tests := []struct {
		pending     map[string][]*base.TaskMessage
		qname       string
		id          string
		wantPending map[string][]*base.TaskMessage
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
			},
			qname: "default",
			id:    m1.ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m2},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			qname: "custom",
			id:    m3.ID,
			wantPending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllPendingQueues(t, r.client, tc.pending)

		if got := r.DeleteTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.DeleteTask(%q, %v) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}
	}
}

func TestDeleteTaskWithUniqueLock(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "email",
		Payload:   h.JSON(map[string]interface{}{"user_id": json.Number("123")}),
		Queue:     base.DefaultQueueName,
		UniqueKey: base.UniqueKey(base.DefaultQueueName, "email", h.JSON(map[string]interface{}{"user_id": 123})),
	}
	t1 := time.Now().Add(3 * time.Hour)

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		id            string
		uniqueKey     string
		wantScheduled map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: t1.Unix()},
				},
			},
			qname:     "default",
			id:        m1.ID,
			uniqueKey: m1.UniqueKey,
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		if got := r.DeleteTask(tc.qname, tc.id); got != nil {
			t.Errorf("r.DeleteTask(%q, %v) returned error: %v", tc.qname, tc.id, got)
			continue
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}

		if r.client.Exists(context.Background(), tc.uniqueKey).Val() != 0 {
			t.Errorf("Uniqueness lock %q still exists", tc.uniqueKey)
		}
	}
}

func TestDeleteTaskError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	t1 := time.Now().Add(5 * time.Minute)

	tests := []struct {
		desc          string
		active        map[string][]*base.TaskMessage
		scheduled     map[string][]base.Z
		qname         string
		id            string
		match         func(err error) bool
		wantActive    map[string][]*base.TaskMessage
		wantScheduled map[string][]*base.TaskMessage
	}{
		{
			desc: "It should return TaskNotFoundError if task doesn't exist the queue",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			qname: "default",
			id:    uuid.NewString(),
			match: errors.IsTaskNotFound,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {m1},
			},
		},
		{
			desc: "It should return QueueNotFoundError if the queue doesn't exist",
			active: map[string][]*base.TaskMessage{
				"default": {},
			},
			scheduled: map[string][]base.Z{
				"default": {{Message: m1, Score: t1.Unix()}},
			},
			qname: "nonexistent",
			id:    uuid.NewString(),
			match: errors.IsQueueNotFound,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {m1},
			},
		},
		{
			desc: "It should return FailedPrecondition error if task is active",
			active: map[string][]*base.TaskMessage{
				"default": {m1},
			},
			scheduled: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			id:    m1.ID,
			match: func(err error) bool { return errors.CanonicalCode(err) == errors.FailedPrecondition },
			wantActive: map[string][]*base.TaskMessage{
				"default": {m1},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		got := r.DeleteTask(tc.qname, tc.id)
		if !tc.match(got) {
			t.Errorf("%s: r.DeleteTask(qname, id) returned %v", tc.desc, got)
			continue
		}

		for qname, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ActiveKey(qname), diff)
			}
		}

		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestDeleteAllArchivedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		want         int64
		wantArchived map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: time.Now().Unix()},
					{Message: m2, Score: time.Now().Unix()},
				},
				"custom": {
					{Message: m3, Score: time.Now().Unix()},
				},
			},
			qname: "default",
			want:  2,
			wantArchived: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m3},
			},
		},
		{
			archived: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantArchived: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.DeleteAllArchivedTasks(tc.qname)
		if err != nil {
			t.Errorf("r.DeleteAllArchivedTasks(%q) returned error: %v", tc.qname, err)
		}
		if got != tc.want {
			t.Errorf("r.DeleteAllArchivedTasks(%q) = %d, nil, want %d, nil", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ArchivedKey(qname), diff)
			}
		}
	}
}

func newCompletedTaskMessage(qname, typename string, retention time.Duration, completedAt time.Time) *base.TaskMessage {
	msg := h.NewTaskMessageWithQueue(typename, nil, qname)
	msg.Retention = int64(retention.Seconds())
	msg.CompletedAt = completedAt.Unix()
	return msg
}

func TestDeleteAllCompletedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	m1 := newCompletedTaskMessage("default", "task1", 30*time.Minute, now.Add(-2*time.Minute))
	m2 := newCompletedTaskMessage("default", "task2", 30*time.Minute, now.Add(-5*time.Minute))
	m3 := newCompletedTaskMessage("custom", "task3", 30*time.Minute, now.Add(-5*time.Minute))

	tests := []struct {
		completed     map[string][]base.Z
		qname         string
		want          int64
		wantCompleted map[string][]*base.TaskMessage
	}{
		{
			completed: map[string][]base.Z{
				"default": {
					{Message: m1, Score: m1.CompletedAt + m1.Retention},
					{Message: m2, Score: m2.CompletedAt + m2.Retention},
				},
				"custom": {
					{Message: m3, Score: m2.CompletedAt + m3.Retention},
				},
			},
			qname: "default",
			want:  2,
			wantCompleted: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m3},
			},
		},
		{
			completed: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantCompleted: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllCompletedQueues(t, r.client, tc.completed)

		got, err := r.DeleteAllCompletedTasks(tc.qname)
		if err != nil {
			t.Errorf("r.DeleteAllCompletedTasks(%q) returned error: %v", tc.qname, err)
		}
		if got != tc.want {
			t.Errorf("r.DeleteAllCompletedTasks(%q) = %d, nil, want %d, nil", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantCompleted {
			gotCompleted := h.GetCompletedMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotCompleted, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.CompletedKey(qname), diff)
			}
		}
	}
}

func TestDeleteAllArchivedTasksWithUniqueKey(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "task1",
		Payload:   nil,
		Timeout:   1800,
		Deadline:  0,
		UniqueKey: "asynq:{default}:unique:task1:nil",
		Queue:     "default",
	}
	m2 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "task2",
		Payload:   nil,
		Timeout:   1800,
		Deadline:  0,
		UniqueKey: "asynq:{default}:unique:task2:nil",
		Queue:     "default",
	}
	m3 := h.NewTaskMessage("task3", nil)

	tests := []struct {
		archived     map[string][]base.Z
		qname        string
		want         int64
		uniqueKeys   []string // list of unique keys that should be cleared
		wantArchived map[string][]*base.TaskMessage
	}{
		{
			archived: map[string][]base.Z{
				"default": {
					{Message: m1, Score: time.Now().Unix()},
					{Message: m2, Score: time.Now().Unix()},
					{Message: m3, Score: time.Now().Unix()},
				},
			},
			qname:      "default",
			want:       3,
			uniqueKeys: []string{m1.UniqueKey, m2.UniqueKey},
			wantArchived: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got, err := r.DeleteAllArchivedTasks(tc.qname)
		if err != nil {
			t.Errorf("r.DeleteAllArchivedTasks(%q) returned error: %v", tc.qname, err)
		}
		if got != tc.want {
			t.Errorf("r.DeleteAllArchivedTasks(%q) = %d, nil, want %d, nil", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantArchived {
			gotArchived := h.GetArchivedMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ArchivedKey(qname), diff)
			}
		}

		for _, uniqueKey := range tc.uniqueKeys {
			if r.client.Exists(context.Background(), uniqueKey).Val() != 0 {
				t.Errorf("Uniqueness lock %q still exists", uniqueKey)
			}
		}
	}
}

func TestDeleteAllRetryTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	tests := []struct {
		retry     map[string][]base.Z
		qname     string
		want      int64
		wantRetry map[string][]*base.TaskMessage
	}{
		{
			retry: map[string][]base.Z{
				"default": {
					{Message: m1, Score: time.Now().Unix()},
					{Message: m2, Score: time.Now().Unix()},
				},
				"custom": {
					{Message: m3, Score: time.Now().Unix()},
				},
			},
			qname: "custom",
			want:  1,
			wantRetry: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
		},
		{
			retry: map[string][]base.Z{
				"default": {},
			},
			qname: "default",
			want:  0,
			wantRetry: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		got, err := r.DeleteAllRetryTasks(tc.qname)
		if err != nil {
			t.Errorf("r.DeleteAllRetryTasks(%q) returned error: %v", tc.qname, err)
		}
		if got != tc.want {
			t.Errorf("r.DeleteAllRetryTasks(%q) = %d, nil, want %d, nil", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.RetryKey(qname), diff)
			}
		}
	}
}

func TestDeleteAllScheduledTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	tests := []struct {
		scheduled     map[string][]base.Z
		qname         string
		want          int64
		wantScheduled map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: m1, Score: time.Now().Add(time.Minute).Unix()},
					{Message: m2, Score: time.Now().Add(time.Minute).Unix()},
				},
				"custom": {
					{Message: m3, Score: time.Now().Add(time.Minute).Unix()},
				},
			},
			qname: "default",
			want:  2,
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m3},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"custom": {},
			},
			qname: "custom",
			want:  0,
			wantScheduled: map[string][]*base.TaskMessage{
				"custom": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)

		got, err := r.DeleteAllScheduledTasks(tc.qname)
		if err != nil {
			t.Errorf("r.DeleteAllScheduledTasks(%q) returned error: %v", tc.qname, err)
		}
		if got != tc.want {
			t.Errorf("r.DeleteAllScheduledTasks(%q) = %d, nil, want %d, nil", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
	}
}

func TestDeleteAllAggregatingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task1").SetGroup("group1").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetType("task2").SetGroup("group1").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetType("task3").SetGroup("group1").Build()

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
		},
		allQueues: []string{"default", "custom"},
		allGroups: map[string][]string{
			base.AllGroups("default"): {"group1"},
			base.AllGroups("custom"):  {"group1"},
		},
		groups: map[string][]redis.Z{
			base.GroupKey("default", "group1"): {
				{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
			},
			base.GroupKey("custom", "group1"): {
				{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
			},
		},
	}

	tests := []struct {
		desc          string
		qname         string
		gname         string
		want          int64
		wantAllGroups map[string][]string
		wantGroups    map[string][]redis.Z
	}{
		{
			desc:  "default queue group1",
			qname: "default",
			gname: "group1",
			want:  2,
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {},
				base.AllGroups("custom"):  {"group1"},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): nil,
				base.GroupKey("custom", "group1"): {
					{Member: m3.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
				},
			},
		},
		{
			desc:  "custom queue group1",
			qname: "custom",
			gname: "group1",
			want:  1,
			wantAllGroups: map[string][]string{
				base.AllGroups("default"): {"group1"},
				base.AllGroups("custom"):  {},
			},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "group1"): {
					{Member: m1.ID, Score: float64(now.Add(-20 * time.Second).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-25 * time.Second).Unix())},
				},
				base.GroupKey("custom", "group1"): nil,
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedTasks(t, r.client, fxt.tasks)
		h.SeedRedisSet(t, r.client, base.AllQueues, fxt.allQueues)
		h.SeedRedisSets(t, r.client, fxt.allGroups)
		h.SeedRedisZSets(t, r.client, fxt.groups)

		t.Run(tc.desc, func(t *testing.T) {
			got, err := r.DeleteAllAggregatingTasks(tc.qname, tc.gname)
			if err != nil {
				t.Fatalf("DeleteAllAggregatingTasks returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("DeleteAllAggregatingTasks = %d, want %d", got, tc.want)
			}
			h.AssertRedisSets(t, r.client, tc.wantAllGroups)
			h.AssertRedisZSets(t, r.client, tc.wantGroups)
		})
	}
}

func TestDeleteAllPendingTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")

	tests := []struct {
		pending     map[string][]*base.TaskMessage
		qname       string
		want        int64
		wantPending map[string][]*base.TaskMessage
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			qname: "default",
			want:  2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m3},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"custom": {},
			},
			qname: "custom",
			want:  0,
			wantPending: map[string][]*base.TaskMessage{
				"custom": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllPendingQueues(t, r.client, tc.pending)

		got, err := r.DeleteAllPendingTasks(tc.qname)
		if err != nil {
			t.Errorf("r.DeleteAllPendingTasks(%q) returned error: %v", tc.qname, err)
		}
		if got != tc.want {
			t.Errorf("r.DeleteAllPendingTasks(%q) = %d, nil, want %d, nil", tc.qname, got, tc.want)
		}
		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}
	}
}

func TestDeleteAllTasksError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		desc  string
		qname string
		match func(err error) bool
	}{
		{
			desc:  "It returns QueueNotFoundError if queue doesn't exist",
			qname: "nonexistent",
			match: errors.IsQueueNotFound,
		},
	}

	for _, tc := range tests {
		if _, got := r.DeleteAllPendingTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: DeleteAllPendingTasks returned %v", tc.desc, got)
		}
		if _, got := r.DeleteAllScheduledTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: DeleteAllScheduledTasks returned %v", tc.desc, got)
		}
		if _, got := r.DeleteAllRetryTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: DeleteAllRetryTasks returned %v", tc.desc, got)
		}
		if _, got := r.DeleteAllArchivedTasks(tc.qname); !tc.match(got) {
			t.Errorf("%s: DeleteAllArchivedTasks returned %v", tc.desc, got)
		}
	}
}

func TestRemoveQueue(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

	tests := []struct {
		pending    map[string][]*base.TaskMessage
		inProgress map[string][]*base.TaskMessage
		scheduled  map[string][]base.Z
		retry      map[string][]base.Z
		archived   map[string][]base.Z
		qname      string // queue to remove
		force      bool
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {},
			},
			inProgress: map[string][]*base.TaskMessage{
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
			inProgress: map[string][]*base.TaskMessage{
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
		h.FlushDB(t, r.client)
		h.SeedAllPendingQueues(t, r.client, tc.pending)
		h.SeedAllActiveQueues(t, r.client, tc.inProgress)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllRetryQueues(t, r.client, tc.retry)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		err := r.RemoveQueue(tc.qname, tc.force)
		if err != nil {
			t.Errorf("(*RDB).RemoveQueue(%q, %t) = %v, want nil",
				tc.qname, tc.force, err)
			continue
		}
		if r.client.SIsMember(context.Background(), base.AllQueues, tc.qname).Val() {
			t.Errorf("%q is a member of %q", tc.qname, base.AllQueues)
		}

		keys := []string{
			base.PendingKey(tc.qname),
			base.ActiveKey(tc.qname),
			base.LeaseKey(tc.qname),
			base.ScheduledKey(tc.qname),
			base.RetryKey(tc.qname),
			base.ArchivedKey(tc.qname),
		}
		for _, key := range keys {
			if r.client.Exists(context.Background(), key).Val() != 0 {
				t.Errorf("key %q still exists", key)
			}
		}

		if n := len(r.client.Keys(context.Background(), base.TaskKeyPrefix(tc.qname)+"*").Val()); n != 0 {
			t.Errorf("%d keys still exists for tasks", n)
		}
	}
}

func TestRemoveQueueError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessageWithQueue("task3", nil, "custom")
	m4 := h.NewTaskMessageWithQueue("task4", nil, "custom")

	tests := []struct {
		desc       string
		pending    map[string][]*base.TaskMessage
		inProgress map[string][]*base.TaskMessage
		scheduled  map[string][]base.Z
		retry      map[string][]base.Z
		archived   map[string][]base.Z
		qname      string // queue to remove
		force      bool
		match      func(err error) bool
	}{
		{
			desc: "removing non-existent queue",
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			inProgress: map[string][]*base.TaskMessage{
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
			qname: "nonexistent",
			force: false,
			match: errors.IsQueueNotFound,
		},
		{
			desc: "removing non-empty queue",
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			inProgress: map[string][]*base.TaskMessage{
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
			force: false,
			match: errors.IsQueueNotEmpty,
		},
		{
			desc: "force removing queue with active tasks",
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"custom":  {m3},
			},
			inProgress: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {m4},
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
			// Even with force=true, it should error if there are active tasks.
			force: true,
			match: func(err error) bool { return errors.CanonicalCode(err) == errors.FailedPrecondition },
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllPendingQueues(t, r.client, tc.pending)
		h.SeedAllActiveQueues(t, r.client, tc.inProgress)
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllRetryQueues(t, r.client, tc.retry)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		got := r.RemoveQueue(tc.qname, tc.force)
		if !tc.match(got) {
			t.Errorf("%s; returned error didn't match expected value; got=%v", tc.desc, got)
			continue
		}

		// Make sure that nothing changed
		for qname, want := range tc.pending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("%s;mismatch found in %q; (-want,+got):\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.inProgress {
			gotActive := h.GetActiveMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("%s;mismatch found in %q; (-want,+got):\n%s", tc.desc, base.ActiveKey(qname), diff)
			}
		}
		for qname, want := range tc.scheduled {
			gotScheduled := h.GetScheduledEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s;mismatch found in %q; (-want,+got):\n%s", tc.desc, base.ScheduledKey(qname), diff)
			}
		}
		for qname, want := range tc.retry {
			gotRetry := h.GetRetryEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s;mismatch found in %q; (-want,+got):\n%s", tc.desc, base.RetryKey(qname), diff)
			}
		}
		for qname, want := range tc.archived {
			gotArchived := h.GetArchivedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s;mismatch found in %q; (-want,+got):\n%s", tc.desc, base.ArchivedKey(qname), diff)
			}
		}
	}
}

func TestListServers(t *testing.T) {
	r := setup(t)
	defer r.Close()

	started1 := time.Now().Add(-time.Hour)
	info1 := &base.ServerInfo{
		Host:              "do.droplet1",
		PID:               1234,
		ServerID:          "server123",
		Concurrency:       10,
		Queues:            map[string]int{"default": 1},
		Status:            "active",
		Started:           started1,
		ActiveWorkerCount: 0,
	}

	started2 := time.Now().Add(-2 * time.Hour)
	info2 := &base.ServerInfo{
		Host:              "do.droplet2",
		PID:               9876,
		ServerID:          "server456",
		Concurrency:       20,
		Queues:            map[string]int{"email": 1},
		Status:            "stopped",
		Started:           started2,
		ActiveWorkerCount: 1,
	}

	tests := []struct {
		data []*base.ServerInfo
	}{
		{
			data: []*base.ServerInfo{},
		},
		{
			data: []*base.ServerInfo{info1},
		},
		{
			data: []*base.ServerInfo{info1, info2},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		for _, info := range tc.data {
			if err := r.WriteServerState(info, []*base.WorkerInfo{}, 5*time.Second); err != nil {
				t.Fatal(err)
			}
		}

		got, err := r.ListServers()
		if err != nil {
			t.Errorf("r.ListServers returned an error: %v", err)
		}
		if diff := cmp.Diff(tc.data, got, h.SortServerInfoOpt); diff != "" {
			t.Errorf("r.ListServers returned %v, want %v; (-want,+got)\n%s",
				got, tc.data, diff)
		}
	}
}

func TestListWorkers(t *testing.T) {
	r := setup(t)
	defer r.Close()

	var (
		host     = "127.0.0.1"
		pid      = 4567
		serverID = "server123"

		m1 = h.NewTaskMessage("send_email", h.JSON(map[string]interface{}{"user_id": "abc123"}))
		m2 = h.NewTaskMessage("gen_thumbnail", h.JSON(map[string]interface{}{"path": "some/path/to/image/file"}))
		m3 = h.NewTaskMessage("reindex", h.JSON(map[string]interface{}{}))
	)

	tests := []struct {
		data []*base.WorkerInfo
	}{
		{
			data: []*base.WorkerInfo{
				{
					Host:     host,
					PID:      pid,
					ServerID: serverID,
					ID:       m1.ID,
					Type:     m1.Type,
					Queue:    m1.Queue,
					Payload:  m1.Payload,
					Started:  time.Now().Add(-1 * time.Second),
					Deadline: time.Now().Add(30 * time.Second),
				},
				{
					Host:     host,
					PID:      pid,
					ServerID: serverID,
					ID:       m2.ID,
					Type:     m2.Type,
					Queue:    m2.Queue,
					Payload:  m2.Payload,
					Started:  time.Now().Add(-5 * time.Second),
					Deadline: time.Now().Add(10 * time.Minute),
				},
				{
					Host:     host,
					PID:      pid,
					ServerID: serverID,
					ID:       m3.ID,
					Type:     m3.Type,
					Queue:    m3.Queue,
					Payload:  m3.Payload,
					Started:  time.Now().Add(-30 * time.Second),
					Deadline: time.Now().Add(30 * time.Minute),
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		err := r.WriteServerState(&base.ServerInfo{}, tc.data, time.Minute)
		if err != nil {
			t.Errorf("could not write server state to redis: %v", err)
			continue
		}

		got, err := r.ListWorkers()
		if err != nil {
			t.Errorf("(*RDB).ListWorkers() returned an error: %v", err)
			continue
		}

		if diff := cmp.Diff(tc.data, got, h.SortWorkerInfoOpt); diff != "" {
			t.Errorf("(*RDB).ListWorkers() = %v, want = %v; (-want,+got)\n%s", got, tc.data, diff)
		}
	}
}

func TestWriteListClearSchedulerEntries(t *testing.T) {
	r := setup(t)
	now := time.Now().UTC()
	schedulerID := "127.0.0.1:9876:abc123"

	data := []*base.SchedulerEntry{
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
			Opts:    nil,
			Next:    now.Add(1 * time.Minute),
			Prev:    now.Add(-19 * time.Minute),
		},
	}

	if err := r.WriteSchedulerEntries(schedulerID, data, 30*time.Second); err != nil {
		t.Fatalf("WriteSchedulerEnties failed: %v", err)
	}
	entries, err := r.ListSchedulerEntries()
	if err != nil {
		t.Fatalf("ListSchedulerEntries failed: %v", err)
	}
	if diff := cmp.Diff(data, entries, h.SortSchedulerEntryOpt); diff != "" {
		t.Errorf("ListSchedulerEntries() = %v, want %v; (-want,+got)\n%s", entries, data, diff)
	}
	if err := r.ClearSchedulerEntries(schedulerID); err != nil {
		t.Fatalf("ClearSchedulerEntries failed: %v", err)
	}
	entries, err = r.ListSchedulerEntries()
	if err != nil {
		t.Fatalf("ListSchedulerEntries() after clear failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("found %d entries, want 0 after clearing", len(entries))
	}
}

func TestSchedulerEnqueueEvents(t *testing.T) {
	r := setup(t)

	var (
		now          = time.Now()
		oneDayAgo    = now.Add(-24 * time.Hour)
		fiveHoursAgo = now.Add(-5 * time.Hour)
		oneHourAgo   = now.Add(-1 * time.Hour)
	)

	tests := []struct {
		entryID string
		events  []*base.SchedulerEnqueueEvent
		want    []*base.SchedulerEnqueueEvent
	}{
		{
			entryID: "entry123",
			events: []*base.SchedulerEnqueueEvent{
				{TaskID: "task123", EnqueuedAt: oneDayAgo},
				{TaskID: "task789", EnqueuedAt: oneHourAgo},
				{TaskID: "task456", EnqueuedAt: fiveHoursAgo},
			},
			// Recent events first
			want: []*base.SchedulerEnqueueEvent{
				{TaskID: "task789", EnqueuedAt: oneHourAgo},
				{TaskID: "task456", EnqueuedAt: fiveHoursAgo},
				{TaskID: "task123", EnqueuedAt: oneDayAgo},
			},
		},
		{
			entryID: "entry456",
			events:  nil,
			want:    nil,
		},
	}

loop:
	for _, tc := range tests {
		h.FlushDB(t, r.client)

		for _, e := range tc.events {
			if err := r.RecordSchedulerEnqueueEvent(tc.entryID, e); err != nil {
				t.Errorf("RecordSchedulerEnqueueEvent(%q, %v) failed: %v", tc.entryID, e, err)
				continue loop
			}
		}
		got, err := r.ListSchedulerEnqueueEvents(tc.entryID, Pagination{Size: 20, Page: 0})
		if err != nil {
			t.Errorf("ListSchedulerEnqueueEvents(%q) failed: %v", tc.entryID, err)
			continue
		}
		if diff := cmp.Diff(tc.want, got, timeCmpOpt); diff != "" {
			t.Errorf("ListSchedulerEnqueueEvent(%q) = %v, want %v; (-want,+got)\n%s",
				tc.entryID, got, tc.want, diff)
		}
	}
}

func TestRecordSchedulerEnqueueEventTrimsDataSet(t *testing.T) {
	r := setup(t)
	var (
		entryID = "entry123"
		now     = time.Now()
		key     = base.SchedulerHistoryKey(entryID)
	)

	// Record maximum number of events.
	for i := 1; i <= maxEvents; i++ {
		event := base.SchedulerEnqueueEvent{
			TaskID:     fmt.Sprintf("task%d", i),
			EnqueuedAt: now.Add(-time.Duration(i) * time.Second),
		}
		if err := r.RecordSchedulerEnqueueEvent(entryID, &event); err != nil {
			t.Fatalf("RecordSchedulerEnqueueEvent failed: %v", err)
		}
	}

	// Make sure the set is full.
	if n := r.client.ZCard(context.Background(), key).Val(); n != maxEvents {
		t.Fatalf("unexpected number of events; got %d, want %d", n, maxEvents)
	}

	// Record one more event, should evict the oldest event.
	event := base.SchedulerEnqueueEvent{
		TaskID:     "latest",
		EnqueuedAt: now,
	}
	if err := r.RecordSchedulerEnqueueEvent(entryID, &event); err != nil {
		t.Fatalf("RecordSchedulerEnqueueEvent failed: %v", err)
	}
	if n := r.client.ZCard(context.Background(), key).Val(); n != maxEvents {
		t.Fatalf("unexpected number of events; got %d, want %d", n, maxEvents)
	}
	events, err := r.ListSchedulerEnqueueEvents(entryID, Pagination{Size: maxEvents})
	if err != nil {
		t.Fatalf("ListSchedulerEnqueueEvents failed: %v", err)
	}
	if first := events[0]; first.TaskID != "latest" {
		t.Errorf("unexpected first event; got %q, want %q", first.TaskID, "latest")
	}
	if last := events[maxEvents-1]; last.TaskID != fmt.Sprintf("task%d", maxEvents-1) {
		t.Errorf("unexpected last event; got %q, want %q", last.TaskID, fmt.Sprintf("task%d", maxEvents-1))
	}
}

func TestPause(t *testing.T) {
	r := setup(t)

	tests := []struct {
		qname string // name of the queue to pause
	}{
		{qname: "default"},
		{qname: "custom"},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		err := r.Pause(tc.qname)
		if err != nil {
			t.Errorf("Pause(%q) returned error: %v", tc.qname, err)
		}
		key := base.PausedKey(tc.qname)
		if r.client.Exists(context.Background(), key).Val() == 0 {
			t.Errorf("key %q does not exist", key)
		}
	}
}

func TestPauseError(t *testing.T) {
	r := setup(t)

	tests := []struct {
		desc   string   // test case description
		paused []string // already paused queues
		qname  string   // name of the queue to pause
	}{
		{"queue already paused", []string{"default", "custom"}, "default"},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		for _, qname := range tc.paused {
			if err := r.Pause(qname); err != nil {
				t.Fatalf("could not pause %q: %v", qname, err)
			}
		}

		err := r.Pause(tc.qname)
		if err == nil {
			t.Errorf("%s; Pause(%q) returned nil: want error", tc.desc, tc.qname)
		}
	}
}

func TestUnpause(t *testing.T) {
	r := setup(t)

	tests := []struct {
		paused []string // already paused queues
		qname  string   // name of the queue to unpause
	}{
		{[]string{"default", "custom"}, "default"},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		for _, qname := range tc.paused {
			if err := r.Pause(qname); err != nil {
				t.Fatalf("could not pause %q: %v", qname, err)
			}
		}

		err := r.Unpause(tc.qname)
		if err != nil {
			t.Errorf("Unpause(%q) returned error: %v", tc.qname, err)
		}
		key := base.PausedKey(tc.qname)
		if r.client.Exists(context.Background(), key).Val() == 1 {
			t.Errorf("key %q exists", key)
		}
	}
}

func TestUnpauseError(t *testing.T) {
	r := setup(t)

	tests := []struct {
		desc   string   // test case description
		paused []string // already paused queues
		qname  string   // name of the queue to unpause
	}{
		{"queue is not paused", []string{"default"}, "custom"},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		for _, qname := range tc.paused {
			if err := r.Pause(qname); err != nil {
				t.Fatalf("could not pause %q: %v", qname, err)
			}
		}

		err := r.Unpause(tc.qname)
		if err == nil {
			t.Errorf("%s; Unpause(%q) returned nil: want error", tc.desc, tc.qname)
		}
	}
}
