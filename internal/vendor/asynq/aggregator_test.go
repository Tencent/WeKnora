// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/rdb"
	h "github.com/hibiken/asynq/internal/testutil"
)

func TestAggregator(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)
	client := Client{broker: rdbClient}

	tests := []struct {
		desc             string
		gracePeriod      time.Duration
		maxDelay         time.Duration
		maxSize          int
		aggregateFunc    func(gname string, tasks []*Task) *Task
		tasks            []*Task       // tasks to enqueue
		enqueueFrequency time.Duration // time between one enqueue event to another
		waitTime         time.Duration // time to wait
		wantGroups       map[string]map[string][]base.Z
		wantPending      map[string][]*base.TaskMessage
	}{
		{
			desc:        "group older than the grace period should be aggregated",
			gracePeriod: 1 * time.Second,
			maxDelay:    0, // no maxdelay limit
			maxSize:     0, // no maxsize limit
			aggregateFunc: func(gname string, tasks []*Task) *Task {
				return NewTask(gname, nil, MaxRetry(len(tasks))) // use max retry to see how many tasks were aggregated
			},
			tasks: []*Task{
				NewTask("task1", nil, Group("mygroup")),
				NewTask("task2", nil, Group("mygroup")),
				NewTask("task3", nil, Group("mygroup")),
			},
			enqueueFrequency: 300 * time.Millisecond,
			waitTime:         3 * time.Second,
			wantGroups: map[string]map[string][]base.Z{
				"default": {
					"mygroup": {},
				},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					h.NewTaskMessageBuilder().SetType("mygroup").SetRetry(3).Build(),
				},
			},
		},
		{
			desc:        "group older than the max-delay should be aggregated",
			gracePeriod: 2 * time.Second,
			maxDelay:    4 * time.Second,
			maxSize:     0, // no maxsize limit
			aggregateFunc: func(gname string, tasks []*Task) *Task {
				return NewTask(gname, nil, MaxRetry(len(tasks))) // use max retry to see how many tasks were aggregated
			},
			tasks: []*Task{
				NewTask("task1", nil, Group("mygroup")), // time 0
				NewTask("task2", nil, Group("mygroup")), // time 1s
				NewTask("task3", nil, Group("mygroup")), // time 2s
				NewTask("task4", nil, Group("mygroup")), // time 3s
			},
			enqueueFrequency: 1 * time.Second,
			waitTime:         4 * time.Second,
			wantGroups: map[string]map[string][]base.Z{
				"default": {
					"mygroup": {},
				},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					h.NewTaskMessageBuilder().SetType("mygroup").SetRetry(4).Build(),
				},
			},
		},
		{
			desc:        "group reached the max-size should be aggregated",
			gracePeriod: 1 * time.Minute,
			maxDelay:    0, // no maxdelay limit
			maxSize:     5,
			aggregateFunc: func(gname string, tasks []*Task) *Task {
				return NewTask(gname, nil, MaxRetry(len(tasks))) // use max retry to see how many tasks were aggregated
			},
			tasks: []*Task{
				NewTask("task1", nil, Group("mygroup")),
				NewTask("task2", nil, Group("mygroup")),
				NewTask("task3", nil, Group("mygroup")),
				NewTask("task4", nil, Group("mygroup")),
				NewTask("task5", nil, Group("mygroup")),
			},
			enqueueFrequency: 300 * time.Millisecond,
			waitTime:         defaultAggregationCheckInterval * 2,
			wantGroups: map[string]map[string][]base.Z{
				"default": {
					"mygroup": {},
				},
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {
					h.NewTaskMessageBuilder().SetType("mygroup").SetRetry(5).Build(),
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)

		aggregator := newAggregator(aggregatorParams{
			logger:          testLogger,
			broker:          rdbClient,
			queues:          []string{"default"},
			gracePeriod:     tc.gracePeriod,
			maxDelay:        tc.maxDelay,
			maxSize:         tc.maxSize,
			groupAggregator: GroupAggregatorFunc(tc.aggregateFunc),
		})

		var wg sync.WaitGroup
		aggregator.start(&wg)

		for _, task := range tc.tasks {
			if _, err := client.Enqueue(task); err != nil {
				t.Errorf("%s: Client Enqueue failed: %v", tc.desc, err)
				aggregator.shutdown()
				wg.Wait()
				continue
			}
			time.Sleep(tc.enqueueFrequency)
		}

		time.Sleep(tc.waitTime)

		for qname, groups := range tc.wantGroups {
			for gname, want := range groups {
				gotGroup := h.GetGroupEntries(t, r, qname, gname)
				if diff := cmp.Diff(want, gotGroup, h.SortZSetEntryOpt); diff != "" {
					t.Errorf("%s: mismatch found in %q; (-want,+got)\n%s", tc.desc, base.GroupKey(qname, gname), diff)
				}
			}
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt, h.IgnoreIDOpt); diff != "" {
				t.Errorf("%s: mismatch found in %q; (-want,+got)\n%s", tc.desc, base.PendingKey(qname), diff)
			}
		}
		aggregator.shutdown()
		wg.Wait()
	}
}
