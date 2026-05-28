// Copyright 2020 Kentaro Hibino. All rights reserved.
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

func TestRecoverer(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	t1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	t2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	t3 := h.NewTaskMessageWithQueue("task3", nil, "critical")
	t4 := h.NewTaskMessageWithQueue("task4", nil, "default")
	t4.Retried = t4.Retry // t4 has reached its max retry count

	now := time.Now()

	tests := []struct {
		desc         string
		active       map[string][]*base.TaskMessage
		lease        map[string][]base.Z
		retry        map[string][]base.Z
		archived     map[string][]base.Z
		wantActive   map[string][]*base.TaskMessage
		wantLease    map[string][]base.Z
		wantRetry    map[string][]*base.TaskMessage
		wantArchived map[string][]*base.TaskMessage
	}{
		{
			desc: "with one active task",
			active: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(-1 * time.Minute).Unix()}},
			},
			retry: map[string][]base.Z{
				"default": {},
			},
			archived: map[string][]base.Z{
				"default": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantLease: map[string][]base.Z{
				"default": {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			desc: "with a task with max-retry reached",
			active: map[string][]*base.TaskMessage{
				"default":  {t4},
				"critical": {},
			},
			lease: map[string][]base.Z{
				"default":  {{Message: t4, Score: now.Add(-40 * time.Second).Unix()}},
				"critical": {},
			},
			retry: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			archived: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
			wantLease: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default":  {t4},
				"critical": {},
			},
		},
		{
			desc: "with multiple active tasks, and one expired",
			active: map[string][]*base.TaskMessage{
				"default":  {t1, t2},
				"critical": {t3},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(-2 * time.Minute).Unix()},
					{Message: t2, Score: now.Add(20 * time.Second).Unix()},
				},
				"critical": {
					{Message: t3, Score: now.Add(20 * time.Second).Unix()},
				},
			},
			retry: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			archived: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {t2},
				"critical": {t3},
			},
			wantLease: map[string][]base.Z{
				"default":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
				"critical": {{Message: t3, Score: now.Add(20 * time.Second).Unix()}},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
		},
		{
			desc: "with multiple expired active tasks",
			active: map[string][]*base.TaskMessage{
				"default":  {t1, t2},
				"critical": {t3},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(-1 * time.Minute).Unix()},
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
				},
				"critical": {
					{Message: t3, Score: now.Add(-1 * time.Minute).Unix()},
				},
			},
			retry: map[string][]base.Z{
				"default": {},
				"cricial": {},
			},
			archived: map[string][]base.Z{
				"default": {},
				"cricial": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {t2},
				"critical": {},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t2, Score: now.Add(10 * time.Second).Unix()}},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t3},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
		},
		{
			desc: "with empty active queue",
			active: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
			lease: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			retry: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			archived: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
			wantLease: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
			wantArchived: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllLease(t, r, tc.lease)
		h.SeedAllRetryQueues(t, r, tc.retry)
		h.SeedAllArchivedQueues(t, r, tc.archived)

		recoverer := newRecoverer(recovererParams{
			logger:         testLogger,
			broker:         rdbClient,
			queues:         []string{"default", "critical"},
			interval:       1 * time.Second,
			retryDelayFunc: func(n int, err error, task *Task) time.Duration { return 30 * time.Second },
			isFailureFunc:  defaultIsFailureFunc,
		})

		var wg sync.WaitGroup
		recoverer.start(&wg)
		runTime := time.Now() // time when recoverer is running
		time.Sleep(2 * time.Second)
		recoverer.shutdown()

		for qname, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r, qname)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want,+got)\n%s", tc.desc, base.ActiveKey(qname), diff)
			}
		}
		for qname, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r, qname)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q; (-want,+got)\n%s", tc.desc, base.LeaseKey(qname), diff)
			}
		}
		cmpOpt := h.EquateInt64Approx(2) // allow up to two-second difference in `LastFailedAt`
		for qname, msgs := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r, qname)
			var wantRetry []*base.TaskMessage // Note: construct message here since `LastFailedAt` is relative to each test run
			for _, msg := range msgs {
				wantRetry = append(wantRetry, h.TaskMessageAfterRetry(*msg, ErrLeaseExpired.Error(), runTime))
			}
			if diff := cmp.Diff(wantRetry, gotRetry, h.SortMsgOpt, cmpOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got)\n%s", tc.desc, base.RetryKey(qname), diff)
			}
		}
		for qname, msgs := range tc.wantArchived {
			gotArchived := h.GetArchivedMessages(t, r, qname)
			var wantArchived []*base.TaskMessage
			for _, msg := range msgs {
				wantArchived = append(wantArchived, h.TaskMessageWithError(*msg, ErrLeaseExpired.Error(), runTime))
			}
			if diff := cmp.Diff(wantArchived, gotArchived, h.SortMsgOpt, cmpOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got)\n%s", tc.desc, base.ArchivedKey(qname), diff)
			}
		}
	}
}
