// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/rdb"
	"github.com/hibiken/asynq/internal/testbroker"
	h "github.com/hibiken/asynq/internal/testutil"
	"github.com/hibiken/asynq/internal/timeutil"
)

// Test goes through a few phases.
//
// Phase1: Simulate Server startup; Simulate starting tasks listed in startedWorkers
// Phase2: Simulate finishing tasks listed in finishedTasks
// Phase3: Simulate Server shutdown;
func TestHeartbeater(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	now := time.Now()
	const elapsedTime = 10 * time.Second // simulated time elapsed between phase1 and phase2

	clock := timeutil.NewSimulatedClock(time.Time{}) // time will be set in each test

	t1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	t2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	t3 := h.NewTaskMessageWithQueue("task3", nil, "default")
	t4 := h.NewTaskMessageWithQueue("task4", nil, "custom")
	t5 := h.NewTaskMessageWithQueue("task5", nil, "custom")
	t6 := h.NewTaskMessageWithQueue("task6", nil, "default")

	// Note: intentionally set to time less than now.Add(rdb.LeaseDuration) to test lease extension is working.
	lease1 := h.NewLeaseWithClock(now.Add(10*time.Second), clock)
	lease2 := h.NewLeaseWithClock(now.Add(10*time.Second), clock)
	lease3 := h.NewLeaseWithClock(now.Add(10*time.Second), clock)
	lease4 := h.NewLeaseWithClock(now.Add(10*time.Second), clock)
	lease5 := h.NewLeaseWithClock(now.Add(10*time.Second), clock)
	lease6 := h.NewLeaseWithClock(now.Add(10*time.Second), clock)

	tests := []struct {
		desc string

		// Interval between heartbeats.
		interval time.Duration

		// Server info.
		host        string
		pid         int
		queues      map[string]int
		concurrency int

		active         map[string][]*base.TaskMessage // initial active set state
		lease          map[string][]base.Z            // initial lease set state
		wantLease1     map[string][]base.Z            // expected lease set state after starting all startedWorkers
		wantLease2     map[string][]base.Z            // expected lease set state after finishing all finishedTasks
		startedWorkers []*workerInfo                  // workerInfo to send via the started channel
		finishedTasks  []*base.TaskMessage            // tasks to send via the finished channel

		startTime   time.Time     // simulated start time
		elapsedTime time.Duration // simulated time elapsed between starting and finishing processing tasks
	}{
		{
			desc:        "With single queue",
			interval:    2 * time.Second,
			host:        "localhost",
			pid:         45678,
			queues:      map[string]int{"default": 1},
			concurrency: 10,
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
					{Message: t3, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			startedWorkers: []*workerInfo{
				{msg: t1, started: now, deadline: now.Add(2 * time.Minute), lease: lease1},
				{msg: t2, started: now, deadline: now.Add(2 * time.Minute), lease: lease2},
				{msg: t3, started: now, deadline: now.Add(2 * time.Minute), lease: lease3},
			},
			finishedTasks: []*base.TaskMessage{t1, t2},
			wantLease1: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(rdb.LeaseDuration).Unix()},
					{Message: t2, Score: now.Add(rdb.LeaseDuration).Unix()},
					{Message: t3, Score: now.Add(rdb.LeaseDuration).Unix()},
				},
			},
			wantLease2: map[string][]base.Z{
				"default": {
					{Message: t3, Score: now.Add(elapsedTime).Add(rdb.LeaseDuration).Unix()},
				},
			},
			startTime:   now,
			elapsedTime: elapsedTime,
		},
		{
			desc:        "With multiple queue",
			interval:    2 * time.Second,
			host:        "localhost",
			pid:         45678,
			queues:      map[string]int{"default": 1, "custom": 2},
			concurrency: 10,
			active: map[string][]*base.TaskMessage{
				"default": {t6},
				"custom":  {t4, t5},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t6, Score: now.Add(10 * time.Second).Unix()},
				},
				"custom": {
					{Message: t4, Score: now.Add(10 * time.Second).Unix()},
					{Message: t5, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			startedWorkers: []*workerInfo{
				{msg: t6, started: now, deadline: now.Add(2 * time.Minute), lease: lease6},
				{msg: t4, started: now, deadline: now.Add(2 * time.Minute), lease: lease4},
				{msg: t5, started: now, deadline: now.Add(2 * time.Minute), lease: lease5},
			},
			finishedTasks: []*base.TaskMessage{t6, t5},
			wantLease1: map[string][]base.Z{
				"default": {
					{Message: t6, Score: now.Add(rdb.LeaseDuration).Unix()},
				},
				"custom": {
					{Message: t4, Score: now.Add(rdb.LeaseDuration).Unix()},
					{Message: t5, Score: now.Add(rdb.LeaseDuration).Unix()},
				},
			},
			wantLease2: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: t4, Score: now.Add(elapsedTime).Add(rdb.LeaseDuration).Unix()},
				},
			},
			startTime:   now,
			elapsedTime: elapsedTime,
		},
	}

	timeCmpOpt := cmpopts.EquateApproxTime(10 * time.Millisecond)
	ignoreOpt := cmpopts.IgnoreUnexported(base.ServerInfo{})
	ignoreFieldOpt := cmpopts.IgnoreFields(base.ServerInfo{}, "ServerID")
	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllActiveQueues(t, r, tc.active)
		h.SeedAllLease(t, r, tc.lease)

		clock.SetTime(tc.startTime)
		rdbClient.SetClock(clock)

		srvState := &serverState{}
		startingCh := make(chan *workerInfo)
		finishedCh := make(chan *base.TaskMessage)
		hb := newHeartbeater(heartbeaterParams{
			logger:         testLogger,
			broker:         rdbClient,
			interval:       tc.interval,
			concurrency:    tc.concurrency,
			queues:         tc.queues,
			strictPriority: false,
			state:          srvState,
			starting:       startingCh,
			finished:       finishedCh,
		})
		hb.clock = clock

		// Change host and pid fields for testing purpose.
		hb.host = tc.host
		hb.pid = tc.pid

		//===================
		// Start Phase1
		//===================

		srvState.mu.Lock()
		srvState.value = srvStateActive // simulating Server.Start
		srvState.mu.Unlock()

		var wg sync.WaitGroup
		hb.start(&wg)

		// Simulate processor starting to work on tasks.
		for _, w := range tc.startedWorkers {
			startingCh <- w
		}

		// Wait for heartbeater to write to redis
		time.Sleep(tc.interval * 2)

		ss, err := rdbClient.ListServers()
		if err != nil {
			t.Errorf("%s: could not read server info from redis: %v", tc.desc, err)
			hb.shutdown()
			continue
		}

		if len(ss) != 1 {
			t.Errorf("%s: (*RDB).ListServers returned %d server info, want 1", tc.desc, len(ss))
			hb.shutdown()
			continue
		}

		wantInfo := &base.ServerInfo{
			Host:              tc.host,
			PID:               tc.pid,
			Queues:            tc.queues,
			Concurrency:       tc.concurrency,
			Started:           now,
			Status:            "active",
			ActiveWorkerCount: len(tc.startedWorkers),
		}
		if diff := cmp.Diff(wantInfo, ss[0], timeCmpOpt, ignoreOpt, ignoreFieldOpt); diff != "" {
			t.Errorf("%s: redis stored server status %+v, want %+v; (-want, +got)\n%s", tc.desc, ss[0], wantInfo, diff)
			hb.shutdown()
			continue
		}

		for qname, wantLease := range tc.wantLease1 {
			gotLease := h.GetLeaseEntries(t, r, qname)
			if diff := cmp.Diff(wantLease, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s: mismatch found in %q: (-want,+got):\n%s", tc.desc, base.LeaseKey(qname), diff)
			}
		}

		for _, w := range tc.startedWorkers {
			if want := now.Add(rdb.LeaseDuration); w.lease.Deadline() != want {
				t.Errorf("%s: lease deadline for %v is set to %v, want %v", tc.desc, w.msg, w.lease.Deadline(), want)
			}
		}

		//===================
		// Start Phase2
		//===================

		clock.AdvanceTime(tc.elapsedTime)
		// Simulate processor finished processing tasks.
		for _, msg := range tc.finishedTasks {
			if err := rdbClient.Done(context.Background(), msg); err != nil {
				t.Fatalf("RDB.Done failed: %v", err)
			}
			finishedCh <- msg
		}
		// Wait for heartbeater to write to redis
		time.Sleep(tc.interval * 2)

		for qname, wantLease := range tc.wantLease2 {
			gotLease := h.GetLeaseEntries(t, r, qname)
			if diff := cmp.Diff(wantLease, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s: mismatch found in %q: (-want,+got):\n%s", tc.desc, base.LeaseKey(qname), diff)
			}
		}

		//===================
		// Start Phase3
		//===================

		// Server state change; simulating Server.Shutdown
		srvState.mu.Lock()
		srvState.value = srvStateClosed
		srvState.mu.Unlock()

		// Wait for heartbeater to write to redis
		time.Sleep(tc.interval * 2)

		wantInfo = &base.ServerInfo{
			Host:              tc.host,
			PID:               tc.pid,
			Queues:            tc.queues,
			Concurrency:       tc.concurrency,
			Started:           now,
			Status:            "closed",
			ActiveWorkerCount: len(tc.startedWorkers) - len(tc.finishedTasks),
		}
		ss, err = rdbClient.ListServers()
		if err != nil {
			t.Errorf("%s: could not read server status from redis: %v", tc.desc, err)
			hb.shutdown()
			continue
		}

		if len(ss) != 1 {
			t.Errorf("%s: (*RDB).ListServers returned %d server info, want 1", tc.desc, len(ss))
			hb.shutdown()
			continue
		}

		if diff := cmp.Diff(wantInfo, ss[0], timeCmpOpt, ignoreOpt, ignoreFieldOpt); diff != "" {
			t.Errorf("%s: redis stored process status %+v, want %+v; (-want, +got)\n%s", tc.desc, ss[0], wantInfo, diff)
			hb.shutdown()
			continue
		}

		hb.shutdown()
	}
}

func TestHeartbeaterWithRedisDown(t *testing.T) {
	// Make sure that heartbeater goroutine doesn't panic
	// if it cannot connect to redis.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic occurred: %v", r)
		}
	}()
	r := rdb.NewRDB(setup(t))
	defer r.Close()
	testBroker := testbroker.NewTestBroker(r)
	state := &serverState{value: srvStateActive}
	hb := newHeartbeater(heartbeaterParams{
		logger:         testLogger,
		broker:         testBroker,
		interval:       time.Second,
		concurrency:    10,
		queues:         map[string]int{"default": 1},
		strictPriority: false,
		state:          state,
		starting:       make(chan *workerInfo),
		finished:       make(chan *base.TaskMessage),
	})

	testBroker.Sleep()
	var wg sync.WaitGroup
	hb.start(&wg)

	// wait for heartbeater to try writing data to redis
	time.Sleep(2 * time.Second)

	hb.shutdown()
}
