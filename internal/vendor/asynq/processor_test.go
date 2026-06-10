// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/errors"
	"github.com/hibiken/asynq/internal/log"
	"github.com/hibiken/asynq/internal/rdb"
	h "github.com/hibiken/asynq/internal/testutil"
	"github.com/hibiken/asynq/internal/timeutil"
)

var taskCmpOpts = []cmp.Option{
	sortTaskOpt,                               // sort the tasks
	cmp.AllowUnexported(Task{}),               // allow typename, payload fields to be compared
	cmpopts.IgnoreFields(Task{}, "opts", "w"), // ignore opts, w fields
}

// fakeHeartbeater receives from starting and finished channels and do nothing.
func fakeHeartbeater(starting <-chan *workerInfo, finished <-chan *base.TaskMessage, done <-chan struct{}) {
	for {
		select {
		case <-starting:
		case <-finished:
		case <-done:
			return
		}
	}
}

// fakeSyncer receives from sync channel and do nothing.
func fakeSyncer(syncCh <-chan *syncRequest, done <-chan struct{}) {
	for {
		select {
		case <-syncCh:
		case <-done:
			return
		}
	}
}

// Returns a processor instance configured for testing purpose.
func newProcessorForTest(t *testing.T, r *rdb.RDB, h Handler) *processor {
	starting := make(chan *workerInfo)
	finished := make(chan *base.TaskMessage)
	syncCh := make(chan *syncRequest)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go fakeHeartbeater(starting, finished, done)
	go fakeSyncer(syncCh, done)
	p := newProcessor(processorParams{
		logger:            testLogger,
		broker:            r,
		baseCtxFn:         context.Background,
		retryDelayFunc:    DefaultRetryDelayFunc,
		taskCheckInterval: defaultTaskCheckInterval,
		isFailureFunc:     defaultIsFailureFunc,
		syncCh:            syncCh,
		cancelations:      base.NewCancelations(),
		concurrency:       10,
		queues:            defaultQueueConfig,
		strictPriority:    false,
		errHandler:        nil,
		shutdownTimeout:   defaultShutdownTimeout,
		starting:          starting,
		finished:          finished,
	})
	p.handler = h
	return p
}

func TestProcessorSuccessWithSingleQueue(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	m1 := h.NewTaskMessage("task1", nil)
	m2 := h.NewTaskMessage("task2", nil)
	m3 := h.NewTaskMessage("task3", nil)
	m4 := h.NewTaskMessage("task4", nil)

	t1 := NewTask(m1.Type, m1.Payload)
	t2 := NewTask(m2.Type, m2.Payload)
	t3 := NewTask(m3.Type, m3.Payload)
	t4 := NewTask(m4.Type, m4.Payload)

	tests := []struct {
		pending       []*base.TaskMessage // initial default queue state
		incoming      []*base.TaskMessage // tasks to be enqueued during run
		wantProcessed []*Task             // tasks to be processed at the end
	}{
		{
			pending:       []*base.TaskMessage{m1},
			incoming:      []*base.TaskMessage{m2, m3, m4},
			wantProcessed: []*Task{t1, t2, t3, t4},
		},
		{
			pending:       []*base.TaskMessage{},
			incoming:      []*base.TaskMessage{m1},
			wantProcessed: []*Task{t1},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)                                             // clean up db before each test case.
		h.SeedPendingQueue(t, r, tc.pending, base.DefaultQueueName) // initialize default queue.

		// instantiate a new processor
		var mu sync.Mutex
		var processed []*Task
		handler := func(ctx context.Context, task *Task) error {
			mu.Lock()
			defer mu.Unlock()
			processed = append(processed, task)
			return nil
		}
		p := newProcessorForTest(t, rdbClient, HandlerFunc(handler))

		p.start(&sync.WaitGroup{})
		for _, msg := range tc.incoming {
			err := rdbClient.Enqueue(context.Background(), msg)
			if err != nil {
				p.shutdown()
				t.Fatal(err)
			}
		}
		time.Sleep(2 * time.Second) // wait for two second to allow all pending tasks to be processed.
		if l := r.LLen(context.Background(), base.ActiveKey(base.DefaultQueueName)).Val(); l != 0 {
			t.Errorf("%q has %d tasks, want 0", base.ActiveKey(base.DefaultQueueName), l)
		}
		p.shutdown()

		mu.Lock()
		if diff := cmp.Diff(tc.wantProcessed, processed, taskCmpOpts...); diff != "" {
			t.Errorf("mismatch found in processed tasks; (-want, +got)\n%s", diff)
		}
		mu.Unlock()
	}
}

func TestProcessorSuccessWithMultipleQueues(t *testing.T) {
	var (
		r         = setup(t)
		rdbClient = rdb.NewRDB(r)

		m1 = h.NewTaskMessage("task1", nil)
		m2 = h.NewTaskMessage("task2", nil)
		m3 = h.NewTaskMessageWithQueue("task3", nil, "high")
		m4 = h.NewTaskMessageWithQueue("task4", nil, "low")

		t1 = NewTask(m1.Type, m1.Payload)
		t2 = NewTask(m2.Type, m2.Payload)
		t3 = NewTask(m3.Type, m3.Payload)
		t4 = NewTask(m4.Type, m4.Payload)
	)
	defer r.Close()

	tests := []struct {
		pending       map[string][]*base.TaskMessage
		queues        []string // list of queues to consume the tasks from
		wantProcessed []*Task  // tasks to be processed at the end
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {m1, m2},
				"high":    {m3},
				"low":     {m4},
			},
			queues:        []string{"default", "high", "low"},
			wantProcessed: []*Task{t1, t2, t3, t4},
		},
	}

	for _, tc := range tests {
		// Set up test case.
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)

		// Instantiate a new processor.
		var mu sync.Mutex
		var processed []*Task
		handler := func(ctx context.Context, task *Task) error {
			mu.Lock()
			defer mu.Unlock()
			processed = append(processed, task)
			return nil
		}
		p := newProcessorForTest(t, rdbClient, HandlerFunc(handler))
		p.queueConfig = map[string]int{
			"default": 2,
			"high":    3,
			"low":     1,
		}

		p.start(&sync.WaitGroup{})
		// Wait for two second to allow all pending tasks to be processed.
		time.Sleep(2 * time.Second)
		// Make sure no messages are stuck in active list.
		for _, qname := range tc.queues {
			if l := r.LLen(context.Background(), base.ActiveKey(qname)).Val(); l != 0 {
				t.Errorf("%q has %d tasks, want 0", base.ActiveKey(qname), l)
			}
		}
		p.shutdown()

		mu.Lock()
		if diff := cmp.Diff(tc.wantProcessed, processed, taskCmpOpts...); diff != "" {
			t.Errorf("mismatch found in processed tasks; (-want, +got)\n%s", diff)
		}
		mu.Unlock()
	}
}

// https://github.com/hibiken/asynq/issues/166
func TestProcessTasksWithLargeNumberInPayload(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	m1 := h.NewTaskMessage("large_number", h.JSON(map[string]interface{}{"data": 111111111111111111}))
	t1 := NewTask(m1.Type, m1.Payload)

	tests := []struct {
		pending       []*base.TaskMessage // initial default queue state
		wantProcessed []*Task             // tasks to be processed at the end
	}{
		{
			pending:       []*base.TaskMessage{m1},
			wantProcessed: []*Task{t1},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)                                             // clean up db before each test case.
		h.SeedPendingQueue(t, r, tc.pending, base.DefaultQueueName) // initialize default queue.

		var mu sync.Mutex
		var processed []*Task
		handler := func(ctx context.Context, task *Task) error {
			mu.Lock()
			defer mu.Unlock()
			var payload map[string]int
			if err := json.Unmarshal(task.Payload(), &payload); err != nil {
				t.Errorf("coult not decode payload: %v", err)
			}
			if data, ok := payload["data"]; ok {
				t.Logf("data == %d", data)
			} else {
				t.Errorf("could not get data from payload")
			}
			processed = append(processed, task)
			return nil
		}
		p := newProcessorForTest(t, rdbClient, HandlerFunc(handler))

		p.start(&sync.WaitGroup{})
		time.Sleep(2 * time.Second) // wait for two second to allow all pending tasks to be processed.
		if l := r.LLen(context.Background(), base.ActiveKey(base.DefaultQueueName)).Val(); l != 0 {
			t.Errorf("%q has %d tasks, want 0", base.ActiveKey(base.DefaultQueueName), l)
		}
		p.shutdown()

		mu.Lock()
		if diff := cmp.Diff(tc.wantProcessed, processed, taskCmpOpts...); diff != "" {
			t.Errorf("mismatch found in processed tasks; (-want, +got)\n%s", diff)
		}
		mu.Unlock()
	}
}

func TestProcessorRetry(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	m1 := h.NewTaskMessage("send_email", nil)
	m1.Retried = m1.Retry // m1 has reached its max retry count
	m2 := h.NewTaskMessage("gen_thumbnail", nil)
	m3 := h.NewTaskMessage("reindex", nil)
	m4 := h.NewTaskMessage("sync", nil)

	errMsg := "something went wrong"
	wrappedSkipRetry := fmt.Errorf("%s:%w", errMsg, SkipRetry)
	wrappedRevokeTask := fmt.Errorf("%s:%w", errMsg, RevokeTask)

	tests := []struct {
		desc         string              // test description
		pending      []*base.TaskMessage // initial default queue state
		delay        time.Duration       // retry delay duration
		handler      Handler             // task handler
		wait         time.Duration       // wait duration between starting and stopping processor for this test case
		wantErrMsg   string              // error message the task should record
		wantRetry    []*base.TaskMessage // tasks in retry queue at the end
		wantArchived []*base.TaskMessage // tasks in archived queue at the end
		wantErrCount int                 // number of times error handler should be called
	}{
		{
			desc:    "Should automatically retry errored tasks",
			pending: []*base.TaskMessage{m1, m2, m3, m4},
			delay:   time.Minute,
			handler: HandlerFunc(func(ctx context.Context, task *Task) error {
				return errors.New(errMsg)
			}),
			wait:         2 * time.Second,
			wantErrMsg:   errMsg,
			wantRetry:    []*base.TaskMessage{m2, m3, m4},
			wantArchived: []*base.TaskMessage{m1},
			wantErrCount: 4,
		},
		{
			desc:    "Should skip retry errored tasks",
			pending: []*base.TaskMessage{m1, m2},
			delay:   time.Minute,
			handler: HandlerFunc(func(ctx context.Context, task *Task) error {
				return SkipRetry // return SkipRetry without wrapping
			}),
			wait:         2 * time.Second,
			wantErrMsg:   SkipRetry.Error(),
			wantRetry:    []*base.TaskMessage{},
			wantArchived: []*base.TaskMessage{m1, m2},
			wantErrCount: 2, // ErrorHandler should still be called with SkipRetry error
		},
		{
			desc:    "Should skip retry errored tasks (with error wrapping)",
			pending: []*base.TaskMessage{m1, m2},
			delay:   time.Minute,
			handler: HandlerFunc(func(ctx context.Context, task *Task) error {
				return wrappedSkipRetry
			}),
			wait:         2 * time.Second,
			wantErrMsg:   wrappedSkipRetry.Error(),
			wantRetry:    []*base.TaskMessage{},
			wantArchived: []*base.TaskMessage{m1, m2},
			wantErrCount: 2, // ErrorHandler should still be called with SkipRetry error
		},
		{
			desc:    "Should revoke task",
			pending: []*base.TaskMessage{m1, m2},
			delay:   time.Minute,
			handler: HandlerFunc(func(ctx context.Context, task *Task) error {
				return RevokeTask // return RevokeTask without wrapping
			}),
			wait:         2 * time.Second,
			wantErrMsg:   RevokeTask.Error(),
			wantRetry:    []*base.TaskMessage{},
			wantArchived: []*base.TaskMessage{},
			wantErrCount: 2, // ErrorHandler should still be called with RevokeTask error
		},
		{
			desc:    "Should revoke task (with error wrapping)",
			pending: []*base.TaskMessage{m1, m2},
			delay:   time.Minute,
			handler: HandlerFunc(func(ctx context.Context, task *Task) error {
				return wrappedRevokeTask
			}),
			wait:         2 * time.Second,
			wantErrMsg:   wrappedRevokeTask.Error(),
			wantRetry:    []*base.TaskMessage{},
			wantArchived: []*base.TaskMessage{},
			wantErrCount: 2, // ErrorHandler should still be called with RevokeTask error
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)                                             // clean up db before each test case.
		h.SeedPendingQueue(t, r, tc.pending, base.DefaultQueueName) // initialize default queue.

		// instantiate a new processor
		delayFunc := func(n int, e error, t *Task) time.Duration {
			return tc.delay
		}
		var (
			mu sync.Mutex // guards n
			n  int        // number of times error handler is called
		)
		errHandler := func(ctx context.Context, t *Task, err error) {
			mu.Lock()
			defer mu.Unlock()
			n++
		}
		p := newProcessorForTest(t, rdbClient, tc.handler)
		p.errHandler = ErrorHandlerFunc(errHandler)
		p.retryDelayFunc = delayFunc

		p.start(&sync.WaitGroup{})
		runTime := time.Now() // time when processor is running
		time.Sleep(tc.wait)   // FIXME: This makes test flaky.
		p.shutdown()

		cmpOpt := h.EquateInt64Approx(int64(tc.wait.Seconds())) // allow up to a wait-second difference in zset score
		gotRetry := h.GetRetryEntries(t, r, base.DefaultQueueName)
		var wantRetry []base.Z // Note: construct wantRetry here since `LastFailedAt` and ZSCORE is relative to each test run.
		for _, msg := range tc.wantRetry {
			wantRetry = append(wantRetry,
				base.Z{
					Message: h.TaskMessageAfterRetry(*msg, tc.wantErrMsg, runTime),
					Score:   runTime.Add(tc.delay).Unix(),
				})
		}
		if diff := cmp.Diff(wantRetry, gotRetry, h.SortZSetEntryOpt, cmpOpt); diff != "" {
			t.Errorf("%s: mismatch found in %q after running processor; (-want, +got)\n%s", tc.desc, base.RetryKey(base.DefaultQueueName), diff)
		}

		gotArchived := h.GetArchivedEntries(t, r, base.DefaultQueueName)
		var wantArchived []base.Z // Note: construct wantArchived here since `LastFailedAt` and ZSCORE is relative to each test run.
		for _, msg := range tc.wantArchived {
			wantArchived = append(wantArchived,
				base.Z{
					Message: h.TaskMessageWithError(*msg, tc.wantErrMsg, runTime),
					Score:   runTime.Unix(),
				})
		}
		if diff := cmp.Diff(wantArchived, gotArchived, h.SortZSetEntryOpt, cmpOpt); diff != "" {
			t.Errorf("%s: mismatch found in %q after running processor; (-want, +got)\n%s", tc.desc, base.ArchivedKey(base.DefaultQueueName), diff)
		}

		if l := r.LLen(context.Background(), base.ActiveKey(base.DefaultQueueName)).Val(); l != 0 {
			t.Errorf("%s: %q has %d tasks, want 0", base.ActiveKey(base.DefaultQueueName), tc.desc, l)
		}

		if n != tc.wantErrCount {
			t.Errorf("error handler was called %d times, want %d", n, tc.wantErrCount)
		}
	}
}

func TestProcessorMarkAsComplete(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	msg1 := h.NewTaskMessage("one", nil)
	msg2 := h.NewTaskMessage("two", nil)
	msg3 := h.NewTaskMessageWithQueue("three", nil, "custom")
	msg1.Retention = 3600
	msg3.Retention = 7200

	handler := func(ctx context.Context, task *Task) error { return nil }

	tests := []struct {
		pending       map[string][]*base.TaskMessage
		completed     map[string][]base.Z
		queueCfg      map[string]int
		wantPending   map[string][]*base.TaskMessage
		wantCompleted func(completedAt time.Time) map[string][]base.Z
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {msg1, msg2},
				"custom":  {msg3},
			},
			completed: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			queueCfg: map[string]int{
				"default": 1,
				"custom":  1,
			},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {},
			},
			wantCompleted: func(completedAt time.Time) map[string][]base.Z {
				return map[string][]base.Z{
					"default": {{Message: h.TaskMessageWithCompletedAt(*msg1, completedAt), Score: completedAt.Unix() + msg1.Retention}},
					"custom":  {{Message: h.TaskMessageWithCompletedAt(*msg3, completedAt), Score: completedAt.Unix() + msg3.Retention}},
				}
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedAllPendingQueues(t, r, tc.pending)
		h.SeedAllCompletedQueues(t, r, tc.completed)

		p := newProcessorForTest(t, rdbClient, HandlerFunc(handler))
		p.queueConfig = tc.queueCfg

		p.start(&sync.WaitGroup{})
		runTime := time.Now() // time when processor is running
		time.Sleep(2 * time.Second)
		p.shutdown()

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r, qname)
			if diff := cmp.Diff(want, gotPending, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("diff found in %q pending set; want=%v, got=%v\n%s", qname, want, gotPending, diff)
			}
		}

		for qname, want := range tc.wantCompleted(runTime) {
			gotCompleted := h.GetCompletedEntries(t, r, qname)
			if diff := cmp.Diff(want, gotCompleted, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("diff found in %q completed set; want=%v, got=%v\n%s", qname, want, gotCompleted, diff)
			}
		}
	}
}

// Test a scenario where the worker server cannot communicate with redis due to a network failure
// and the lease expires
func TestProcessorWithExpiredLease(t *testing.T) {
	r := setup(t)
	defer r.Close()
	rdbClient := rdb.NewRDB(r)

	m1 := h.NewTaskMessage("task1", nil)

	tests := []struct {
		pending      []*base.TaskMessage
		handler      Handler
		wantErrCount int
	}{
		{
			pending: []*base.TaskMessage{m1},
			handler: HandlerFunc(func(ctx context.Context, task *Task) error {
				// make sure the task processing time exceeds lease duration
				// to test expired lease.
				time.Sleep(rdb.LeaseDuration + 10*time.Second)
				return nil
			}),
			wantErrCount: 1, // ErrorHandler should still be called with ErrLeaseExpired
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r)
		h.SeedPendingQueue(t, r, tc.pending, base.DefaultQueueName)

		starting := make(chan *workerInfo)
		finished := make(chan *base.TaskMessage)
		syncCh := make(chan *syncRequest)
		done := make(chan struct{})
		t.Cleanup(func() { close(done) })
		// fake heartbeater which notifies lease expiration
		go func() {
			for {
				select {
				case w := <-starting:
					// simulate expiration by resetting to some time in the past
					w.lease.Reset(time.Now().Add(-5 * time.Second))
					if !w.lease.NotifyExpiration() {
						panic("Failed to notifiy lease expiration")
					}
				case <-finished:
					// do nothing
				case <-done:
					return
				}
			}
		}()
		go fakeSyncer(syncCh, done)
		p := newProcessor(processorParams{
			logger:            testLogger,
			broker:            rdbClient,
			baseCtxFn:         context.Background,
			taskCheckInterval: defaultTaskCheckInterval,
			retryDelayFunc:    DefaultRetryDelayFunc,
			isFailureFunc:     defaultIsFailureFunc,
			syncCh:            syncCh,
			cancelations:      base.NewCancelations(),
			concurrency:       10,
			queues:            defaultQueueConfig,
			strictPriority:    false,
			errHandler:        nil,
			shutdownTimeout:   defaultShutdownTimeout,
			starting:          starting,
			finished:          finished,
		})
		p.handler = tc.handler
		var (
			mu   sync.Mutex // guards n and errs
			n    int        // number of times error handler is called
			errs []error    // error passed to error handler
		)
		p.errHandler = ErrorHandlerFunc(func(ctx context.Context, t *Task, err error) {
			mu.Lock()
			defer mu.Unlock()
			n++
			errs = append(errs, err)
		})

		p.start(&sync.WaitGroup{})
		time.Sleep(4 * time.Second)
		p.shutdown()

		if n != tc.wantErrCount {
			t.Errorf("Unexpected number of error count: got %d, want %d", n, tc.wantErrCount)
			continue
		}
		for i := 0; i < tc.wantErrCount; i++ {
			if !errors.Is(errs[i], ErrLeaseExpired) {
				t.Errorf("Unexpected error was passed to ErrorHandler: got %v want %v", errs[i], ErrLeaseExpired)
			}
		}
	}
}

func TestProcessorQueues(t *testing.T) {
	sortOpt := cmp.Transformer("SortStrings", func(in []string) []string {
		out := append([]string(nil), in...) // Copy input to avoid mutating it
		sort.Strings(out)
		return out
	})

	tests := []struct {
		queueCfg map[string]int
		want     []string
	}{
		{
			queueCfg: map[string]int{
				"high":    6,
				"default": 3,
				"low":     1,
			},
			want: []string{"high", "default", "low"},
		},
		{
			queueCfg: map[string]int{
				"default": 1,
			},
			want: []string{"default"},
		},
	}

	for _, tc := range tests {
		// Note: rdb and handler not needed for this test.
		p := newProcessorForTest(t, nil, nil)
		p.queueConfig = tc.queueCfg

		got := p.queues()
		if diff := cmp.Diff(tc.want, got, sortOpt); diff != "" {
			t.Errorf("with queue config: %v\n(*processor).queues() = %v, want %v\n(-want,+got):\n%s",
				tc.queueCfg, got, tc.want, diff)
		}
	}
}

func TestProcessorWithStrictPriority(t *testing.T) {
	var (
		r = setup(t)

		rdbClient = rdb.NewRDB(r)

		m1 = h.NewTaskMessageWithQueue("task1", nil, "critical")
		m2 = h.NewTaskMessageWithQueue("task2", nil, "critical")
		m3 = h.NewTaskMessageWithQueue("task3", nil, "critical")
		m4 = h.NewTaskMessageWithQueue("task4", nil, base.DefaultQueueName)
		m5 = h.NewTaskMessageWithQueue("task5", nil, base.DefaultQueueName)
		m6 = h.NewTaskMessageWithQueue("task6", nil, "low")
		m7 = h.NewTaskMessageWithQueue("task7", nil, "low")

		t1 = NewTask(m1.Type, m1.Payload)
		t2 = NewTask(m2.Type, m2.Payload)
		t3 = NewTask(m3.Type, m3.Payload)
		t4 = NewTask(m4.Type, m4.Payload)
		t5 = NewTask(m5.Type, m5.Payload)
		t6 = NewTask(m6.Type, m6.Payload)
		t7 = NewTask(m7.Type, m7.Payload)
	)
	defer r.Close()

	tests := []struct {
		pending       map[string][]*base.TaskMessage // initial queues state
		queues        []string                       // list of queues to consume tasks from
		wait          time.Duration                  // wait duration between starting and stopping processor for this test case
		wantProcessed []*Task                        // tasks to be processed at the end
	}{
		{
			pending: map[string][]*base.TaskMessage{
				base.DefaultQueueName: {m4, m5},
				"critical":            {m1, m2, m3},
				"low":                 {m6, m7},
			},
			queues:        []string{base.DefaultQueueName, "critical", "low"},
			wait:          time.Second,
			wantProcessed: []*Task{t1, t2, t3, t4, t5, t6, t7},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r) // clean up db before each test case.
		for qname, msgs := range tc.pending {
			h.SeedPendingQueue(t, r, msgs, qname)
		}

		// instantiate a new processor
		var mu sync.Mutex
		var processed []*Task
		handler := func(ctx context.Context, task *Task) error {
			mu.Lock()
			defer mu.Unlock()
			processed = append(processed, task)
			return nil
		}
		queueCfg := map[string]int{
			base.DefaultQueueName: 2,
			"critical":            3,
			"low":                 1,
		}
		starting := make(chan *workerInfo)
		finished := make(chan *base.TaskMessage)
		syncCh := make(chan *syncRequest)
		done := make(chan struct{})
		defer func() { close(done) }()
		go fakeHeartbeater(starting, finished, done)
		go fakeSyncer(syncCh, done)
		p := newProcessor(processorParams{
			logger:            testLogger,
			broker:            rdbClient,
			baseCtxFn:         context.Background,
			taskCheckInterval: defaultTaskCheckInterval,
			retryDelayFunc:    DefaultRetryDelayFunc,
			isFailureFunc:     defaultIsFailureFunc,
			syncCh:            syncCh,
			cancelations:      base.NewCancelations(),
			concurrency:       1, // Set concurrency to 1 to make sure tasks are processed one at a time.
			queues:            queueCfg,
			strictPriority:    true,
			errHandler:        nil,
			shutdownTimeout:   defaultShutdownTimeout,
			starting:          starting,
			finished:          finished,
		})
		p.handler = HandlerFunc(handler)

		p.start(&sync.WaitGroup{})
		time.Sleep(tc.wait)
		// Make sure no tasks are stuck in active list.
		for _, qname := range tc.queues {
			if l := r.LLen(context.Background(), base.ActiveKey(qname)).Val(); l != 0 {
				t.Errorf("%q has %d tasks, want 0", base.ActiveKey(qname), l)
			}
		}
		p.shutdown()

		if diff := cmp.Diff(tc.wantProcessed, processed, taskCmpOpts...); diff != "" {
			t.Errorf("mismatch found in processed tasks; (-want, +got)\n%s", diff)
		}

	}
}

func TestProcessorPerform(t *testing.T) {
	tests := []struct {
		desc    string
		handler HandlerFunc
		task    *Task
		wantErr bool
	}{
		{
			desc: "handler returns nil",
			handler: func(ctx context.Context, t *Task) error {
				return nil
			},
			task:    NewTask("gen_thumbnail", h.JSON(map[string]interface{}{"src": "some/img/path"})),
			wantErr: false,
		},
		{
			desc: "handler returns error",
			handler: func(ctx context.Context, t *Task) error {
				return fmt.Errorf("something went wrong")
			},
			task:    NewTask("gen_thumbnail", h.JSON(map[string]interface{}{"src": "some/img/path"})),
			wantErr: true,
		},
		{
			desc: "handler panics",
			handler: func(ctx context.Context, t *Task) error {
				panic("something went terribly wrong")
			},
			task:    NewTask("gen_thumbnail", h.JSON(map[string]interface{}{"src": "some/img/path"})),
			wantErr: true,
		},
	}
	// Note: We don't need to fully initialized the processor since we are only testing
	// perform method.
	p := newProcessorForTest(t, nil, nil)

	for _, tc := range tests {
		p.handler = tc.handler
		got := p.perform(context.Background(), tc.task)
		if !tc.wantErr && got != nil {
			t.Errorf("%s: perform() = %v, want nil", tc.desc, got)
			continue
		}
		if tc.wantErr && got == nil {
			t.Errorf("%s: perform() = nil, want non-nil error", tc.desc)
			continue
		}
	}
}

func TestGCD(t *testing.T) {
	tests := []struct {
		input []int
		want  int
	}{
		{[]int{6, 2, 12}, 2},
		{[]int{3, 3, 3}, 3},
		{[]int{6, 3, 1}, 1},
		{[]int{1}, 1},
		{[]int{1, 0, 2}, 1},
		{[]int{8, 0, 4}, 4},
		{[]int{9, 12, 18, 30}, 3},
	}

	for _, tc := range tests {
		got := gcd(tc.input...)
		if got != tc.want {
			t.Errorf("gcd(%v) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeQueues(t *testing.T) {
	tests := []struct {
		input map[string]int
		want  map[string]int
	}{
		{
			input: map[string]int{
				"high":    100,
				"default": 20,
				"low":     5,
			},
			want: map[string]int{
				"high":    20,
				"default": 4,
				"low":     1,
			},
		},
		{
			input: map[string]int{
				"default": 10,
			},
			want: map[string]int{
				"default": 1,
			},
		},
		{
			input: map[string]int{
				"critical": 5,
				"default":  1,
			},
			want: map[string]int{
				"critical": 5,
				"default":  1,
			},
		},
		{
			input: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      0,
			},
			want: map[string]int{
				"critical": 2,
				"default":  1,
				"low":      0,
			},
		},
	}

	for _, tc := range tests {
		got := normalizeQueues(tc.input)
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("normalizeQueues(%v) = %v, want %v; (-want, +got):\n%s",
				tc.input, got, tc.want, diff)
		}
	}
}

func TestProcessorComputeDeadline(t *testing.T) {
	now := time.Now()
	p := processor{
		logger: log.NewLogger(nil),
		clock:  timeutil.NewSimulatedClock(now),
	}

	tests := []struct {
		desc string
		msg  *base.TaskMessage
		want time.Time
	}{
		{
			desc: "message with only timeout specified",
			msg: &base.TaskMessage{
				Timeout: int64((30 * time.Minute).Seconds()),
			},
			want: now.Add(30 * time.Minute),
		},
		{
			desc: "message with only deadline specified",
			msg: &base.TaskMessage{
				Deadline: now.Add(24 * time.Hour).Unix(),
			},
			want: now.Add(24 * time.Hour),
		},
		{
			desc: "message with both timeout and deadline set (now+timeout < deadline)",
			msg: &base.TaskMessage{
				Deadline: now.Add(24 * time.Hour).Unix(),
				Timeout:  int64((30 * time.Minute).Seconds()),
			},
			want: now.Add(30 * time.Minute),
		},
		{
			desc: "message with both timeout and deadline set (now+timeout > deadline)",
			msg: &base.TaskMessage{
				Deadline: now.Add(10 * time.Minute).Unix(),
				Timeout:  int64((30 * time.Minute).Seconds()),
			},
			want: now.Add(10 * time.Minute),
		},
		{
			desc: "message with both timeout and deadline set (now+timeout == deadline)",
			msg: &base.TaskMessage{
				Deadline: now.Add(30 * time.Minute).Unix(),
				Timeout:  int64((30 * time.Minute).Seconds()),
			},
			want: now.Add(30 * time.Minute),
		},
		{
			desc: "message without timeout and deadline",
			msg:  &base.TaskMessage{},
			want: now.Add(defaultTimeout),
		},
	}

	for _, tc := range tests {
		got := p.computeDeadline(tc.msg)
		// Compare the Unix epoch with seconds granularity
		if got.Unix() != tc.want.Unix() {
			t.Errorf("%s: got=%v, want=%v", tc.desc, got.Unix(), tc.want.Unix())
		}
	}
}

func TestReturnPanicError(t *testing.T) {

	task := NewTask("gen_thumbnail", h.JSON(map[string]interface{}{"src": "some/img/path"}))

	tests := []struct {
		name         string
		handler      HandlerFunc
		IsPanicError bool
	}{
		{
			name: "should return panic error when occurred panic recovery",
			handler: func(ctx context.Context, t *Task) error {
				panic("something went terribly wrong")
			},
			IsPanicError: true,
		},
		{
			name: "should return normal error when don't occur panic recovery",
			handler: func(ctx context.Context, t *Task) error {
				return fmt.Errorf("something went terribly wrong")
			},
			IsPanicError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := processor{
				logger:  log.NewLogger(nil),
				handler: tc.handler,
			}
			got := p.perform(context.Background(), task)
			if tc.IsPanicError != IsPanicError(got) {
				t.Errorf("%s: got=%t, want=%t", tc.name, IsPanicError(got), tc.IsPanicError)
			}
			if tc.IsPanicError && !strings.HasPrefix(got.Error(), "panic error cause by:") {
				t.Error("wrong text msg for panic error")
			}
		})
	}
}
