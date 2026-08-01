// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package rdb

import (
	"context"
	"encoding/json"
	"flag"
	"math"
	"strconv"
	"strings"
	"sync"
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

// variables used for package testing.
var (
	redisAddr string
	redisDB   int

	useRedisCluster   bool
	redisClusterAddrs string // comma-separated list of host:port
)

func init() {
	flag.StringVar(&redisAddr, "redis_addr", "localhost:6379", "redis address to use in testing")
	flag.IntVar(&redisDB, "redis_db", 15, "redis db number to use in testing")
	flag.BoolVar(&useRedisCluster, "redis_cluster", false, "use redis cluster as a broker in testing")
	flag.StringVar(&redisClusterAddrs, "redis_cluster_addrs", "localhost:7000,localhost:7001,localhost:7002", "comma separated list of redis server addresses")
}

func setup(tb testing.TB) (r *RDB) {
	tb.Helper()
	if useRedisCluster {
		addrs := strings.Split(redisClusterAddrs, ",")
		if len(addrs) == 0 {
			tb.Fatal("No redis cluster addresses provided. Please set addresses using --redis_cluster_addrs flag.")
		}
		r = NewRDB(redis.NewClusterClient(&redis.ClusterOptions{
			Addrs: addrs,
		}))
	} else {
		r = NewRDB(redis.NewClient(&redis.Options{
			Addr: redisAddr,
			DB:   redisDB,
		}))
	}
	// Start each test with a clean slate.
	h.FlushDB(tb, r.client)
	return r
}

func TestEnqueue(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", h.JSON(map[string]interface{}{"to": "exampleuser@gmail.com", "from": "noreply@example.com"}))
	t2 := h.NewTaskMessageWithQueue("generate_csv", h.JSON(map[string]interface{}{}), "csv")
	t3 := h.NewTaskMessageWithQueue("sync", nil, "low")

	enqueueTime := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(enqueueTime))

	tests := []struct {
		msg *base.TaskMessage
	}{
		{t1},
		{t2},
		{t3},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		err := r.Enqueue(context.Background(), tc.msg)
		if err != nil {
			t.Errorf("(*RDB).Enqueue(msg) = %v, want nil", err)
			continue
		}

		// Check Pending list has task ID.
		pendingKey := base.PendingKey(tc.msg.Queue)
		pendingIDs := r.client.LRange(context.Background(), pendingKey, 0, -1).Val()
		if n := len(pendingIDs); n != 1 {
			t.Errorf("Redis LIST %q contains %d IDs, want 1", pendingKey, n)
			continue
		}
		if pendingIDs[0] != tc.msg.ID {
			t.Errorf("Redis LIST %q: got %v, want %v", pendingKey, pendingIDs, []string{tc.msg.ID})
			continue
		}

		// Check the value under the task key.
		taskKey := base.TaskKey(tc.msg.Queue, tc.msg.ID)
		encoded := r.client.HGet(context.Background(), taskKey, "msg").Val() // "msg" field
		decoded := h.MustUnmarshal(t, encoded)
		if diff := cmp.Diff(tc.msg, decoded); diff != "" {
			t.Errorf("persisted message was %v, want %v; (-want, +got)\n%s", decoded, tc.msg, diff)
		}
		state := r.client.HGet(context.Background(), taskKey, "state").Val() // "state" field
		if state != "pending" {
			t.Errorf("state field under task-key is set to %q, want %q", state, "pending")
		}
		pendingSince := r.client.HGet(context.Background(), taskKey, "pending_since").Val() // "pending_since" field
		if want := strconv.Itoa(int(enqueueTime.UnixNano())); pendingSince != want {
			t.Errorf("pending_since field under task-key is set to %v, want %v", pendingSince, want)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}
	}
}

func TestEnqueueTaskIdConflictError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := base.TaskMessage{
		ID:      "custom_id",
		Type:    "foo",
		Payload: nil,
	}
	m2 := base.TaskMessage{
		ID:      "custom_id",
		Type:    "bar",
		Payload: nil,
	}

	tests := []struct {
		firstMsg  *base.TaskMessage
		secondMsg *base.TaskMessage
	}{
		{firstMsg: &m1, secondMsg: &m2},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		if err := r.Enqueue(context.Background(), tc.firstMsg); err != nil {
			t.Errorf("First message: Enqueue failed: %v", err)
			continue
		}
		if err := r.Enqueue(context.Background(), tc.secondMsg); !errors.Is(err, errors.ErrTaskIdConflict) {
			t.Errorf("Second message: Enqueue returned %v, want %v", err, errors.ErrTaskIdConflict)
			continue
		}
	}
}

func TestEnqueueQueueCache(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessageWithQueue("sync1", nil, "low")

	enqueueTime := time.Now()
	clock := timeutil.NewSimulatedClock(enqueueTime)
	r.SetClock(clock)

	err := r.Enqueue(context.Background(), t1)
	if err != nil {
		t.Fatalf("(*RDB).Enqueue(msg) = %v, want nil", err)
	}

	// Check queue is in the AllQueues set.
	if !r.client.SIsMember(context.Background(), base.AllQueues, t1.Queue).Val() {
		t.Fatalf("%q is not a member of SET %q", t1.Queue, base.AllQueues)
	}

	if _, ok := r.queuesPublished.Load(t1.Queue); !ok {
		t.Fatalf("%q is not cached in queuesPublished", t1.Queue)
	}

	t.Run("remove-queue", func(t *testing.T) {
		err := r.RemoveQueue(t1.Queue, true)
		if err != nil {
			t.Errorf("(*RDB).RemoveQueue(%q, %t) = %v, want nil", t1.Queue, true, err)
		}

		if _, ok := r.queuesPublished.Load(t1.Queue); ok {
			t.Fatalf("%q is still cached in queuesPublished", t1.Queue)
		}

		if r.client.SIsMember(context.Background(), base.AllQueues, t1.Queue).Val() {
			t.Fatalf("%q is a member of SET %q", t1.Queue, base.AllQueues)
		}

		err = r.Enqueue(context.Background(), t1)
		if err != nil {
			t.Fatalf("(*RDB).Enqueue(msg) = %v, want nil", err)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, t1.Queue).Val() {
			t.Fatalf("%q is not a member of SET %q", t1.Queue, base.AllQueues)
		}

		if _, ok := r.queuesPublished.Load(t1.Queue); !ok {
			t.Fatalf("%q is not cached in queuesPublished", t1.Queue)
		}
	})
}

func TestEnqueueUnique(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "email",
		Payload:   h.JSON(map[string]interface{}{"user_id": json.Number("123")}),
		Queue:     base.DefaultQueueName,
		UniqueKey: base.UniqueKey(base.DefaultQueueName, "email", h.JSON(map[string]interface{}{"user_id": 123})),
	}

	enqueueTime := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(enqueueTime))

	tests := []struct {
		msg *base.TaskMessage
		ttl time.Duration // uniqueness ttl
	}{
		{&m1, time.Minute},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		// Enqueue the first message, should succeed.
		err := r.EnqueueUnique(context.Background(), tc.msg, tc.ttl)
		if err != nil {
			t.Errorf("First message: (*RDB).EnqueueUnique(%v, %v) = %v, want nil",
				tc.msg, tc.ttl, err)
			continue
		}
		gotPending := h.GetPendingMessages(t, r.client, tc.msg.Queue)
		if len(gotPending) != 1 {
			t.Errorf("%q has length %d, want 1", base.PendingKey(tc.msg.Queue), len(gotPending))
			continue
		}
		if diff := cmp.Diff(tc.msg, gotPending[0]); diff != "" {
			t.Errorf("persisted data differed from the original input (-want, +got)\n%s", diff)
		}
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}

		// Check Pending list has task ID.
		pendingKey := base.PendingKey(tc.msg.Queue)
		pendingIDs := r.client.LRange(context.Background(), pendingKey, 0, -1).Val()
		if len(pendingIDs) != 1 {
			t.Errorf("Redis LIST %q contains %d IDs, want 1", pendingKey, len(pendingIDs))
			continue
		}
		if pendingIDs[0] != tc.msg.ID {
			t.Errorf("Redis LIST %q: got %v, want %v", pendingKey, pendingIDs, []string{tc.msg.ID})
			continue
		}

		// Check the value under the task key.
		taskKey := base.TaskKey(tc.msg.Queue, tc.msg.ID)
		encoded := r.client.HGet(context.Background(), taskKey, "msg").Val() // "msg" field
		decoded := h.MustUnmarshal(t, encoded)
		if diff := cmp.Diff(tc.msg, decoded); diff != "" {
			t.Errorf("persisted message was %v, want %v; (-want, +got)\n%s", decoded, tc.msg, diff)
		}
		state := r.client.HGet(context.Background(), taskKey, "state").Val() // "state" field
		if state != "pending" {
			t.Errorf("state field under task-key is set to %q, want %q", state, "pending")
		}
		pendingSince := r.client.HGet(context.Background(), taskKey, "pending_since").Val() // "pending_since" field
		if want := strconv.Itoa(int(enqueueTime.UnixNano())); pendingSince != want {
			t.Errorf("pending_since field under task-key is set to %v, want %v", pendingSince, want)
		}
		uniqueKey := r.client.HGet(context.Background(), taskKey, "unique_key").Val() // "unique_key" field
		if uniqueKey != tc.msg.UniqueKey {
			t.Errorf("uniqueue_key field under task key is set to %q, want %q", uniqueKey, tc.msg.UniqueKey)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}

		// Enqueue the second message, should fail.
		got := r.EnqueueUnique(context.Background(), tc.msg, tc.ttl)
		if !errors.Is(got, errors.ErrDuplicateTask) {
			t.Errorf("Second message: (*RDB).EnqueueUnique(msg, ttl) = %v, want %v", got, errors.ErrDuplicateTask)
			continue
		}
		gotTTL := r.client.TTL(context.Background(), tc.msg.UniqueKey).Val()
		if !cmp.Equal(tc.ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 2)) {
			t.Errorf("TTL %q = %v, want %v", tc.msg.UniqueKey, gotTTL, tc.ttl)
			continue
		}
	}
}

func TestEnqueueUniqueTaskIdConflictError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "foo",
		Payload:   nil,
		UniqueKey: "unique_key_one",
	}
	m2 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "bar",
		Payload:   nil,
		UniqueKey: "unique_key_two",
	}
	const ttl = 30 * time.Second

	tests := []struct {
		firstMsg  *base.TaskMessage
		secondMsg *base.TaskMessage
	}{
		{firstMsg: &m1, secondMsg: &m2},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		if err := r.EnqueueUnique(context.Background(), tc.firstMsg, ttl); err != nil {
			t.Errorf("First message: EnqueueUnique failed: %v", err)
			continue
		}
		if err := r.EnqueueUnique(context.Background(), tc.secondMsg, ttl); !errors.Is(err, errors.ErrTaskIdConflict) {
			t.Errorf("Second message: EnqueueUnique returned %v, want %v", err, errors.ErrTaskIdConflict)
			continue
		}
	}
}

func TestDequeue(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	t1 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "send_email",
		Payload:  h.JSON(map[string]interface{}{"subject": "hello!"}),
		Queue:    "default",
		Timeout:  1800,
		Deadline: 0,
	}
	t2 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "export_csv",
		Payload:  nil,
		Queue:    "critical",
		Timeout:  0,
		Deadline: 1593021600,
	}
	t3 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "reindex",
		Payload:  nil,
		Queue:    "low",
		Timeout:  int64((5 * time.Minute).Seconds()),
		Deadline: time.Now().Add(10 * time.Minute).Unix(),
	}

	tests := []struct {
		pending            map[string][]*base.TaskMessage
		qnames             []string // list of queues to query
		wantMsg            *base.TaskMessage
		wantExpirationTime time.Time
		wantPending        map[string][]*base.TaskMessage
		wantActive         map[string][]*base.TaskMessage
		wantLease          map[string][]base.Z
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			qnames:             []string{"default"},
			wantMsg:            t1,
			wantExpirationTime: now.Add(LeaseDuration),
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(LeaseDuration).Unix()}},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t2},
				"low":      {t3},
			},
			qnames:             []string{"critical", "default", "low"},
			wantMsg:            t2,
			wantExpirationTime: now.Add(LeaseDuration),
			wantPending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {},
				"low":      {t3},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {t2},
				"low":      {},
			},
			wantLease: map[string][]base.Z{
				"default":  {},
				"critical": {{Message: t2, Score: now.Add(LeaseDuration).Unix()}},
				"low":      {},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {},
				"low":      {t3},
			},
			qnames:             []string{"critical", "default", "low"},
			wantMsg:            t1,
			wantExpirationTime: now.Add(LeaseDuration),
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {t3},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {},
				"low":      {},
			},
			wantLease: map[string][]base.Z{
				"default":  {{Message: t1, Score: now.Add(LeaseDuration).Unix()}},
				"critical": {},
				"low":      {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllPendingQueues(t, r.client, tc.pending)

		gotMsg, gotExpirationTime, err := r.Dequeue(tc.qnames...)
		if err != nil {
			t.Errorf("(*RDB).Dequeue(%v) returned error %v", tc.qnames, err)
			continue
		}
		if !cmp.Equal(gotMsg, tc.wantMsg) {
			t.Errorf("(*RDB).Dequeue(%v) returned message %v; want %v",
				tc.qnames, gotMsg, tc.wantMsg)
			continue
		}
		if gotExpirationTime != tc.wantExpirationTime {
			t.Errorf("(*RDB).Dequeue(%v) returned expiration time %v, want %v",
				tc.qnames, gotExpirationTime, tc.wantExpirationTime)
		}

		for queue, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.PendingKey(queue), diff)
			}
		}
		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.ActiveKey(queue), diff)
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.LeaseKey(queue), diff)
			}
		}
	}
}

func TestDequeueError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		pending     map[string][]*base.TaskMessage
		qnames      []string // list of queues to query
		wantErr     error
		wantPending map[string][]*base.TaskMessage
		wantActive  map[string][]*base.TaskMessage
		wantLease   map[string][]base.Z
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			qnames:  []string{"default"},
			wantErr: errors.ErrNoProcessableTask,
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantLease: map[string][]base.Z{
				"default": {},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			qnames:  []string{"critical", "default", "low"},
			wantErr: errors.ErrNoProcessableTask,
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			wantLease: map[string][]base.Z{
				"default":  {},
				"critical": {},
				"low":      {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllPendingQueues(t, r.client, tc.pending)

		gotMsg, _, gotErr := r.Dequeue(tc.qnames...)
		if !errors.Is(gotErr, tc.wantErr) {
			t.Errorf("(*RDB).Dequeue(%v) returned error %v; want %v",
				tc.qnames, gotErr, tc.wantErr)
			continue
		}
		if gotMsg != nil {
			t.Errorf("(*RDB).Dequeue(%v) returned message %v; want nil", tc.qnames, gotMsg)
			continue
		}

		for queue, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.PendingKey(queue), diff)
			}
		}
		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.ActiveKey(queue), diff)
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.LeaseKey(queue), diff)
			}
		}
	}
}

func TestDequeueIgnoresPausedQueues(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "send_email",
		Payload:  h.JSON(map[string]interface{}{"subject": "hello!"}),
		Queue:    "default",
		Timeout:  1800,
		Deadline: 0,
	}
	t2 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "export_csv",
		Payload:  nil,
		Queue:    "critical",
		Timeout:  1800,
		Deadline: 0,
	}

	tests := []struct {
		paused      []string // list of paused queues
		pending     map[string][]*base.TaskMessage
		qnames      []string // list of queues to query
		wantMsg     *base.TaskMessage
		wantErr     error
		wantPending map[string][]*base.TaskMessage
		wantActive  map[string][]*base.TaskMessage
	}{
		{
			paused: []string{"default"},
			pending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t2},
			},
			qnames:  []string{"default", "critical"},
			wantMsg: t2,
			wantErr: nil,
			wantPending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {t2},
			},
		},
		{
			paused: []string{"default"},
			pending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			qnames:  []string{"default"},
			wantMsg: nil,
			wantErr: errors.ErrNoProcessableTask,
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			paused: []string{"critical", "default"},
			pending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t2},
			},
			qnames:  []string{"default", "critical"},
			wantMsg: nil,
			wantErr: errors.ErrNoProcessableTask,
			wantPending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t2},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
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
		h.SeedAllPendingQueues(t, r.client, tc.pending)

		got, _, err := r.Dequeue(tc.qnames...)
		if !cmp.Equal(got, tc.wantMsg) || !errors.Is(err, tc.wantErr) {
			t.Errorf("Dequeue(%v) = %v, %v; want %v, %v",
				tc.qnames, got, err, tc.wantMsg, tc.wantErr)
			continue
		}

		for queue, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.PendingKey(queue), diff)
			}
		}
		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want,+got):\n%s", base.ActiveKey(queue), diff)
			}
		}
	}
}

func TestDone(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	t1 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "send_email",
		Payload:  nil,
		Timeout:  1800,
		Deadline: 0,
		Queue:    "default",
	}
	t2 := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "export_csv",
		Payload:  nil,
		Timeout:  0,
		Deadline: 1592485787,
		Queue:    "custom",
	}
	t3 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "reindex",
		Payload:   nil,
		Timeout:   1800,
		Deadline:  0,
		UniqueKey: "asynq:{default}:unique:b0804ec967f48520697662a204f5fe72",
		Queue:     "default",
	}

	tests := []struct {
		desc       string
		active     map[string][]*base.TaskMessage // initial state of the active list
		lease      map[string][]base.Z            // initial state of the lease set
		target     *base.TaskMessage              // task to remove
		wantActive map[string][]*base.TaskMessage // final state of the active list
		wantLease  map[string][]base.Z            // final state of the lease set
	}{
		{
			desc: "removes message from the correct queue",
			active: map[string][]*base.TaskMessage{
				"default": {t1},
				"custom":  {t2},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}},
				"custom":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
			},
			target: t1,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {},
				"custom":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
			},
		},
		{
			desc: "with one queue",
			active: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}},
			},
			target: t1,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantLease: map[string][]base.Z{
				"default": {},
			},
		},
		{
			desc: "with multiple messages in a queue",
			active: map[string][]*base.TaskMessage{
				"default": {t1, t3},
				"custom":  {t2},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(15 * time.Second).Unix()}, {Message: t3, Score: now.Add(10 * time.Second).Unix()}},
				"custom":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
			},
			target: t3,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1},
				"custom":  {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(15 * time.Second).Unix()}},
				"custom":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllLease(t, r.client, tc.lease)
		h.SeedAllActiveQueues(t, r.client, tc.active)
		for _, msgs := range tc.active {
			for _, msg := range msgs {
				// Set uniqueness lock if unique key is present.
				if len(msg.UniqueKey) > 0 {
					err := r.client.SetNX(context.Background(), msg.UniqueKey, msg.ID, time.Minute).Err()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		}

		err := r.Done(context.Background(), tc.target)
		if err != nil {
			t.Errorf("%s; (*RDB).Done(task) = %v, want nil", tc.desc, err)
			continue
		}

		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got):\n%s", tc.desc, base.ActiveKey(queue), diff)
				continue
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got):\n%s", tc.desc, base.LeaseKey(queue), diff)
				continue
			}
		}

		processedKey := base.ProcessedKey(tc.target.Queue, time.Now())
		gotProcessed := r.client.Get(context.Background(), processedKey).Val()
		if gotProcessed != "1" {
			t.Errorf("%s; GET %q = %q, want 1", tc.desc, processedKey, gotProcessed)
		}
		gotTTL := r.client.TTL(context.Background(), processedKey).Val()
		if gotTTL > statsTTL {
			t.Errorf("%s; TTL %q = %v, want less than or equal to %v", tc.desc, processedKey, gotTTL, statsTTL)
		}

		processedTotalKey := base.ProcessedTotalKey(tc.target.Queue)
		gotProcessedTotal := r.client.Get(context.Background(), processedTotalKey).Val()
		if gotProcessedTotal != "1" {
			t.Errorf("%s; GET %q = %q, want 1", tc.desc, processedTotalKey, gotProcessedTotal)
		}

		if len(tc.target.UniqueKey) > 0 && r.client.Exists(context.Background(), tc.target.UniqueKey).Val() != 0 {
			t.Errorf("%s; Uniqueness lock %q still exists", tc.desc, tc.target.UniqueKey)
		}
	}
}

// Make sure that processed_total counter wraps to 1 when reaching int64 max value.
func TestDoneWithMaxCounter(t *testing.T) {
	r := setup(t)
	defer r.Close()
	msg := &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "foo",
		Payload:  nil,
		Timeout:  1800,
		Deadline: 0,
		Queue:    "default",
	}

	z := base.Z{
		Message: msg,
		Score:   time.Now().Add(15 * time.Second).Unix(),
	}
	h.SeedLease(t, r.client, []base.Z{z}, msg.Queue)
	h.SeedActiveQueue(t, r.client, []*base.TaskMessage{msg}, msg.Queue)

	processedTotalKey := base.ProcessedTotalKey(msg.Queue)
	ctx := context.Background()
	if err := r.client.Set(ctx, processedTotalKey, math.MaxInt64, 0).Err(); err != nil {
		t.Fatalf("Redis command failed: SET %q %v", processedTotalKey, math.MaxInt64)
	}

	if err := r.Done(context.Background(), msg); err != nil {
		t.Fatalf("RDB.Done failed: %v", err)
	}

	gotProcessedTotal := r.client.Get(ctx, processedTotalKey).Val()
	if gotProcessedTotal != "1" {
		t.Errorf("GET %q = %v, want 1", processedTotalKey, gotProcessedTotal)
	}
}

func TestMarkAsComplete(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	t1 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "send_email",
		Payload:   nil,
		Timeout:   1800,
		Deadline:  0,
		Queue:     "default",
		Retention: 3600,
	}
	t2 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "export_csv",
		Payload:   nil,
		Timeout:   0,
		Deadline:  now.Add(2 * time.Hour).Unix(),
		Queue:     "custom",
		Retention: 7200,
	}
	t3 := &base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "reindex",
		Payload:   nil,
		Timeout:   1800,
		Deadline:  0,
		UniqueKey: "asynq:{default}:unique:b0804ec967f48520697662a204f5fe72",
		Queue:     "default",
		Retention: 1800,
	}

	tests := []struct {
		desc          string
		active        map[string][]*base.TaskMessage // initial state of the active list
		lease         map[string][]base.Z            // initial state of the lease set
		completed     map[string][]base.Z            // initial state of the completed set
		target        *base.TaskMessage              // task to mark as completed
		wantActive    map[string][]*base.TaskMessage // final state of the active list
		wantLease     map[string][]base.Z            // final state of the lease set
		wantCompleted map[string][]base.Z            // final state of the completed set
	}{
		{
			desc: "select a message from the correct queue",
			active: map[string][]*base.TaskMessage{
				"default": {t1},
				"custom":  {t2},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(30 * time.Second).Unix()}},
				"custom":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
			},
			completed: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			target: t1,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
				"custom":  {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {},
				"custom":  {{Message: t2, Score: now.Add(20 * time.Second).Unix()}},
			},
			wantCompleted: map[string][]base.Z{
				"default": {{Message: h.TaskMessageWithCompletedAt(*t1, now), Score: now.Unix() + t1.Retention}},
				"custom":  {},
			},
		},
		{
			desc: "with one queue",
			active: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}},
			},
			completed: map[string][]base.Z{
				"default": {},
			},
			target: t1,
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantLease: map[string][]base.Z{
				"default": {},
			},
			wantCompleted: map[string][]base.Z{
				"default": {{Message: h.TaskMessageWithCompletedAt(*t1, now), Score: now.Unix() + t1.Retention}},
			},
		},
		{
			desc: "with multiple messages in a queue",
			active: map[string][]*base.TaskMessage{
				"default": {t1, t3},
				"custom":  {t2},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t3, Score: now.Add(12 * time.Second).Unix()}},
				"custom":  {{Message: t2, Score: now.Add(12 * time.Second).Unix()}},
			},
			completed: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			target: t3,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1},
				"custom":  {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}},
				"custom":  {{Message: t2, Score: now.Add(12 * time.Second).Unix()}},
			},
			wantCompleted: map[string][]base.Z{
				"default": {{Message: h.TaskMessageWithCompletedAt(*t3, now), Score: now.Unix() + t3.Retention}},
				"custom":  {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllLease(t, r.client, tc.lease)
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllCompletedQueues(t, r.client, tc.completed)
		for _, msgs := range tc.active {
			for _, msg := range msgs {
				// Set uniqueness lock if unique key is present.
				if len(msg.UniqueKey) > 0 {
					err := r.client.SetNX(context.Background(), msg.UniqueKey, msg.ID, time.Minute).Err()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		}

		err := r.MarkAsComplete(context.Background(), tc.target)
		if err != nil {
			t.Errorf("%s; (*RDB).MarkAsCompleted(task) = %v, want nil", tc.desc, err)
			continue
		}

		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got):\n%s", tc.desc, base.ActiveKey(queue), diff)
				continue
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got):\n%s", tc.desc, base.LeaseKey(queue), diff)
				continue
			}
		}
		for queue, want := range tc.wantCompleted {
			gotCompleted := h.GetCompletedEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotCompleted, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s; mismatch found in %q: (-want, +got):\n%s", tc.desc, base.CompletedKey(queue), diff)
				continue
			}
		}

		processedKey := base.ProcessedKey(tc.target.Queue, time.Now())
		gotProcessed := r.client.Get(context.Background(), processedKey).Val()
		if gotProcessed != "1" {
			t.Errorf("%s; GET %q = %q, want 1", tc.desc, processedKey, gotProcessed)
		}

		gotTTL := r.client.TTL(context.Background(), processedKey).Val()
		if gotTTL > statsTTL {
			t.Errorf("%s; TTL %q = %v, want less than or equal to %v", tc.desc, processedKey, gotTTL, statsTTL)
		}

		if len(tc.target.UniqueKey) > 0 && r.client.Exists(context.Background(), tc.target.UniqueKey).Val() != 0 {
			t.Errorf("%s; Uniqueness lock %q still exists", tc.desc, tc.target.UniqueKey)
		}
	}
}

func TestRequeue(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	t1 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: nil,
		Queue:   "default",
		Timeout: 1800,
	}
	t2 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "export_csv",
		Payload: nil,
		Queue:   "default",
		Timeout: 3000,
	}
	t3 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: nil,
		Queue:   "critical",
		Timeout: 80,
	}

	tests := []struct {
		pending     map[string][]*base.TaskMessage // initial state of queues
		active      map[string][]*base.TaskMessage // initial state of the active list
		lease       map[string][]base.Z            // initial state of the lease set
		target      *base.TaskMessage              // task to requeue
		wantPending map[string][]*base.TaskMessage // final state of queues
		wantActive  map[string][]*base.TaskMessage // final state of the active list
		wantLease   map[string][]base.Z            // final state of the lease set
	}{
		{
			pending: map[string][]*base.TaskMessage{
				"default": {},
			},
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			target: t1,
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default": {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
				},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			active: map[string][]*base.TaskMessage{
				"default": {t2},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t2, Score: now.Add(20 * time.Second).Unix()},
				},
			},
			target: t2,
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1, t2},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantLease: map[string][]base.Z{
				"default": {},
			},
		},
		{
			pending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {},
			},
			active: map[string][]*base.TaskMessage{
				"default":  {t2},
				"critical": {t3},
			},
			lease: map[string][]base.Z{
				"default":  {{Message: t2, Score: now.Add(10 * time.Second).Unix()}},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
			target: t3,
			wantPending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t3},
			},
			wantActive: map[string][]*base.TaskMessage{
				"default":  {t2},
				"critical": {},
			},
			wantLease: map[string][]base.Z{
				"default":  {{Message: t2, Score: now.Add(10 * time.Second).Unix()}},
				"critical": {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllPendingQueues(t, r.client, tc.pending)
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllLease(t, r.client, tc.lease)

		err := r.Requeue(context.Background(), tc.target)
		if err != nil {
			t.Errorf("(*RDB).Requeue(task) = %v, want nil", err)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
		}
		for qname, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want, +got):\n%s", base.ActiveKey(qname), diff)
			}
		}
		for qname, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want, +got):\n%s", base.LeaseKey(qname), diff)
			}
		}
	}
}

func TestAddToGroup(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	msg := h.NewTaskMessage("mytask", []byte("foo"))
	ctx := context.Background()

	tests := []struct {
		msg      *base.TaskMessage
		groupKey string
	}{
		{
			msg:      msg,
			groupKey: "mygroup",
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		err := r.AddToGroup(ctx, tc.msg, tc.groupKey)
		if err != nil {
			t.Errorf("r.AddToGroup(ctx, msg, %q) returned error: %v", tc.groupKey, err)
			continue
		}

		// Check Group zset has task ID
		gkey := base.GroupKey(tc.msg.Queue, tc.groupKey)
		zs := r.client.ZRangeWithScores(ctx, gkey, 0, -1).Val()
		if n := len(zs); n != 1 {
			t.Errorf("Redis ZSET %q contains %d elements, want 1", gkey, n)
			continue
		}
		if got := zs[0].Member.(string); got != tc.msg.ID {
			t.Errorf("Redis ZSET %q member: got %v, want %v", gkey, got, tc.msg.ID)
			continue
		}
		if got := int64(zs[0].Score); got != now.Unix() {
			t.Errorf("Redis ZSET %q score: got %d, want %d", gkey, got, now.Unix())
			continue
		}

		// Check the values under the task key.
		taskKey := base.TaskKey(tc.msg.Queue, tc.msg.ID)
		encoded := r.client.HGet(ctx, taskKey, "msg").Val() // "msg" field
		decoded := h.MustUnmarshal(t, encoded)
		if diff := cmp.Diff(tc.msg, decoded); diff != "" {
			t.Errorf("persisted message was %v, want %v; (-want, +got)\n%s", decoded, tc.msg, diff)
		}
		state := r.client.HGet(ctx, taskKey, "state").Val() // "state" field
		if want := "aggregating"; state != want {
			t.Errorf("state field under task-key is set to %q, want %q", state, want)
		}
		group := r.client.HGet(ctx, taskKey, "group").Val() // "group" field
		if want := tc.groupKey; group != want {
			t.Errorf("group field under task-key is set to %q, want %q", group, want)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}
	}
}

func TestAddToGroupeTaskIdConflictError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	ctx := context.Background()
	m1 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "foo",
		Payload:   nil,
		UniqueKey: "unique_key_one",
	}
	m2 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "bar",
		Payload:   nil,
		UniqueKey: "unique_key_two",
	}
	const groupKey = "mygroup"

	tests := []struct {
		firstMsg  *base.TaskMessage
		secondMsg *base.TaskMessage
	}{
		{firstMsg: &m1, secondMsg: &m2},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		if err := r.AddToGroup(ctx, tc.firstMsg, groupKey); err != nil {
			t.Errorf("First message: AddToGroup failed: %v", err)
			continue
		}
		if err := r.AddToGroup(ctx, tc.secondMsg, groupKey); !errors.Is(err, errors.ErrTaskIdConflict) {
			t.Errorf("Second message: AddToGroup returned %v, want %v", err, errors.ErrTaskIdConflict)
			continue
		}
	}
}

func TestAddToGroupUnique(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	msg := h.NewTaskMessage("mytask", []byte("foo"))
	msg.UniqueKey = base.UniqueKey(msg.Queue, msg.Type, msg.Payload)
	ctx := context.Background()

	tests := []struct {
		msg      *base.TaskMessage
		groupKey string
		ttl      time.Duration
	}{
		{
			msg:      msg,
			groupKey: "mygroup",
			ttl:      30 * time.Second,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		err := r.AddToGroupUnique(ctx, tc.msg, tc.groupKey, tc.ttl)
		if err != nil {
			t.Errorf("First message: r.AddToGroupUnique(ctx, msg, %q) returned error: %v", tc.groupKey, err)
			continue
		}

		// Check Group zset has task ID
		gkey := base.GroupKey(tc.msg.Queue, tc.groupKey)
		zs := r.client.ZRangeWithScores(ctx, gkey, 0, -1).Val()
		if n := len(zs); n != 1 {
			t.Errorf("Redis ZSET %q contains %d elements, want 1", gkey, n)
			continue
		}
		if got := zs[0].Member.(string); got != tc.msg.ID {
			t.Errorf("Redis ZSET %q member: got %v, want %v", gkey, got, tc.msg.ID)
			continue
		}
		if got := int64(zs[0].Score); got != now.Unix() {
			t.Errorf("Redis ZSET %q score: got %d, want %d", gkey, got, now.Unix())
			continue
		}

		// Check the values under the task key.
		taskKey := base.TaskKey(tc.msg.Queue, tc.msg.ID)
		encoded := r.client.HGet(ctx, taskKey, "msg").Val() // "msg" field
		decoded := h.MustUnmarshal(t, encoded)
		if diff := cmp.Diff(tc.msg, decoded); diff != "" {
			t.Errorf("persisted message was %v, want %v; (-want, +got)\n%s", decoded, tc.msg, diff)
		}
		state := r.client.HGet(ctx, taskKey, "state").Val() // "state" field
		if want := "aggregating"; state != want {
			t.Errorf("state field under task-key is set to %q, want %q", state, want)
		}
		group := r.client.HGet(ctx, taskKey, "group").Val() // "group" field
		if want := tc.groupKey; group != want {
			t.Errorf("group field under task-key is set to %q, want %q", group, want)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}

		got := r.AddToGroupUnique(ctx, tc.msg, tc.groupKey, tc.ttl)
		if !errors.Is(got, errors.ErrDuplicateTask) {
			t.Errorf("Second message: r.AddGroupUnique(ctx, msg, %q) = %v, want %v",
				tc.groupKey, got, errors.ErrDuplicateTask)
			continue
		}

		gotTTL := r.client.TTL(ctx, tc.msg.UniqueKey).Val()
		if !cmp.Equal(tc.ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
			t.Errorf("TTL %q = %v, want %v", tc.msg.UniqueKey, gotTTL, tc.ttl)
			continue
		}
	}
}

func TestAddToGroupUniqueTaskIdConflictError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	ctx := context.Background()
	m1 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "foo",
		Payload:   nil,
		UniqueKey: "unique_key_one",
	}
	m2 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "bar",
		Payload:   nil,
		UniqueKey: "unique_key_two",
	}
	const groupKey = "mygroup"
	const ttl = 30 * time.Second

	tests := []struct {
		firstMsg  *base.TaskMessage
		secondMsg *base.TaskMessage
	}{
		{firstMsg: &m1, secondMsg: &m2},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		if err := r.AddToGroupUnique(ctx, tc.firstMsg, groupKey, ttl); err != nil {
			t.Errorf("First message: AddToGroupUnique failed: %v", err)
			continue
		}
		if err := r.AddToGroupUnique(ctx, tc.secondMsg, groupKey, ttl); !errors.Is(err, errors.ErrTaskIdConflict) {
			t.Errorf("Second message: AddToGroupUnique returned %v, want %v", err, errors.ErrTaskIdConflict)
			continue
		}
	}
}

func TestSchedule(t *testing.T) {
	r := setup(t)
	defer r.Close()
	msg := h.NewTaskMessage("send_email", h.JSON(map[string]interface{}{"subject": "hello"}))
	tests := []struct {
		msg       *base.TaskMessage
		processAt time.Time
	}{
		{msg, time.Now().Add(15 * time.Minute)},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case

		err := r.Schedule(context.Background(), tc.msg, tc.processAt)
		if err != nil {
			t.Errorf("(*RDB).Schedule(%v, %v) = %v, want nil", tc.msg, tc.processAt, err)
			continue
		}

		// Check Scheduled zset has task ID.
		scheduledKey := base.ScheduledKey(tc.msg.Queue)
		zs := r.client.ZRangeWithScores(context.Background(), scheduledKey, 0, -1).Val()
		if n := len(zs); n != 1 {
			t.Errorf("Redis ZSET %q contains %d elements, want 1", scheduledKey, n)
			continue
		}
		if got := zs[0].Member.(string); got != tc.msg.ID {
			t.Errorf("Redis ZSET %q member: got %v, want %v", scheduledKey, got, tc.msg.ID)
			continue
		}
		if got := int64(zs[0].Score); got != tc.processAt.Unix() {
			t.Errorf("Redis ZSET %q score: got %d, want %d",
				scheduledKey, got, tc.processAt.Unix())
			continue
		}

		// Check the values under the task key.
		taskKey := base.TaskKey(tc.msg.Queue, tc.msg.ID)
		encoded := r.client.HGet(context.Background(), taskKey, "msg").Val() // "msg" field
		decoded := h.MustUnmarshal(t, encoded)
		if diff := cmp.Diff(tc.msg, decoded); diff != "" {
			t.Errorf("persisted message was %v, want %v; (-want, +got)\n%s",
				decoded, tc.msg, diff)
		}
		state := r.client.HGet(context.Background(), taskKey, "state").Val() // "state" field
		if want := "scheduled"; state != want {
			t.Errorf("state field under task-key is set to %q, want %q",
				state, want)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}
	}
}

func TestScheduleTaskIdConflictError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "foo",
		Payload:   nil,
		UniqueKey: "unique_key_one",
	}
	m2 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "bar",
		Payload:   nil,
		UniqueKey: "unique_key_two",
	}
	processAt := time.Now().Add(30 * time.Second)

	tests := []struct {
		firstMsg  *base.TaskMessage
		secondMsg *base.TaskMessage
	}{
		{firstMsg: &m1, secondMsg: &m2},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		if err := r.Schedule(context.Background(), tc.firstMsg, processAt); err != nil {
			t.Errorf("First message: Schedule failed: %v", err)
			continue
		}
		if err := r.Schedule(context.Background(), tc.secondMsg, processAt); !errors.Is(err, errors.ErrTaskIdConflict) {
			t.Errorf("Second message: Schedule returned %v, want %v", err, errors.ErrTaskIdConflict)
			continue
		}
	}
}

func TestScheduleUnique(t *testing.T) {
	r := setup(t)
	defer r.Close()
	m1 := base.TaskMessage{
		ID:        uuid.NewString(),
		Type:      "email",
		Payload:   h.JSON(map[string]interface{}{"user_id": 123}),
		Queue:     base.DefaultQueueName,
		UniqueKey: base.UniqueKey(base.DefaultQueueName, "email", h.JSON(map[string]interface{}{"user_id": 123})),
	}

	tests := []struct {
		msg       *base.TaskMessage
		processAt time.Time
		ttl       time.Duration // uniqueness lock ttl
	}{
		{&m1, time.Now().Add(15 * time.Minute), time.Minute},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case

		desc := "(*RDB).ScheduleUnique(msg, processAt, ttl)"
		err := r.ScheduleUnique(context.Background(), tc.msg, tc.processAt, tc.ttl)
		if err != nil {
			t.Errorf("First task: %s = %v, want nil", desc, err)
			continue
		}

		// Check Scheduled zset has task ID.
		scheduledKey := base.ScheduledKey(tc.msg.Queue)
		zs := r.client.ZRangeWithScores(context.Background(), scheduledKey, 0, -1).Val()
		if n := len(zs); n != 1 {
			t.Errorf("Redis ZSET %q contains %d elements, want 1",
				scheduledKey, n)
			continue
		}
		if got := zs[0].Member.(string); got != tc.msg.ID {
			t.Errorf("Redis ZSET %q member: got %v, want %v",
				scheduledKey, got, tc.msg.ID)
			continue
		}
		if got := int64(zs[0].Score); got != tc.processAt.Unix() {
			t.Errorf("Redis ZSET %q score: got %d, want %d",
				scheduledKey, got, tc.processAt.Unix())
			continue
		}

		// Check the values under the task key.
		taskKey := base.TaskKey(tc.msg.Queue, tc.msg.ID)
		encoded := r.client.HGet(context.Background(), taskKey, "msg").Val() // "msg" field
		decoded := h.MustUnmarshal(t, encoded)
		if diff := cmp.Diff(tc.msg, decoded); diff != "" {
			t.Errorf("persisted message was %v, want %v; (-want, +got)\n%s",
				decoded, tc.msg, diff)
		}
		state := r.client.HGet(context.Background(), taskKey, "state").Val() // "state" field
		if want := "scheduled"; state != want {
			t.Errorf("state field under task-key is set to %q, want %q",
				state, want)
		}
		uniqueKey := r.client.HGet(context.Background(), taskKey, "unique_key").Val() // "unique_key" field
		if uniqueKey != tc.msg.UniqueKey {
			t.Errorf("uniqueue_key field under task key is set to %q, want %q", uniqueKey, tc.msg.UniqueKey)
		}

		// Check queue is in the AllQueues set.
		if !r.client.SIsMember(context.Background(), base.AllQueues, tc.msg.Queue).Val() {
			t.Errorf("%q is not a member of SET %q", tc.msg.Queue, base.AllQueues)
		}

		// Enqueue the second message, should fail.
		got := r.ScheduleUnique(context.Background(), tc.msg, tc.processAt, tc.ttl)
		if !errors.Is(got, errors.ErrDuplicateTask) {
			t.Errorf("Second task: %s = %v, want %v", desc, got, errors.ErrDuplicateTask)
			continue
		}

		gotTTL := r.client.TTL(context.Background(), tc.msg.UniqueKey).Val()
		if !cmp.Equal(tc.ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
			t.Errorf("TTL %q = %v, want %v", tc.msg.UniqueKey, gotTTL, tc.ttl)
			continue
		}
	}
}

func TestScheduleUniqueTaskIdConflictError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	m1 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "foo",
		Payload:   nil,
		UniqueKey: "unique_key_one",
	}
	m2 := base.TaskMessage{
		ID:        "custom_id",
		Type:      "bar",
		Payload:   nil,
		UniqueKey: "unique_key_two",
	}
	const ttl = 30 * time.Second
	processAt := time.Now().Add(30 * time.Second)

	tests := []struct {
		firstMsg  *base.TaskMessage
		secondMsg *base.TaskMessage
	}{
		{firstMsg: &m1, secondMsg: &m2},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case.

		if err := r.ScheduleUnique(context.Background(), tc.firstMsg, processAt, ttl); err != nil {
			t.Errorf("First message: ScheduleUnique failed: %v", err)
			continue
		}
		if err := r.ScheduleUnique(context.Background(), tc.secondMsg, processAt, ttl); !errors.Is(err, errors.ErrTaskIdConflict) {
			t.Errorf("Second message: ScheduleUnique returned %v, want %v", err, errors.ErrTaskIdConflict)
			continue
		}
	}
}

func TestRetry(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	t1 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: h.JSON(map[string]interface{}{"subject": "Hola!"}),
		Retried: 10,
		Timeout: 1800,
		Queue:   "default",
	}
	t2 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "gen_thumbnail",
		Payload: h.JSON(map[string]interface{}{"path": "some/path/to/image.jpg"}),
		Timeout: 3000,
		Queue:   "default",
	}
	t3 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "reindex",
		Payload: nil,
		Timeout: 60,
		Queue:   "default",
	}
	t4 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_notification",
		Payload: nil,
		Timeout: 1800,
		Queue:   "custom",
	}
	errMsg := "SMTP server is not responding"

	tests := []struct {
		active     map[string][]*base.TaskMessage
		lease      map[string][]base.Z
		retry      map[string][]base.Z
		msg        *base.TaskMessage
		processAt  time.Time
		errMsg     string
		wantActive map[string][]*base.TaskMessage
		wantLease  map[string][]base.Z
		wantRetry  map[string][]base.Z
	}{
		{
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(10 * time.Second).Unix()}},
			},
			retry: map[string][]base.Z{
				"default": {{Message: t3, Score: now.Add(time.Minute).Unix()}},
			},
			msg:       t1,
			processAt: now.Add(5 * time.Minute),
			errMsg:    errMsg,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t2, Score: now.Add(10 * time.Second).Unix()}},
			},
			wantRetry: map[string][]base.Z{
				"default": {
					{Message: h.TaskMessageAfterRetry(*t1, errMsg, now), Score: now.Add(5 * time.Minute).Unix()},
					{Message: t3, Score: now.Add(time.Minute).Unix()},
				},
			},
		},
		{
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2},
				"custom":  {t4},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(20 * time.Second).Unix()}},
				"custom":  {{Message: t4, Score: now.Add(10 * time.Second).Unix()}},
			},
			retry: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			msg:       t4,
			processAt: now.Add(5 * time.Minute),
			errMsg:    errMsg,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1, t2},
				"custom":  {},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(20 * time.Second).Unix()}},
				"custom":  {},
			},
			wantRetry: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: h.TaskMessageAfterRetry(*t4, errMsg, now), Score: now.Add(5 * time.Minute).Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllLease(t, r.client, tc.lease)
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		err := r.Retry(context.Background(), tc.msg, tc.processAt, tc.errMsg, true /*isFailure*/)
		if err != nil {
			t.Errorf("(*RDB).Retry = %v, want nil", err)
			continue
		}

		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ActiveKey(queue), diff)
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.LeaseKey(queue), diff)
			}
		}
		for queue, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.RetryKey(queue), diff)
			}
		}

		processedKey := base.ProcessedKey(tc.msg.Queue, time.Now())
		gotProcessed := r.client.Get(context.Background(), processedKey).Val()
		if gotProcessed != "1" {
			t.Errorf("GET %q = %q, want 1", processedKey, gotProcessed)
		}
		gotTTL := r.client.TTL(context.Background(), processedKey).Val()
		if gotTTL > statsTTL {
			t.Errorf("TTL %q = %v, want less than or equal to %v", processedKey, gotTTL, statsTTL)
		}

		failedKey := base.FailedKey(tc.msg.Queue, time.Now())
		gotFailed := r.client.Get(context.Background(), failedKey).Val()
		if gotFailed != "1" {
			t.Errorf("GET %q = %q, want 1", failedKey, gotFailed)
		}
		gotTTL = r.client.TTL(context.Background(), failedKey).Val()
		if gotTTL > statsTTL {
			t.Errorf("TTL %q = %v, want less than or equal to %v", failedKey, gotTTL, statsTTL)
		}

		processedTotalKey := base.ProcessedTotalKey(tc.msg.Queue)
		gotProcessedTotal := r.client.Get(context.Background(), processedTotalKey).Val()
		if gotProcessedTotal != "1" {
			t.Errorf("GET %q = %q, want 1", processedTotalKey, gotProcessedTotal)
		}

		failedTotalKey := base.FailedTotalKey(tc.msg.Queue)
		gotFailedTotal := r.client.Get(context.Background(), failedTotalKey).Val()
		if gotFailedTotal != "1" {
			t.Errorf("GET %q = %q, want 1", failedTotalKey, gotFailedTotal)
		}
	}
}

func TestRetryWithNonFailureError(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	t1 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: h.JSON(map[string]interface{}{"subject": "Hola!"}),
		Retried: 10,
		Timeout: 1800,
		Queue:   "default",
	}
	t2 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "gen_thumbnail",
		Payload: h.JSON(map[string]interface{}{"path": "some/path/to/image.jpg"}),
		Timeout: 3000,
		Queue:   "default",
	}
	t3 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "reindex",
		Payload: nil,
		Timeout: 60,
		Queue:   "default",
	}
	t4 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_notification",
		Payload: nil,
		Timeout: 1800,
		Queue:   "custom",
	}
	errMsg := "SMTP server is not responding"

	tests := []struct {
		active     map[string][]*base.TaskMessage
		lease      map[string][]base.Z
		retry      map[string][]base.Z
		msg        *base.TaskMessage
		processAt  time.Time
		errMsg     string
		wantActive map[string][]*base.TaskMessage
		wantLease  map[string][]base.Z
		wantRetry  map[string][]base.Z
	}{
		{
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(10 * time.Second).Unix()}},
			},
			retry: map[string][]base.Z{
				"default": {{Message: t3, Score: now.Add(time.Minute).Unix()}},
			},
			msg:       t1,
			processAt: now.Add(5 * time.Minute),
			errMsg:    errMsg,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t2, Score: now.Add(10 * time.Second).Unix()}},
			},
			wantRetry: map[string][]base.Z{
				"default": {
					// Task message should include the error message but without incrementing the retry count.
					{Message: h.TaskMessageWithError(*t1, errMsg, now), Score: now.Add(5 * time.Minute).Unix()},
					{Message: t3, Score: now.Add(time.Minute).Unix()},
				},
			},
		},
		{
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2},
				"custom":  {t4},
			},
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(10 * time.Second).Unix()}},
				"custom":  {{Message: t4, Score: now.Add(10 * time.Second).Unix()}},
			},
			retry: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			msg:       t4,
			processAt: now.Add(5 * time.Minute),
			errMsg:    errMsg,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1, t2},
				"custom":  {},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(10 * time.Second).Unix()}},
				"custom":  {},
			},
			wantRetry: map[string][]base.Z{
				"default": {},
				"custom": {
					// Task message should include the error message but without incrementing the retry count.
					{Message: h.TaskMessageWithError(*t4, errMsg, now), Score: now.Add(5 * time.Minute).Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllLease(t, r.client, tc.lease)
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		err := r.Retry(context.Background(), tc.msg, tc.processAt, tc.errMsg, false /*isFailure*/)
		if err != nil {
			t.Errorf("(*RDB).Retry = %v, want nil", err)
			continue
		}

		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ActiveKey(queue), diff)
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.LeaseKey(queue), diff)
			}
		}
		for queue, want := range tc.wantRetry {
			gotRetry := h.GetRetryEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotRetry, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.RetryKey(queue), diff)
			}
		}

		// If isFailure is set to false, no stats should be recorded to avoid skewing the error rate.
		processedKey := base.ProcessedKey(tc.msg.Queue, time.Now())
		gotProcessed := r.client.Get(context.Background(), processedKey).Val()
		if gotProcessed != "" {
			t.Errorf("GET %q = %q, want empty", processedKey, gotProcessed)
		}

		// If isFailure is set to false, no stats should be recorded to avoid skewing the error rate.
		failedKey := base.FailedKey(tc.msg.Queue, time.Now())
		gotFailed := r.client.Get(context.Background(), failedKey).Val()
		if gotFailed != "" {
			t.Errorf("GET %q = %q, want empty", failedKey, gotFailed)
		}

		processedTotalKey := base.ProcessedTotalKey(tc.msg.Queue)
		gotProcessedTotal := r.client.Get(context.Background(), processedTotalKey).Val()
		if gotProcessedTotal != "" {
			t.Errorf("GET %q = %q, want empty", processedTotalKey, gotProcessedTotal)
		}

		failedTotalKey := base.FailedTotalKey(tc.msg.Queue)
		gotFailedTotal := r.client.Get(context.Background(), failedTotalKey).Val()
		if gotFailedTotal != "" {
			t.Errorf("GET %q = %q, want empty", failedTotalKey, gotFailedTotal)
		}
	}
}

func TestArchive(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	t1 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: nil,
		Queue:   "default",
		Retry:   25,
		Retried: 25,
		Timeout: 1800,
	}
	t2 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "reindex",
		Payload: nil,
		Queue:   "default",
		Retry:   25,
		Retried: 0,
		Timeout: 3000,
	}
	t3 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "generate_csv",
		Payload: nil,
		Queue:   "default",
		Retry:   25,
		Retried: 0,
		Timeout: 60,
	}
	t4 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: nil,
		Queue:   "custom",
		Retry:   25,
		Retried: 25,
		Timeout: 1800,
	}
	errMsg := "SMTP server not responding"

	tests := []struct {
		active       map[string][]*base.TaskMessage
		lease        map[string][]base.Z
		archived     map[string][]base.Z
		target       *base.TaskMessage // task to archive
		wantActive   map[string][]*base.TaskMessage
		wantLease    map[string][]base.Z
		wantArchived map[string][]base.Z
	}{
		{
			active: map[string][]*base.TaskMessage{
				"default": {t1, t2},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {
					{Message: t3, Score: now.Add(-time.Hour).Unix()},
				},
			},
			target: t1,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t2},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t2, Score: now.Add(10 * time.Second).Unix()}},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: h.TaskMessageWithError(*t1, errMsg, now), Score: now.Unix()},
					{Message: t3, Score: now.Add(-time.Hour).Unix()},
				},
			},
		},
		{
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
			archived: map[string][]base.Z{
				"default": {},
			},
			target: t1,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t2, t3},
			},
			wantLease: map[string][]base.Z{
				"default": {
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
					{Message: t3, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: h.TaskMessageWithError(*t1, errMsg, now), Score: now.Unix()},
				},
			},
		},
		{
			active: map[string][]*base.TaskMessage{
				"default": {t1},
				"custom":  {t4},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
				},
				"custom": {
					{Message: t4, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {},
				"custom":  {},
			},
			target: t4,
			wantActive: map[string][]*base.TaskMessage{
				"default": {t1},
				"custom":  {},
			},
			wantLease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(10 * time.Second).Unix()}},
				"custom":  {},
			},
			wantArchived: map[string][]base.Z{
				"default": {},
				"custom": {
					{Message: h.TaskMessageWithError(*t4, errMsg, now), Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllActiveQueues(t, r.client, tc.active)
		h.SeedAllLease(t, r.client, tc.lease)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		err := r.Archive(context.Background(), tc.target, errMsg)
		if err != nil {
			t.Errorf("(*RDB).Archive(%v, %v) = %v, want nil", tc.target, errMsg, err)
			continue
		}

		for queue, want := range tc.wantActive {
			gotActive := h.GetActiveMessages(t, r.client, queue)
			if diff := cmp.Diff(want, gotActive, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q: (-want, +got)\n%s", base.ActiveKey(queue), diff)
			}
		}
		for queue, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("mismatch found in %q after calling (*RDB).Archive: (-want, +got):\n%s", base.LeaseKey(queue), diff)
			}
		}
		for queue, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, queue)
			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt, zScoreCmpOpt, timeCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q after calling (*RDB).Archive: (-want, +got):\n%s", base.ArchivedKey(queue), diff)
			}
		}

		processedKey := base.ProcessedKey(tc.target.Queue, time.Now())
		gotProcessed := r.client.Get(context.Background(), processedKey).Val()
		if gotProcessed != "1" {
			t.Errorf("GET %q = %q, want 1", processedKey, gotProcessed)
		}
		gotTTL := r.client.TTL(context.Background(), processedKey).Val()
		if gotTTL > statsTTL {
			t.Errorf("TTL %q = %v, want less than or equal to %v", processedKey, gotTTL, statsTTL)
		}

		failedKey := base.FailedKey(tc.target.Queue, time.Now())
		gotFailed := r.client.Get(context.Background(), failedKey).Val()
		if gotFailed != "1" {
			t.Errorf("GET %q = %q, want 1", failedKey, gotFailed)
		}
		gotTTL = r.client.TTL(context.Background(), processedKey).Val()
		if gotTTL > statsTTL {
			t.Errorf("TTL %q = %v, want less than or equal to %v", failedKey, gotTTL, statsTTL)
		}

		processedTotalKey := base.ProcessedTotalKey(tc.target.Queue)
		gotProcessedTotal := r.client.Get(context.Background(), processedTotalKey).Val()
		if gotProcessedTotal != "1" {
			t.Errorf("GET %q = %q, want 1", processedTotalKey, gotProcessedTotal)
		}

		failedTotalKey := base.FailedTotalKey(tc.target.Queue)
		gotFailedTotal := r.client.Get(context.Background(), failedTotalKey).Val()
		if gotFailedTotal != "1" {
			t.Errorf("GET %q = %q, want 1", failedTotalKey, gotFailedTotal)
		}
	}
}

func TestArchiveTrim(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	t1 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "send_email",
		Payload: nil,
		Queue:   "default",
		Retry:   25,
		Retried: 25,
		Timeout: 1800,
	}
	t2 := &base.TaskMessage{
		ID:      uuid.NewString(),
		Type:    "reindex",
		Payload: nil,
		Queue:   "default",
		Retry:   25,
		Retried: 0,
		Timeout: 3000,
	}
	errMsg := "SMTP server not responding"

	maxArchiveSet := make([]base.Z, 0)
	for i := 0; i < maxArchiveSize-1; i++ {
		maxArchiveSet = append(maxArchiveSet, base.Z{Message: &base.TaskMessage{
			ID:      uuid.NewString(),
			Type:    "generate_csv",
			Payload: nil,
			Queue:   "default",
			Retry:   25,
			Retried: 0,
			Timeout: 60,
		}, Score: now.Add(-time.Hour + -time.Second*time.Duration(i)).Unix()})
	}

	wantMaxArchiveSet := make([]base.Z, 0)
	// newly archived task should be at the front
	wantMaxArchiveSet = append(wantMaxArchiveSet, base.Z{Message: h.TaskMessageWithError(*t1, errMsg, now), Score: now.Unix()})
	// oldest task should be dropped from the set
	wantMaxArchiveSet = append(wantMaxArchiveSet, maxArchiveSet[:len(maxArchiveSet)-1]...)

	tests := []struct {
		toArchive    map[string][]*base.TaskMessage
		lease        map[string][]base.Z
		archived     map[string][]base.Z
		wantArchived map[string][]base.Z
	}{
		{ // simple, 1 to be archived, 1 already archived, both are in the archive set
			toArchive: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {
					{Message: t2, Score: now.Add(-time.Hour).Unix()},
				},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: h.TaskMessageWithError(*t1, errMsg, now), Score: now.Unix()},
					{Message: t2, Score: now.Add(-time.Hour).Unix()},
				},
			},
		},
		{ // 1 to be archived, 1 already archived but past expiry, only the newly archived task should be left
			toArchive: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": {
					{Message: t2, Score: now.Add(-time.Hour * 24 * (archivedExpirationInDays + 1)).Unix()},
				},
			},
			wantArchived: map[string][]base.Z{
				"default": {
					{Message: h.TaskMessageWithError(*t1, errMsg, now), Score: now.Unix()},
				},
			},
		},
		{ // 1 to be archived, maxArchiveSize in archive set, archive set should be trimmed back to maxArchiveSize and newly archived task should be in the set
			toArchive: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			archived: map[string][]base.Z{
				"default": maxArchiveSet,
			},
			wantArchived: map[string][]base.Z{
				"default": wantMaxArchiveSet,
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllActiveQueues(t, r.client, tc.toArchive)
		h.SeedAllLease(t, r.client, tc.lease)
		h.SeedAllArchivedQueues(t, r.client, tc.archived)

		for _, tasks := range tc.toArchive {
			for _, target := range tasks {
				err := r.Archive(context.Background(), target, errMsg)
				if err != nil {
					t.Errorf("(*RDB).Archive(%v, %v) = %v, want nil", target, errMsg, err)
					continue
				}
			}
		}

		for queue, want := range tc.wantArchived {
			gotArchived := h.GetArchivedEntries(t, r.client, queue)

			if diff := cmp.Diff(want, gotArchived, h.SortZSetEntryOpt, zScoreCmpOpt, timeCmpOpt); diff != "" {
				t.Errorf("mismatch found in %q after calling (*RDB).Archive: (-want, +got):\n%s", base.ArchivedKey(queue), diff)
			}

			// check that only keys present in the archived set are in rdb
			vals := r.client.Keys(context.Background(), base.TaskKeyPrefix(queue)+"*").Val()
			if len(vals) != len(gotArchived) {
				t.Errorf("len of keys = %v, want %v", len(vals), len(gotArchived))
				return
			}

			for _, val := range vals {
				found := false
				for _, entry := range gotArchived {
					if strings.Contains(val, entry.Message.ID) {
						found = true
						break
					}
				}

				if !found {
					t.Errorf("key %v not found in archived set (it was orphaned by the archive trim)", val)
				}
			}
		}
	}
}

func TestForwardIfReadyWithGroup(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("generate_csv", nil)
	t3 := h.NewTaskMessage("gen_thumbnail", nil)
	t4 := h.NewTaskMessageWithQueue("important_task", nil, "critical")
	t5 := h.NewTaskMessageWithQueue("minor_task", nil, "low")
	// Set group keys for the tasks.
	t1.GroupKey = "notifications"
	t2.GroupKey = "csv"
	t4.GroupKey = "critical_task_group"
	t5.GroupKey = "minor_task_group"

	ctx := context.Background()
	secondAgo := now.Add(-time.Second)

	tests := []struct {
		scheduled     map[string][]base.Z
		retry         map[string][]base.Z
		qnames        []string
		wantPending   map[string][]*base.TaskMessage
		wantScheduled map[string][]*base.TaskMessage
		wantRetry     map[string][]*base.TaskMessage
		wantGroup     map[string]map[string][]base.Z
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: secondAgo.Unix()},
					{Message: t2, Score: secondAgo.Unix()},
				},
			},
			retry: map[string][]base.Z{
				"default": {{Message: t3, Score: secondAgo.Unix()}},
			},
			qnames: []string{"default"},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t3},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantGroup: map[string]map[string][]base.Z{
				"default": {
					"notifications": {{Message: t1, Score: now.Unix()}},
					"csv":           {{Message: t2, Score: now.Unix()}},
				},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default":  {{Message: t1, Score: secondAgo.Unix()}},
				"critical": {{Message: t4, Score: secondAgo.Unix()}},
				"low":      {},
			},
			retry: map[string][]base.Z{
				"default":  {},
				"critical": {},
				"low":      {{Message: t5, Score: secondAgo.Unix()}},
			},
			qnames: []string{"default", "critical", "low"},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			wantGroup: map[string]map[string][]base.Z{
				"default": {
					"notifications": {{Message: t1, Score: now.Unix()}},
				},
				"critical": {
					"critical_task_group": {{Message: t4, Score: now.Unix()}},
				},
				"low": {
					"minor_task_group": {{Message: t5, Score: now.Unix()}},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		err := r.ForwardIfReady(tc.qnames...)
		if err != nil {
			t.Errorf("(*RDB).ForwardIfReady(%v) = %v, want nil", tc.qnames, err)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
			// Make sure "pending_since" field is set
			for _, msg := range gotPending {
				pendingSince := r.client.HGet(ctx, base.TaskKey(msg.Queue, msg.ID), "pending_since").Val()
				if want := strconv.Itoa(int(now.UnixNano())); pendingSince != want {
					t.Error("pending_since field is not set for newly pending message")
				}
			}
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.RetryKey(qname), diff)
			}
		}
		for qname, groups := range tc.wantGroup {
			for groupKey, wantGroup := range groups {
				gotGroup := h.GetGroupEntries(t, r.client, qname, groupKey)
				if diff := cmp.Diff(wantGroup, gotGroup, h.SortZSetEntryOpt); diff != "" {
					t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.GroupKey(qname, groupKey), diff)
				}
			}
		}
	}
}

func TestForwardIfReady(t *testing.T) {
	r := setup(t)
	defer r.Close()
	t1 := h.NewTaskMessage("send_email", nil)
	t2 := h.NewTaskMessage("generate_csv", nil)
	t3 := h.NewTaskMessage("gen_thumbnail", nil)
	t4 := h.NewTaskMessageWithQueue("important_task", nil, "critical")
	t5 := h.NewTaskMessageWithQueue("minor_task", nil, "low")
	secondAgo := time.Now().Add(-time.Second)
	hourFromNow := time.Now().Add(time.Hour)

	tests := []struct {
		scheduled     map[string][]base.Z
		retry         map[string][]base.Z
		qnames        []string
		wantPending   map[string][]*base.TaskMessage
		wantScheduled map[string][]*base.TaskMessage
		wantRetry     map[string][]*base.TaskMessage
	}{
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: secondAgo.Unix()},
					{Message: t2, Score: secondAgo.Unix()},
				},
			},
			retry: map[string][]base.Z{
				"default": {{Message: t3, Score: secondAgo.Unix()}},
			},
			qnames: []string{"default"},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t1, t2, t3},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: hourFromNow.Unix()},
					{Message: t2, Score: secondAgo.Unix()},
				},
			},
			retry: map[string][]base.Z{
				"default": {{Message: t3, Score: secondAgo.Unix()}},
			},
			qnames: []string{"default"},
			wantPending: map[string][]*base.TaskMessage{
				"default": {t2, t3},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {t1},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default": {
					{Message: t1, Score: hourFromNow.Unix()},
					{Message: t2, Score: hourFromNow.Unix()},
				},
			},
			retry: map[string][]base.Z{
				"default": {{Message: t3, Score: hourFromNow.Unix()}},
			},
			qnames: []string{"default"},
			wantPending: map[string][]*base.TaskMessage{
				"default": {},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default": {t1, t2},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default": {t3},
			},
		},
		{
			scheduled: map[string][]base.Z{
				"default":  {{Message: t1, Score: secondAgo.Unix()}},
				"critical": {{Message: t4, Score: secondAgo.Unix()}},
				"low":      {},
			},
			retry: map[string][]base.Z{
				"default":  {},
				"critical": {},
				"low":      {{Message: t5, Score: secondAgo.Unix()}},
			},
			qnames: []string{"default", "critical", "low"},
			wantPending: map[string][]*base.TaskMessage{
				"default":  {t1},
				"critical": {t4},
				"low":      {t5},
			},
			wantScheduled: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
			wantRetry: map[string][]*base.TaskMessage{
				"default":  {},
				"critical": {},
				"low":      {},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client) // clean up db before each test case
		h.SeedAllScheduledQueues(t, r.client, tc.scheduled)
		h.SeedAllRetryQueues(t, r.client, tc.retry)

		now := time.Now()
		r.SetClock(timeutil.NewSimulatedClock(now))

		err := r.ForwardIfReady(tc.qnames...)
		if err != nil {
			t.Errorf("(*RDB).ForwardIfReady(%v) = %v, want nil", tc.qnames, err)
			continue
		}

		for qname, want := range tc.wantPending {
			gotPending := h.GetPendingMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotPending, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.PendingKey(qname), diff)
			}
			// Make sure "pending_since" field is set
			for _, msg := range gotPending {
				pendingSince := r.client.HGet(context.Background(), base.TaskKey(msg.Queue, msg.ID), "pending_since").Val()
				if want := strconv.Itoa(int(now.UnixNano())); pendingSince != want {
					t.Error("pending_since field is not set for newly pending message")
				}
			}
		}
		for qname, want := range tc.wantScheduled {
			gotScheduled := h.GetScheduledMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotScheduled, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.ScheduledKey(qname), diff)
			}
		}
		for qname, want := range tc.wantRetry {
			gotRetry := h.GetRetryMessages(t, r.client, qname)
			if diff := cmp.Diff(want, gotRetry, h.SortMsgOpt); diff != "" {
				t.Errorf("mismatch found in %q; (-want, +got)\n%s", base.RetryKey(qname), diff)
			}
		}
	}
}

func newCompletedTask(qname, typename string, payload []byte, completedAt time.Time) *base.TaskMessage {
	msg := h.NewTaskMessageWithQueue(typename, payload, qname)
	msg.CompletedAt = completedAt.Unix()
	return msg
}

func TestDeleteExpiredCompletedTasks(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	secondAgo := now.Add(-time.Second)
	hourFromNow := now.Add(time.Hour)
	hourAgo := now.Add(-time.Hour)
	minuteAgo := now.Add(-time.Minute)

	t1 := newCompletedTask("default", "task1", nil, hourAgo)
	t2 := newCompletedTask("default", "task2", nil, minuteAgo)
	t3 := newCompletedTask("default", "task3", nil, secondAgo)
	t4 := newCompletedTask("critical", "critical_task", nil, hourAgo)
	t5 := newCompletedTask("low", "low_priority_task", nil, hourAgo)

	tests := []struct {
		desc          string
		completed     map[string][]base.Z
		qname         string
		wantCompleted map[string][]base.Z
	}{
		{
			desc: "deletes expired task from default queue",
			completed: map[string][]base.Z{
				"default": {
					{Message: t1, Score: secondAgo.Unix()},
					{Message: t2, Score: hourFromNow.Unix()},
					{Message: t3, Score: now.Unix()},
				},
			},
			qname: "default",
			wantCompleted: map[string][]base.Z{
				"default": {
					{Message: t2, Score: hourFromNow.Unix()},
				},
			},
		},
		{
			desc: "deletes expired task from specified queue",
			completed: map[string][]base.Z{
				"default": {
					{Message: t2, Score: secondAgo.Unix()},
				},
				"critical": {
					{Message: t4, Score: secondAgo.Unix()},
				},
				"low": {
					{Message: t5, Score: now.Unix()},
				},
			},
			qname: "critical",
			wantCompleted: map[string][]base.Z{
				"default": {
					{Message: t2, Score: secondAgo.Unix()},
				},
				"critical": {},
				"low": {
					{Message: t5, Score: now.Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllCompletedQueues(t, r.client, tc.completed)

		if err := r.DeleteExpiredCompletedTasks(tc.qname, 100); err != nil {
			t.Errorf("DeleteExpiredCompletedTasks(%q, 100) failed: %v", tc.qname, err)
			continue
		}

		for qname, want := range tc.wantCompleted {
			got := h.GetCompletedEntries(t, r.client, qname)
			if diff := cmp.Diff(want, got, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s: diff found in %q completed set: want=%v, got=%v\n%s", tc.desc, qname, want, got, diff)
			}
		}
	}
}

func TestListLeaseExpired(t *testing.T) {
	t1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	t2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	t3 := h.NewTaskMessageWithQueue("task3", nil, "critical")

	now := time.Now()

	tests := []struct {
		desc   string
		lease  map[string][]base.Z
		qnames []string
		cutoff time.Time
		want   []*base.TaskMessage
	}{
		{
			desc: "with a single active task",
			lease: map[string][]base.Z{
				"default": {{Message: t1, Score: now.Add(-10 * time.Second).Unix()}},
			},
			qnames: []string{"default"},
			cutoff: now,
			want:   []*base.TaskMessage{t1},
		},
		{
			desc: "with multiple active tasks, and one expired",
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(-5 * time.Minute).Unix()},
					{Message: t2, Score: now.Add(20 * time.Second).Unix()},
				},
				"critical": {
					{Message: t3, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			qnames: []string{"default", "critical"},
			cutoff: now,
			want:   []*base.TaskMessage{t1},
		},
		{
			desc: "with multiple expired active tasks",
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(-2 * time.Minute).Unix()},
					{Message: t2, Score: now.Add(20 * time.Second).Unix()},
				},
				"critical": {
					{Message: t3, Score: now.Add(-30 * time.Second).Unix()},
				},
			},
			qnames: []string{"default", "critical"},
			cutoff: now,
			want:   []*base.TaskMessage{t1, t3},
		},
		{
			desc: "with empty active queue",
			lease: map[string][]base.Z{
				"default":  {},
				"critical": {},
			},
			qnames: []string{"default", "critical"},
			cutoff: now,
			want:   []*base.TaskMessage{},
		},
	}

	r := setup(t)
	defer r.Close()
	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllLease(t, r.client, tc.lease)

		got, err := r.ListLeaseExpired(tc.cutoff, tc.qnames...)
		if err != nil {
			t.Errorf("%s; ListLeaseExpired(%v) returned error: %v", tc.desc, tc.cutoff, err)
			continue
		}

		if diff := cmp.Diff(tc.want, got, h.SortMsgOpt); diff != "" {
			t.Errorf("%s; ListLeaseExpired(%v) returned %v, want %v;(-want,+got)\n%s",
				tc.desc, tc.cutoff, got, tc.want, diff)
		}
	}
}

func TestExtendLease(t *testing.T) {
	r := setup(t)
	defer r.Close()
	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	t1 := h.NewTaskMessageWithQueue("task1", nil, "default")
	t2 := h.NewTaskMessageWithQueue("task2", nil, "default")
	t3 := h.NewTaskMessageWithQueue("task3", nil, "critical")
	t4 := h.NewTaskMessageWithQueue("task4", nil, "default")

	tests := []struct {
		desc               string
		lease              map[string][]base.Z
		qname              string
		ids                []string
		wantExpirationTime time.Time
		wantLease          map[string][]base.Z
	}{
		{
			desc: "Should extends lease for a single message in a queue",
			lease: map[string][]base.Z{
				"default":  {{Message: t1, Score: now.Add(10 * time.Second).Unix()}},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
			qname:              "default",
			ids:                []string{t1.ID},
			wantExpirationTime: now.Add(LeaseDuration),
			wantLease: map[string][]base.Z{
				"default":  {{Message: t1, Score: now.Add(LeaseDuration).Unix()}},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
		},
		{
			desc: "Should extends lease for multiple message in a queue",
			lease: map[string][]base.Z{
				"default":  {{Message: t1, Score: now.Add(10 * time.Second).Unix()}, {Message: t2, Score: now.Add(10 * time.Second).Unix()}},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
			qname:              "default",
			ids:                []string{t1.ID, t2.ID},
			wantExpirationTime: now.Add(LeaseDuration),
			wantLease: map[string][]base.Z{
				"default":  {{Message: t1, Score: now.Add(LeaseDuration).Unix()}, {Message: t2, Score: now.Add(LeaseDuration).Unix()}},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
		},
		{
			desc: "Should selectively extends lease for messages in a queue",
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
					{Message: t2, Score: now.Add(10 * time.Second).Unix()},
					{Message: t4, Score: now.Add(10 * time.Second).Unix()},
				},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
			qname:              "default",
			ids:                []string{t2.ID, t4.ID},
			wantExpirationTime: now.Add(LeaseDuration),
			wantLease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
					{Message: t2, Score: now.Add(LeaseDuration).Unix()},
					{Message: t4, Score: now.Add(LeaseDuration).Unix()},
				},
				"critical": {{Message: t3, Score: now.Add(10 * time.Second).Unix()}},
			},
		},
		{
			desc: "Should not add a new entry in the lease set",
			lease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(10 * time.Second).Unix()},
				},
			},
			qname:              "default",
			ids:                []string{t1.ID, t2.ID},
			wantExpirationTime: now.Add(LeaseDuration),
			wantLease: map[string][]base.Z{
				"default": {
					{Message: t1, Score: now.Add(LeaseDuration).Unix()},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllLease(t, r.client, tc.lease)

		gotExpirationTime, err := r.ExtendLease(tc.qname, tc.ids...)
		if err != nil {
			t.Fatalf("%s: ExtendLease(%q, %v) returned error: %v", tc.desc, tc.qname, tc.ids, err)
		}
		if gotExpirationTime != tc.wantExpirationTime {
			t.Errorf("%s: ExtendLease(%q, %v) returned expirationTime %v, want %v", tc.desc, tc.qname, tc.ids, gotExpirationTime, tc.wantExpirationTime)
		}

		for qname, want := range tc.wantLease {
			gotLease := h.GetLeaseEntries(t, r.client, qname)
			if diff := cmp.Diff(want, gotLease, h.SortZSetEntryOpt); diff != "" {
				t.Errorf("%s: mismatch found in %q: (-want,+got):\n%s", tc.desc, base.LeaseKey(qname), diff)
			}
		}
	}
}

func TestWriteServerState(t *testing.T) {
	r := setup(t)
	defer r.Close()

	var (
		host     = "localhost"
		pid      = 4242
		serverID = "server123"

		ttl = 5 * time.Second
	)

	info := base.ServerInfo{
		Host:              host,
		PID:               pid,
		ServerID:          serverID,
		Concurrency:       10,
		Queues:            map[string]int{"default": 2, "email": 5, "low": 1},
		StrictPriority:    false,
		Started:           time.Now().UTC(),
		Status:            "active",
		ActiveWorkerCount: 0,
	}

	err := r.WriteServerState(&info, nil /* workers */, ttl)
	if err != nil {
		t.Errorf("r.WriteServerState returned an error: %v", err)
	}

	// Check ServerInfo was written correctly.
	skey := base.ServerInfoKey(host, pid, serverID)
	data := r.client.Get(context.Background(), skey).Val()
	got, err := base.DecodeServerInfo([]byte(data))
	if err != nil {
		t.Fatalf("could not decode server info: %v", err)
	}
	if diff := cmp.Diff(info, *got); diff != "" {
		t.Errorf("persisted ServerInfo was %v, want %v; (-want,+got)\n%s",
			got, info, diff)
	}
	// Check ServerInfo TTL was set correctly.
	gotTTL := r.client.TTL(context.Background(), skey).Val()
	if !cmp.Equal(ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
		t.Errorf("TTL of %q was %v, want %v", skey, gotTTL, ttl)
	}
	// Check ServerInfo key was added to the set all server keys correctly.
	gotServerKeys := r.client.ZRange(context.Background(), base.AllServers, 0, -1).Val()
	wantServerKeys := []string{skey}
	if diff := cmp.Diff(wantServerKeys, gotServerKeys); diff != "" {
		t.Errorf("%q contained %v, want %v", base.AllServers, gotServerKeys, wantServerKeys)
	}

	// Check WorkersInfo was written correctly.
	wkey := base.WorkersKey(host, pid, serverID)
	workerExist := r.client.Exists(context.Background(), wkey).Val()
	if workerExist != 0 {
		t.Errorf("%q key exists", wkey)
	}
	// Check WorkersInfo key was added to the set correctly.
	gotWorkerKeys := r.client.ZRange(context.Background(), base.AllWorkers, 0, -1).Val()
	wantWorkerKeys := []string{wkey}
	if diff := cmp.Diff(wantWorkerKeys, gotWorkerKeys); diff != "" {
		t.Errorf("%q contained %v, want %v", base.AllWorkers, gotWorkerKeys, wantWorkerKeys)
	}
}

func TestWriteServerStateWithWorkers(t *testing.T) {
	r := setup(t)
	defer r.Close()

	var (
		host     = "127.0.0.1"
		pid      = 4242
		serverID = "server123"

		msg1 = h.NewTaskMessage("send_email", h.JSON(map[string]interface{}{"user_id": "123"}))
		msg2 = h.NewTaskMessage("gen_thumbnail", h.JSON(map[string]interface{}{"path": "some/path/to/imgfile"}))

		ttl = 5 * time.Second
	)

	workers := []*base.WorkerInfo{
		{
			Host:    host,
			PID:     pid,
			ID:      msg1.ID,
			Type:    msg1.Type,
			Queue:   msg1.Queue,
			Payload: msg1.Payload,
			Started: time.Now().Add(-10 * time.Second),
		},
		{
			Host:    host,
			PID:     pid,
			ID:      msg2.ID,
			Type:    msg2.Type,
			Queue:   msg2.Queue,
			Payload: msg2.Payload,
			Started: time.Now().Add(-2 * time.Minute),
		},
	}

	serverInfo := base.ServerInfo{
		Host:              host,
		PID:               pid,
		ServerID:          serverID,
		Concurrency:       10,
		Queues:            map[string]int{"default": 2, "email": 5, "low": 1},
		StrictPriority:    false,
		Started:           time.Now().Add(-10 * time.Minute).UTC(),
		Status:            "active",
		ActiveWorkerCount: len(workers),
	}

	err := r.WriteServerState(&serverInfo, workers, ttl)
	if err != nil {
		t.Fatalf("r.WriteServerState returned an error: %v", err)
	}

	// Check ServerInfo was written correctly.
	skey := base.ServerInfoKey(host, pid, serverID)
	data := r.client.Get(context.Background(), skey).Val()
	got, err := base.DecodeServerInfo([]byte(data))
	if err != nil {
		t.Fatalf("could not decode server info: %v", err)
	}
	if diff := cmp.Diff(serverInfo, *got); diff != "" {
		t.Errorf("persisted ServerInfo was %v, want %v; (-want,+got)\n%s",
			got, serverInfo, diff)
	}
	// Check ServerInfo TTL was set correctly.
	gotTTL := r.client.TTL(context.Background(), skey).Val()
	if !cmp.Equal(ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
		t.Errorf("TTL of %q was %v, want %v", skey, gotTTL, ttl)
	}
	// Check ServerInfo key was added to the set correctly.
	gotServerKeys := r.client.ZRange(context.Background(), base.AllServers, 0, -1).Val()
	wantServerKeys := []string{skey}
	if diff := cmp.Diff(wantServerKeys, gotServerKeys); diff != "" {
		t.Errorf("%q contained %v, want %v", base.AllServers, gotServerKeys, wantServerKeys)
	}

	// Check WorkersInfo was written correctly.
	wkey := base.WorkersKey(host, pid, serverID)
	wdata := r.client.HGetAll(context.Background(), wkey).Val()
	if len(wdata) != 2 {
		t.Fatalf("HGETALL %q returned a hash of size %d, want 2", wkey, len(wdata))
	}
	var gotWorkers []*base.WorkerInfo
	for _, val := range wdata {
		w, err := base.DecodeWorkerInfo([]byte(val))
		if err != nil {
			t.Fatalf("could not unmarshal worker's data: %v", err)
		}
		gotWorkers = append(gotWorkers, w)
	}
	if diff := cmp.Diff(workers, gotWorkers, h.SortWorkerInfoOpt); diff != "" {
		t.Errorf("persisted workers info was %v, want %v; (-want,+got)\n%s",
			gotWorkers, workers, diff)
	}

	// Check WorkersInfo TTL was set correctly.
	gotTTL = r.client.TTL(context.Background(), wkey).Val()
	if !cmp.Equal(ttl.Seconds(), gotTTL.Seconds(), cmpopts.EquateApprox(0, 1)) {
		t.Errorf("TTL of %q was %v, want %v", wkey, gotTTL, ttl)
	}
	// Check WorkersInfo key was added to the set correctly.
	gotWorkerKeys := r.client.ZRange(context.Background(), base.AllWorkers, 0, -1).Val()
	wantWorkerKeys := []string{wkey}
	if diff := cmp.Diff(wantWorkerKeys, gotWorkerKeys); diff != "" {
		t.Errorf("%q contained %v, want %v", base.AllWorkers, gotWorkerKeys, wantWorkerKeys)
	}
}

func TestClearServerState(t *testing.T) {
	r := setup(t)
	defer r.Close()

	var (
		host     = "127.0.0.1"
		pid      = 1234
		serverID = "server123"

		otherHost     = "127.0.0.2"
		otherPID      = 9876
		otherServerID = "server987"

		msg1 = h.NewTaskMessage("send_email", h.JSON(map[string]interface{}{"user_id": "123"}))
		msg2 = h.NewTaskMessage("gen_thumbnail", h.JSON(map[string]interface{}{"path": "some/path/to/imgfile"}))

		ttl = 5 * time.Second
	)

	workers1 := []*base.WorkerInfo{
		{
			Host:    host,
			PID:     pid,
			ID:      msg1.ID,
			Type:    msg1.Type,
			Queue:   msg1.Queue,
			Payload: msg1.Payload,
			Started: time.Now().Add(-10 * time.Second),
		},
	}
	serverInfo1 := base.ServerInfo{
		Host:              host,
		PID:               pid,
		ServerID:          serverID,
		Concurrency:       10,
		Queues:            map[string]int{"default": 2, "email": 5, "low": 1},
		StrictPriority:    false,
		Started:           time.Now().Add(-10 * time.Minute),
		Status:            "active",
		ActiveWorkerCount: len(workers1),
	}

	workers2 := []*base.WorkerInfo{
		{
			Host:    otherHost,
			PID:     otherPID,
			ID:      msg2.ID,
			Type:    msg2.Type,
			Queue:   msg2.Queue,
			Payload: msg2.Payload,
			Started: time.Now().Add(-30 * time.Second),
		},
	}
	serverInfo2 := base.ServerInfo{
		Host:              otherHost,
		PID:               otherPID,
		ServerID:          otherServerID,
		Concurrency:       10,
		Queues:            map[string]int{"default": 2, "email": 5, "low": 1},
		StrictPriority:    false,
		Started:           time.Now().Add(-15 * time.Minute),
		Status:            "active",
		ActiveWorkerCount: len(workers2),
	}

	// Write server and workers data.
	if err := r.WriteServerState(&serverInfo1, workers1, ttl); err != nil {
		t.Fatalf("could not write server state: %v", err)
	}
	if err := r.WriteServerState(&serverInfo2, workers2, ttl); err != nil {
		t.Fatalf("could not write server state: %v", err)
	}

	err := r.ClearServerState(host, pid, serverID)
	if err != nil {
		t.Fatalf("(*RDB).ClearServerState failed: %v", err)
	}

	skey := base.ServerInfoKey(host, pid, serverID)
	wkey := base.WorkersKey(host, pid, serverID)
	otherSKey := base.ServerInfoKey(otherHost, otherPID, otherServerID)
	otherWKey := base.WorkersKey(otherHost, otherPID, otherServerID)
	// Check all keys are cleared.
	if r.client.Exists(context.Background(), skey).Val() != 0 {
		t.Errorf("Redis key %q exists", skey)
	}
	if r.client.Exists(context.Background(), wkey).Val() != 0 {
		t.Errorf("Redis key %q exists", wkey)
	}
	gotServerKeys := r.client.ZRange(context.Background(), base.AllServers, 0, -1).Val()
	wantServerKeys := []string{otherSKey}
	if diff := cmp.Diff(wantServerKeys, gotServerKeys); diff != "" {
		t.Errorf("%q contained %v, want %v", base.AllServers, gotServerKeys, wantServerKeys)
	}
	gotWorkerKeys := r.client.ZRange(context.Background(), base.AllWorkers, 0, -1).Val()
	wantWorkerKeys := []string{otherWKey}
	if diff := cmp.Diff(wantWorkerKeys, gotWorkerKeys); diff != "" {
		t.Errorf("%q contained %v, want %v", base.AllWorkers, gotWorkerKeys, wantWorkerKeys)
	}
}

func TestCancelationPubSub(t *testing.T) {
	r := setup(t)
	defer r.Close()

	pubsub, err := r.CancelationPubSub()
	if err != nil {
		t.Fatalf("(*RDB).CancelationPubSub() returned an error: %v", err)
	}

	cancelCh := pubsub.Channel()

	var (
		mu       sync.Mutex
		received []string
	)

	go func() {
		for msg := range cancelCh {
			mu.Lock()
			received = append(received, msg.Payload)
			mu.Unlock()
		}
	}()

	publish := []string{"one", "two", "three"}

	for _, msg := range publish {
		_ = r.PublishCancelation(msg)
	}

	// allow for message to reach subscribers.
	time.Sleep(time.Second)

	pubsub.Close()

	mu.Lock()
	if diff := cmp.Diff(publish, received, h.SortStringSliceOpt); diff != "" {
		t.Errorf("subscriber received %v, want %v; (-want,+got)\n%s", received, publish, diff)
	}
	mu.Unlock()
}

func TestWriteResult(t *testing.T) {
	r := setup(t)
	defer r.Close()

	tests := []struct {
		qname  string
		taskID string
		data   []byte
	}{
		{
			qname:  "default",
			taskID: uuid.NewString(),
			data:   []byte("hello"),
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		n, err := r.WriteResult(tc.qname, tc.taskID, tc.data)
		if err != nil {
			t.Errorf("WriteResult failed: %v", err)
			continue
		}
		if n != len(tc.data) {
			t.Errorf("WriteResult returned %d, want %d", n, len(tc.data))
		}

		taskKey := base.TaskKey(tc.qname, tc.taskID)
		got := r.client.HGet(context.Background(), taskKey, "result").Val()
		if got != string(tc.data) {
			t.Errorf("`result` field under %q key is set to %q, want %q", taskKey, got, string(tc.data))
		}
	}
}

func TestAggregationCheck(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	ctx := context.Background()
	msg1 := h.NewTaskMessageBuilder().SetType("task1").SetGroup("mygroup").Build()
	msg2 := h.NewTaskMessageBuilder().SetType("task2").SetGroup("mygroup").Build()
	msg3 := h.NewTaskMessageBuilder().SetType("task3").SetGroup("mygroup").Build()
	msg4 := h.NewTaskMessageBuilder().SetType("task4").SetGroup("mygroup").Build()
	msg5 := h.NewTaskMessageBuilder().SetType("task5").SetGroup("mygroup").Build()

	tests := []struct {
		desc string
		// initial data
		tasks     []*h.TaskSeedData
		groups    map[string][]redis.Z
		allGroups map[string][]string

		// args
		qname       string
		gname       string
		gracePeriod time.Duration
		maxDelay    time.Duration
		maxSize     int

		// expectaions
		shouldCreateSet    bool // whether the check should create a new aggregation set
		wantAggregationSet []*base.TaskMessage
		wantGroups         map[string][]redis.Z
		shouldClearGroup   bool // whether the check should clear the group from redis
	}{
		{
			desc:  "with an empty group",
			tasks: []*h.TaskSeedData{},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        1 * time.Minute,
			maxDelay:           10 * time.Minute,
			maxSize:            5,
			shouldCreateSet:    false,
			wantAggregationSet: nil,
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {},
			},
			shouldClearGroup: true,
		},
		{
			desc: "with a group size reaching the max size",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
				{Msg: msg4, State: base.TaskStateAggregating},
				{Msg: msg5, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        1 * time.Minute,
			maxDelay:           10 * time.Minute,
			maxSize:            5,
			shouldCreateSet:    true,
			wantAggregationSet: []*base.TaskMessage{msg1, msg2, msg3, msg4, msg5},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {},
			},
			shouldClearGroup: true,
		},
		{
			desc: "with group size greater than max size",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
				{Msg: msg4, State: base.TaskStateAggregating},
				{Msg: msg5, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        2 * time.Minute,
			maxDelay:           10 * time.Minute,
			maxSize:            3,
			shouldCreateSet:    true,
			wantAggregationSet: []*base.TaskMessage{msg1, msg2, msg3},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			shouldClearGroup: false,
		},
		{
			desc: "with the most recent task older than grace period",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        1 * time.Minute,
			maxDelay:           10 * time.Minute,
			maxSize:            5,
			shouldCreateSet:    true,
			wantAggregationSet: []*base.TaskMessage{msg1, msg2, msg3},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {},
			},
			shouldClearGroup: true,
		},
		{
			desc: "with the oldest task older than max delay",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
				{Msg: msg4, State: base.TaskStateAggregating},
				{Msg: msg5, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-15 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        2 * time.Minute,
			maxDelay:           10 * time.Minute,
			maxSize:            30,
			shouldCreateSet:    true,
			wantAggregationSet: []*base.TaskMessage{msg1, msg2, msg3, msg4, msg5},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {},
			},
			shouldClearGroup: true,
		},
		{
			desc: "with unlimited size",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
				{Msg: msg4, State: base.TaskStateAggregating},
				{Msg: msg5, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-15 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        1 * time.Minute,
			maxDelay:           30 * time.Minute,
			maxSize:            0, // maxSize=0 indicates no size limit
			shouldCreateSet:    false,
			wantAggregationSet: nil,
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-15 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			shouldClearGroup: false,
		},
		{
			desc: "with unlimited size and passed grace period",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
				{Msg: msg4, State: base.TaskStateAggregating},
				{Msg: msg5, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-15 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        30 * time.Second,
			maxDelay:           30 * time.Minute,
			maxSize:            0, // maxSize=0 indicates no size limit
			shouldCreateSet:    true,
			wantAggregationSet: []*base.TaskMessage{msg1, msg2, msg3, msg4, msg5},
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {},
			},
			shouldClearGroup: true,
		},
		{
			desc: "with unlimited delay",
			tasks: []*h.TaskSeedData{
				{Msg: msg1, State: base.TaskStateAggregating},
				{Msg: msg2, State: base.TaskStateAggregating},
				{Msg: msg3, State: base.TaskStateAggregating},
				{Msg: msg4, State: base.TaskStateAggregating},
				{Msg: msg5, State: base.TaskStateAggregating},
			},
			groups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-15 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			allGroups: map[string][]string{
				base.AllGroups("default"): {"mygroup"},
			},
			qname:              "default",
			gname:              "mygroup",
			gracePeriod:        1 * time.Minute,
			maxDelay:           0, // maxDelay=0 indicates no limit
			maxSize:            10,
			shouldCreateSet:    false,
			wantAggregationSet: nil,
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "mygroup"): {
					{Member: msg1.ID, Score: float64(now.Add(-15 * time.Minute).Unix())},
					{Member: msg2.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: msg3.ID, Score: float64(now.Add(-2 * time.Minute).Unix())},
					{Member: msg4.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
					{Member: msg5.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			shouldClearGroup: false,
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)

		t.Run(tc.desc, func(t *testing.T) {
			h.SeedTasks(t, r.client, tc.tasks)
			h.SeedRedisZSets(t, r.client, tc.groups)
			h.SeedRedisSets(t, r.client, tc.allGroups)

			aggregationSetID, err := r.AggregationCheck(tc.qname, tc.gname, now, tc.gracePeriod, tc.maxDelay, tc.maxSize)
			if err != nil {
				t.Fatalf("AggregationCheck returned error: %v", err)
			}

			if !tc.shouldCreateSet && aggregationSetID != "" {
				t.Fatal("AggregationCheck returned non empty set ID. want empty ID")
			}
			if tc.shouldCreateSet && aggregationSetID == "" {
				t.Fatal("AggregationCheck returned empty set ID. want non empty ID")
			}

			if tc.shouldCreateSet {
				msgs, deadline, err := r.ReadAggregationSet(tc.qname, tc.gname, aggregationSetID)
				if err != nil {
					t.Fatalf("Failed to read aggregation set %q: %v", aggregationSetID, err)
				}
				if diff := cmp.Diff(tc.wantAggregationSet, msgs, h.SortMsgOpt); diff != "" {
					t.Errorf("Mismatch found in aggregation set: (-want,+got)\n%s", diff)
				}

				if wantDeadline := now.Add(aggregationTimeout); deadline.Unix() != wantDeadline.Unix() {
					t.Errorf("ReadAggregationSet returned deadline=%v, want=%v", deadline, wantDeadline)
				}
			}

			h.AssertRedisZSets(t, r.client, tc.wantGroups)

			if tc.shouldClearGroup {
				if key := base.GroupKey(tc.qname, tc.gname); r.client.Exists(ctx, key).Val() != 0 {
					t.Errorf("group key %q still exists", key)
				}
				if r.client.SIsMember(ctx, base.AllGroups(tc.qname), tc.gname).Val() {
					t.Errorf("all-group set %q still contains the group name %q", base.AllGroups(tc.qname), tc.gname)
				}
			} else {
				if key := base.GroupKey(tc.qname, tc.gname); r.client.Exists(ctx, key).Val() == 0 {
					t.Errorf("group key %q does not exists", key)
				}
				if !r.client.SIsMember(ctx, base.AllGroups(tc.qname), tc.gname).Val() {
					t.Errorf("all-group set %q doesn't contains the group name %q", base.AllGroups(tc.qname), tc.gname)
				}
			}
		})
	}
}

func TestDeleteAggregationSet(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	setID := uuid.NewString()
	otherSetID := uuid.NewString()
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("mygroup").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("mygroup").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("mygroup").Build()

	tests := []struct {
		desc string
		// initial data
		tasks              []*h.TaskSeedData
		aggregationSets    map[string][]redis.Z
		allAggregationSets map[string][]redis.Z

		// args
		ctx   context.Context
		qname string
		gname string
		setID string

		// expectations
		wantDeletedKeys        []string // redis key to check for non existence
		wantAggregationSets    map[string][]redis.Z
		wantAllAggregationSets map[string][]redis.Z
	}{
		{
			desc: "with a sigle active aggregation set",
			tasks: []*h.TaskSeedData{
				{Msg: m1, State: base.TaskStateAggregating},
				{Msg: m2, State: base.TaskStateAggregating},
				{Msg: m3, State: base.TaskStateAggregating},
			},
			aggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "mygroup", setID): {
					{Member: m1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
					{Member: m3.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
				},
			},
			allAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "mygroup", setID), Score: float64(now.Add(aggregationTimeout).Unix())},
				},
			},
			ctx:   context.Background(),
			qname: "default",
			gname: "mygroup",
			setID: setID,
			wantDeletedKeys: []string{
				base.AggregationSetKey("default", "mygroup", setID),
				base.TaskKey(m1.Queue, m1.ID),
				base.TaskKey(m2.Queue, m2.ID),
				base.TaskKey(m3.Queue, m3.ID),
			},
			wantAggregationSets: map[string][]redis.Z{},
			wantAllAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {},
			},
		},
		{
			desc: "with multiple active aggregation sets",
			tasks: []*h.TaskSeedData{
				{Msg: m1, State: base.TaskStateAggregating},
				{Msg: m2, State: base.TaskStateAggregating},
				{Msg: m3, State: base.TaskStateAggregating},
			},
			aggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "mygroup", setID): {
					{Member: m1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
				},
				base.AggregationSetKey("default", "mygroup", otherSetID): {
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
					{Member: m3.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
				},
			},
			allAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "mygroup", setID), Score: float64(now.Add(aggregationTimeout).Unix())},
					{Member: base.AggregationSetKey("default", "mygroup", otherSetID), Score: float64(now.Add(aggregationTimeout).Unix())},
				},
			},
			ctx:   context.Background(),
			qname: "default",
			gname: "mygroup",
			setID: setID,
			wantDeletedKeys: []string{
				base.AggregationSetKey("default", "mygroup", setID),
				base.TaskKey(m1.Queue, m1.ID),
			},
			wantAggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "mygroup", otherSetID): {
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
					{Member: m3.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
				},
			},
			wantAllAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "mygroup", otherSetID), Score: float64(now.Add(aggregationTimeout).Unix())},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		t.Run(tc.desc, func(t *testing.T) {
			h.SeedTasks(t, r.client, tc.tasks)
			h.SeedRedisZSets(t, r.client, tc.aggregationSets)
			h.SeedRedisZSets(t, r.client, tc.allAggregationSets)

			if err := r.DeleteAggregationSet(tc.ctx, tc.qname, tc.gname, tc.setID); err != nil {
				t.Fatalf("DeleteAggregationSet returned error: %v", err)
			}

			for _, key := range tc.wantDeletedKeys {
				if r.client.Exists(context.Background(), key).Val() != 0 {
					t.Errorf("key=%q still exists, want deleted", key)
				}
			}
			h.AssertRedisZSets(t, r.client, tc.wantAllAggregationSets)
		})
	}
}

func TestDeleteAggregationSetError(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	setID := uuid.NewString()
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("mygroup").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("mygroup").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("mygroup").Build()
	deadlineExceededCtx, cancel := context.WithDeadline(context.Background(), now.Add(-10*time.Second))
	defer cancel()

	tests := []struct {
		desc string
		// initial data
		tasks              []*h.TaskSeedData
		aggregationSets    map[string][]redis.Z
		allAggregationSets map[string][]redis.Z

		// args
		ctx   context.Context
		qname string
		gname string
		setID string

		// expectations
		wantAggregationSets    map[string][]redis.Z
		wantAllAggregationSets map[string][]redis.Z
	}{
		{
			desc: "with deadline exceeded context",
			tasks: []*h.TaskSeedData{
				{Msg: m1, State: base.TaskStateAggregating},
				{Msg: m2, State: base.TaskStateAggregating},
				{Msg: m3, State: base.TaskStateAggregating},
			},
			aggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "mygroup", setID): {
					{Member: m1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
					{Member: m3.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
				},
			},
			allAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "mygroup", setID), Score: float64(now.Add(aggregationTimeout).Unix())},
				},
			},
			ctx:   deadlineExceededCtx,
			qname: "default",
			gname: "mygroup",
			setID: setID,
			// want data unchanged.
			wantAggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "mygroup", setID): {
					{Member: m1.ID, Score: float64(now.Add(-5 * time.Minute).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
					{Member: m3.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
				},
			},
			// want data unchanged.
			wantAllAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "mygroup", setID), Score: float64(now.Add(aggregationTimeout).Unix())},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		t.Run(tc.desc, func(t *testing.T) {
			h.SeedTasks(t, r.client, tc.tasks)
			h.SeedRedisZSets(t, r.client, tc.aggregationSets)
			h.SeedRedisZSets(t, r.client, tc.allAggregationSets)

			if err := r.DeleteAggregationSet(tc.ctx, tc.qname, tc.gname, tc.setID); err == nil {
				t.Fatal("DeleteAggregationSet returned nil, want non-nil error")
			}

			// Make sure zsets are unchanged.
			h.AssertRedisZSets(t, r.client, tc.wantAggregationSets)
			h.AssertRedisZSets(t, r.client, tc.wantAllAggregationSets)
		})
	}
}

func TestReclaimStaleAggregationSets(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	r.SetClock(timeutil.NewSimulatedClock(now))

	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("foo").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("foo").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("bar").Build()
	m4 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("qux").Build()

	// Note: In this test, we're trying out a new way to test RDB by exactly describing how
	// keys and values are represented in Redis.
	tests := []struct {
		groups                 map[string][]redis.Z // map redis-key to redis-zset
		aggregationSets        map[string][]redis.Z
		allAggregationSets     map[string][]redis.Z
		qname                  string
		wantGroups             map[string][]redis.Z
		wantAggregationSets    map[string][]redis.Z
		wantAllAggregationSets map[string][]redis.Z
	}{
		{
			groups: map[string][]redis.Z{
				base.GroupKey("default", "foo"): {},
				base.GroupKey("default", "bar"): {},
				base.GroupKey("default", "qux"): {
					{Member: m4.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			aggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "foo", "set1"): {
					{Member: m1.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
				},
				base.AggregationSetKey("default", "bar", "set2"): {
					{Member: m3.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
				},
			},
			allAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "foo", "set1"), Score: float64(now.Add(-10 * time.Second).Unix())}, // set1 is expired
					{Member: base.AggregationSetKey("default", "bar", "set2"), Score: float64(now.Add(40 * time.Second).Unix())},  // set2 is not expired
				},
			},
			qname: "default",
			wantGroups: map[string][]redis.Z{
				base.GroupKey("default", "foo"): {
					{Member: m1.ID, Score: float64(now.Add(-3 * time.Minute).Unix())},
					{Member: m2.ID, Score: float64(now.Add(-4 * time.Minute).Unix())},
				},
				base.GroupKey("default", "bar"): {},
				base.GroupKey("default", "qux"): {
					{Member: m4.ID, Score: float64(now.Add(-10 * time.Second).Unix())},
				},
			},
			wantAggregationSets: map[string][]redis.Z{
				base.AggregationSetKey("default", "bar", "set2"): {
					{Member: m3.ID, Score: float64(now.Add(-1 * time.Minute).Unix())},
				},
			},
			wantAllAggregationSets: map[string][]redis.Z{
				base.AllAggregationSets("default"): {
					{Member: base.AggregationSetKey("default", "bar", "set2"), Score: float64(now.Add(40 * time.Second).Unix())},
				},
			},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedRedisZSets(t, r.client, tc.groups)
		h.SeedRedisZSets(t, r.client, tc.aggregationSets)
		h.SeedRedisZSets(t, r.client, tc.allAggregationSets)

		if err := r.ReclaimStaleAggregationSets(tc.qname); err != nil {
			t.Errorf("ReclaimStaleAggregationSets returned error: %v", err)
			continue
		}

		h.AssertRedisZSets(t, r.client, tc.wantGroups)
		h.AssertRedisZSets(t, r.client, tc.wantAggregationSets)
		h.AssertRedisZSets(t, r.client, tc.wantAllAggregationSets)
	}
}

func TestListGroups(t *testing.T) {
	r := setup(t)
	defer r.Close()

	now := time.Now()
	m1 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("foo").Build()
	m2 := h.NewTaskMessageBuilder().SetQueue("default").SetGroup("bar").Build()
	m3 := h.NewTaskMessageBuilder().SetQueue("custom").SetGroup("baz").Build()
	m4 := h.NewTaskMessageBuilder().SetQueue("custom").SetGroup("qux").Build()

	tests := []struct {
		groups map[string]map[string][]base.Z
		qname  string
		want   []string
	}{
		{
			groups: map[string]map[string][]base.Z{
				"default": {
					"foo": {{Message: m1, Score: now.Add(-10 * time.Second).Unix()}},
					"bar": {{Message: m2, Score: now.Add(-10 * time.Second).Unix()}},
				},
				"custom": {
					"baz": {{Message: m3, Score: now.Add(-10 * time.Second).Unix()}},
					"qux": {{Message: m4, Score: now.Add(-10 * time.Second).Unix()}},
				},
			},
			qname: "default",
			want:  []string{"foo", "bar"},
		},
		{
			groups: map[string]map[string][]base.Z{
				"default": {
					"foo": {{Message: m1, Score: now.Add(-10 * time.Second).Unix()}},
					"bar": {{Message: m2, Score: now.Add(-10 * time.Second).Unix()}},
				},
				"custom": {
					"baz": {{Message: m3, Score: now.Add(-10 * time.Second).Unix()}},
					"qux": {{Message: m4, Score: now.Add(-10 * time.Second).Unix()}},
				},
			},
			qname: "custom",
			want:  []string{"baz", "qux"},
		},
	}

	for _, tc := range tests {
		h.FlushDB(t, r.client)
		h.SeedAllGroups(t, r.client, tc.groups)

		got, err := r.ListGroups(tc.qname)
		if err != nil {
			t.Errorf("ListGroups returned error: %v", err)
			continue
		}

		if diff := cmp.Diff(tc.want, got, h.SortStringSliceOpt); diff != "" {
			t.Errorf("ListGroups=%v, want=%v; (-want,+got)\n%s", got, tc.want, diff)
		}
	}
}
