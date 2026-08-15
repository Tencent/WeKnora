// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package base

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/timeutil"
)

func TestTaskKey(t *testing.T) {
	id := uuid.NewString()

	tests := []struct {
		qname string
		id    string
		want  string
	}{
		{"default", id, fmt.Sprintf("asynq:{default}:t:%s", id)},
	}

	for _, tc := range tests {
		got := TaskKey(tc.qname, tc.id)
		if got != tc.want {
			t.Errorf("TaskKey(%q, %s) = %q, want %q", tc.qname, tc.id, got, tc.want)
		}
	}
}

func TestQueueKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:pending"},
		{"custom", "asynq:{custom}:pending"},
	}

	for _, tc := range tests {
		got := PendingKey(tc.qname)
		if got != tc.want {
			t.Errorf("QueueKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestActiveKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:active"},
		{"custom", "asynq:{custom}:active"},
	}

	for _, tc := range tests {
		got := ActiveKey(tc.qname)
		if got != tc.want {
			t.Errorf("ActiveKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestLeaseKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:lease"},
		{"custom", "asynq:{custom}:lease"},
	}

	for _, tc := range tests {
		got := LeaseKey(tc.qname)
		if got != tc.want {
			t.Errorf("LeaseKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestScheduledKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:scheduled"},
		{"custom", "asynq:{custom}:scheduled"},
	}

	for _, tc := range tests {
		got := ScheduledKey(tc.qname)
		if got != tc.want {
			t.Errorf("ScheduledKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestRetryKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:retry"},
		{"custom", "asynq:{custom}:retry"},
	}

	for _, tc := range tests {
		got := RetryKey(tc.qname)
		if got != tc.want {
			t.Errorf("RetryKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestArchivedKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:archived"},
		{"custom", "asynq:{custom}:archived"},
	}

	for _, tc := range tests {
		got := ArchivedKey(tc.qname)
		if got != tc.want {
			t.Errorf("ArchivedKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestCompletedKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:completed"},
		{"custom", "asynq:{custom}:completed"},
	}

	for _, tc := range tests {
		got := CompletedKey(tc.qname)
		if got != tc.want {
			t.Errorf("CompletedKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestPausedKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:paused"},
		{"custom", "asynq:{custom}:paused"},
	}

	for _, tc := range tests {
		got := PausedKey(tc.qname)
		if got != tc.want {
			t.Errorf("PausedKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestProcessedTotalKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:processed"},
		{"custom", "asynq:{custom}:processed"},
	}

	for _, tc := range tests {
		got := ProcessedTotalKey(tc.qname)
		if got != tc.want {
			t.Errorf("ProcessedTotalKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestFailedTotalKey(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{"default", "asynq:{default}:failed"},
		{"custom", "asynq:{custom}:failed"},
	}

	for _, tc := range tests {
		got := FailedTotalKey(tc.qname)
		if got != tc.want {
			t.Errorf("FailedTotalKey(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestProcessedKey(t *testing.T) {
	tests := []struct {
		qname string
		input time.Time
		want  string
	}{
		{"default", time.Date(2019, 11, 14, 10, 30, 1, 1, time.UTC), "asynq:{default}:processed:2019-11-14"},
		{"critical", time.Date(2020, 12, 1, 1, 0, 1, 1, time.UTC), "asynq:{critical}:processed:2020-12-01"},
		{"default", time.Date(2020, 1, 6, 15, 02, 1, 1, time.UTC), "asynq:{default}:processed:2020-01-06"},
	}

	for _, tc := range tests {
		got := ProcessedKey(tc.qname, tc.input)
		if got != tc.want {
			t.Errorf("ProcessedKey(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFailedKey(t *testing.T) {
	tests := []struct {
		qname string
		input time.Time
		want  string
	}{
		{"default", time.Date(2019, 11, 14, 10, 30, 1, 1, time.UTC), "asynq:{default}:failed:2019-11-14"},
		{"custom", time.Date(2020, 12, 1, 1, 0, 1, 1, time.UTC), "asynq:{custom}:failed:2020-12-01"},
		{"low", time.Date(2020, 1, 6, 15, 02, 1, 1, time.UTC), "asynq:{low}:failed:2020-01-06"},
	}

	for _, tc := range tests {
		got := FailedKey(tc.qname, tc.input)
		if got != tc.want {
			t.Errorf("FailureKey(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestServerInfoKey(t *testing.T) {
	tests := []struct {
		hostname string
		pid      int
		sid      string
		want     string
	}{
		{"localhost", 9876, "server123", "asynq:servers:{localhost:9876:server123}"},
		{"127.0.0.1", 1234, "server987", "asynq:servers:{127.0.0.1:1234:server987}"},
	}

	for _, tc := range tests {
		got := ServerInfoKey(tc.hostname, tc.pid, tc.sid)
		if got != tc.want {
			t.Errorf("ServerInfoKey(%q, %d, %q) = %q, want %q",
				tc.hostname, tc.pid, tc.sid, got, tc.want)
		}
	}
}

func TestWorkersKey(t *testing.T) {
	tests := []struct {
		hostname string
		pid      int
		sid      string
		want     string
	}{
		{"localhost", 9876, "server1", "asynq:workers:{localhost:9876:server1}"},
		{"127.0.0.1", 1234, "server2", "asynq:workers:{127.0.0.1:1234:server2}"},
	}

	for _, tc := range tests {
		got := WorkersKey(tc.hostname, tc.pid, tc.sid)
		if got != tc.want {
			t.Errorf("WorkersKey(%q, %d, %q) = %q, want = %q",
				tc.hostname, tc.pid, tc.sid, got, tc.want)
		}
	}
}

func TestSchedulerEntriesKey(t *testing.T) {
	tests := []struct {
		schedulerID string
		want        string
	}{
		{"localhost:9876:scheduler123", "asynq:schedulers:{localhost:9876:scheduler123}"},
		{"127.0.0.1:1234:scheduler987", "asynq:schedulers:{127.0.0.1:1234:scheduler987}"},
	}

	for _, tc := range tests {
		got := SchedulerEntriesKey(tc.schedulerID)
		if got != tc.want {
			t.Errorf("SchedulerEntriesKey(%q) = %q, want %q", tc.schedulerID, got, tc.want)
		}
	}
}

func TestSchedulerHistoryKey(t *testing.T) {
	tests := []struct {
		entryID string
		want    string
	}{
		{"entry876", "asynq:scheduler_history:entry876"},
		{"entry345", "asynq:scheduler_history:entry345"},
	}

	for _, tc := range tests {
		got := SchedulerHistoryKey(tc.entryID)
		if got != tc.want {
			t.Errorf("SchedulerHistoryKey(%q) = %q, want %q",
				tc.entryID, got, tc.want)
		}
	}
}

func toBytes(m map[string]interface{}) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func TestUniqueKey(t *testing.T) {
	payload1 := toBytes(map[string]interface{}{"a": 123, "b": "hello", "c": true})
	payload2 := toBytes(map[string]interface{}{"b": "hello", "c": true, "a": 123})
	payload3 := toBytes(map[string]interface{}{
		"address": map[string]string{"line": "123 Main St", "city": "Boston", "state": "MA"},
		"names":   []string{"bob", "mike", "rob"}})
	payload4 := toBytes(map[string]interface{}{
		"time":     time.Date(2020, time.July, 28, 0, 0, 0, 0, time.UTC),
		"duration": time.Hour})

	checksum := func(data []byte) string {
		sum := md5.Sum(data)
		return hex.EncodeToString(sum[:])
	}
	tests := []struct {
		desc     string
		qname    string
		tasktype string
		payload  []byte
		want     string
	}{
		{
			"with primitive types",
			"default",
			"email:send",
			payload1,
			fmt.Sprintf("asynq:{default}:unique:email:send:%s", checksum(payload1)),
		},
		{
			"with unsorted keys",
			"default",
			"email:send",
			payload2,
			fmt.Sprintf("asynq:{default}:unique:email:send:%s", checksum(payload2)),
		},
		{
			"with composite types",
			"default",
			"email:send",
			payload3,
			fmt.Sprintf("asynq:{default}:unique:email:send:%s", checksum(payload3)),
		},
		{
			"with complex types",
			"default",
			"email:send",
			payload4,
			fmt.Sprintf("asynq:{default}:unique:email:send:%s", checksum(payload4)),
		},
		{
			"with nil payload",
			"default",
			"reindex",
			nil,
			"asynq:{default}:unique:reindex:",
		},
	}

	for _, tc := range tests {
		got := UniqueKey(tc.qname, tc.tasktype, tc.payload)
		if got != tc.want {
			t.Errorf("%s: UniqueKey(%q, %q, %v) = %q, want %q", tc.desc, tc.qname, tc.tasktype, tc.payload, got, tc.want)
		}
	}
}

func TestGroupKey(t *testing.T) {
	tests := []struct {
		qname string
		gkey  string
		want  string
	}{
		{
			qname: "default",
			gkey:  "mygroup",
			want:  "asynq:{default}:g:mygroup",
		},
		{
			qname: "custom",
			gkey:  "foo",
			want:  "asynq:{custom}:g:foo",
		},
	}

	for _, tc := range tests {
		got := GroupKey(tc.qname, tc.gkey)
		if got != tc.want {
			t.Errorf("GroupKey(%q, %q) = %q, want %q", tc.qname, tc.gkey, got, tc.want)
		}
	}
}

func TestAggregationSetKey(t *testing.T) {
	tests := []struct {
		qname string
		gname string
		setID string
		want  string
	}{
		{
			qname: "default",
			gname: "mygroup",
			setID: "12345",
			want:  "asynq:{default}:g:mygroup:12345",
		},
		{
			qname: "custom",
			gname: "foo",
			setID: "98765",
			want:  "asynq:{custom}:g:foo:98765",
		},
	}

	for _, tc := range tests {
		got := AggregationSetKey(tc.qname, tc.gname, tc.setID)
		if got != tc.want {
			t.Errorf("AggregationSetKey(%q, %q, %q) = %q, want %q", tc.qname, tc.gname, tc.setID, got, tc.want)
		}
	}
}

func TestAllGroups(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{
			qname: "default",
			want:  "asynq:{default}:groups",
		},
		{
			qname: "custom",
			want:  "asynq:{custom}:groups",
		},
	}

	for _, tc := range tests {
		got := AllGroups(tc.qname)
		if got != tc.want {
			t.Errorf("AllGroups(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestAllAggregationSets(t *testing.T) {
	tests := []struct {
		qname string
		want  string
	}{
		{
			qname: "default",
			want:  "asynq:{default}:aggregation_sets",
		},
		{
			qname: "custom",
			want:  "asynq:{custom}:aggregation_sets",
		},
	}

	for _, tc := range tests {
		got := AllAggregationSets(tc.qname)
		if got != tc.want {
			t.Errorf("AllAggregationSets(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestMessageEncoding(t *testing.T) {
	id := uuid.NewString()
	tests := []struct {
		in  *TaskMessage
		out *TaskMessage
	}{
		{
			in: &TaskMessage{
				Type:      "task1",
				Payload:   toBytes(map[string]interface{}{"a": 1, "b": "hello!", "c": true}),
				ID:        id,
				Queue:     "default",
				GroupKey:  "mygroup",
				Retry:     10,
				Retried:   0,
				Timeout:   1800,
				Deadline:  1692311100,
				Retention: 3600,
			},
			out: &TaskMessage{
				Type:      "task1",
				Payload:   toBytes(map[string]interface{}{"a": json.Number("1"), "b": "hello!", "c": true}),
				ID:        id,
				Queue:     "default",
				GroupKey:  "mygroup",
				Retry:     10,
				Retried:   0,
				Timeout:   1800,
				Deadline:  1692311100,
				Retention: 3600,
			},
		},
	}

	for _, tc := range tests {
		encoded, err := EncodeMessage(tc.in)
		if err != nil {
			t.Errorf("EncodeMessage(msg) returned error: %v", err)
			continue
		}
		decoded, err := DecodeMessage(encoded)
		if err != nil {
			t.Errorf("DecodeMessage(encoded) returned error: %v", err)
			continue
		}
		if diff := cmp.Diff(tc.out, decoded); diff != "" {
			t.Errorf("Decoded message == %+v, want %+v;(-want,+got)\n%s",
				decoded, tc.out, diff)
		}
	}
}

func TestServerInfoEncoding(t *testing.T) {
	tests := []struct {
		info ServerInfo
	}{
		{
			info: ServerInfo{
				Host:              "127.0.0.1",
				PID:               9876,
				ServerID:          "abc123",
				Concurrency:       10,
				Queues:            map[string]int{"default": 1, "critical": 2},
				StrictPriority:    false,
				Status:            "active",
				Started:           time.Now().Add(-3 * time.Hour),
				ActiveWorkerCount: 8,
			},
		},
	}

	for _, tc := range tests {
		encoded, err := EncodeServerInfo(&tc.info)
		if err != nil {
			t.Errorf("EncodeServerInfo(info) returned error: %v", err)
			continue
		}
		decoded, err := DecodeServerInfo(encoded)
		if err != nil {
			t.Errorf("DecodeServerInfo(encoded) returned error: %v", err)
			continue
		}
		if diff := cmp.Diff(&tc.info, decoded); diff != "" {
			t.Errorf("Decoded ServerInfo == %+v, want %+v;(-want,+got)\n%s",
				decoded, tc.info, diff)
		}
	}
}

func TestWorkerInfoEncoding(t *testing.T) {
	tests := []struct {
		info WorkerInfo
	}{
		{
			info: WorkerInfo{
				Host:     "127.0.0.1",
				PID:      9876,
				ServerID: "abc123",
				ID:       uuid.NewString(),
				Type:     "taskA",
				Payload:  toBytes(map[string]interface{}{"foo": "bar"}),
				Queue:    "default",
				Started:  time.Now().Add(-3 * time.Hour),
				Deadline: time.Now().Add(30 * time.Second),
			},
		},
	}

	for _, tc := range tests {
		encoded, err := EncodeWorkerInfo(&tc.info)
		if err != nil {
			t.Errorf("EncodeWorkerInfo(info) returned error: %v", err)
			continue
		}
		decoded, err := DecodeWorkerInfo(encoded)
		if err != nil {
			t.Errorf("DecodeWorkerInfo(encoded) returned error: %v", err)
			continue
		}
		if diff := cmp.Diff(&tc.info, decoded); diff != "" {
			t.Errorf("Decoded WorkerInfo == %+v, want %+v;(-want,+got)\n%s",
				decoded, tc.info, diff)
		}
	}
}

func TestSchedulerEntryEncoding(t *testing.T) {
	tests := []struct {
		entry SchedulerEntry
	}{
		{
			entry: SchedulerEntry{
				ID:      uuid.NewString(),
				Spec:    "* * * * *",
				Type:    "task_A",
				Payload: toBytes(map[string]interface{}{"foo": "bar"}),
				Opts:    []string{"Queue('email')"},
				Next:    time.Now().Add(30 * time.Second).UTC(),
				Prev:    time.Now().Add(-2 * time.Minute).UTC(),
			},
		},
	}

	for _, tc := range tests {
		encoded, err := EncodeSchedulerEntry(&tc.entry)
		if err != nil {
			t.Errorf("EncodeSchedulerEntry(entry) returned error: %v", err)
			continue
		}
		decoded, err := DecodeSchedulerEntry(encoded)
		if err != nil {
			t.Errorf("DecodeSchedulerEntry(encoded) returned error: %v", err)
			continue
		}
		if diff := cmp.Diff(&tc.entry, decoded); diff != "" {
			t.Errorf("Decoded SchedulerEntry == %+v, want %+v;(-want,+got)\n%s",
				decoded, tc.entry, diff)
		}
	}
}

func TestSchedulerEnqueueEventEncoding(t *testing.T) {
	tests := []struct {
		event SchedulerEnqueueEvent
	}{
		{
			event: SchedulerEnqueueEvent{
				TaskID:     uuid.NewString(),
				EnqueuedAt: time.Now().Add(-30 * time.Second).UTC(),
			},
		},
	}

	for _, tc := range tests {
		encoded, err := EncodeSchedulerEnqueueEvent(&tc.event)
		if err != nil {
			t.Errorf("EncodeSchedulerEnqueueEvent(event) returned error: %v", err)
			continue
		}
		decoded, err := DecodeSchedulerEnqueueEvent(encoded)
		if err != nil {
			t.Errorf("DecodeSchedulerEnqueueEvent(encoded) returned error: %v", err)
			continue
		}
		if diff := cmp.Diff(&tc.event, decoded); diff != "" {
			t.Errorf("Decoded SchedulerEnqueueEvent == %+v, want %+v;(-want,+got)\n%s",
				decoded, tc.event, diff)
		}
	}
}

// Test for cancelations being accessed by multiple goroutines.
// Run with -race flag to check for data race.
func TestCancelationsConcurrentAccess(t *testing.T) {
	c := NewCancelations()

	_, cancel1 := context.WithCancel(context.Background())
	_, cancel2 := context.WithCancel(context.Background())
	_, cancel3 := context.WithCancel(context.Background())
	var key1, key2, key3 = "key1", "key2", "key3"

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Add(key1, cancel1)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Add(key2, cancel2)
		time.Sleep(200 * time.Millisecond)
		c.Delete(key2)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Add(key3, cancel3)
	}()

	wg.Wait()

	_, ok := c.Get(key1)
	if !ok {
		t.Errorf("(*Cancelations).Get(%q) = _, false, want <function>, true", key1)
	}

	_, ok = c.Get(key2)
	if ok {
		t.Errorf("(*Cancelations).Get(%q) = _, true, want <nil>, false", key2)
	}
}

func TestLeaseReset(t *testing.T) {
	now := time.Now()
	clock := timeutil.NewSimulatedClock(now)

	l := NewLease(now.Add(30 * time.Second))
	l.Clock = clock

	// Check initial state
	if !l.IsValid() {
		t.Errorf("lease should be valid when expiration is set to a future time")
	}
	if want := now.Add(30 * time.Second); l.Deadline() != want {
		t.Errorf("Lease.Deadline() = %v, want %v", l.Deadline(), want)
	}

	// Test Reset
	if !l.Reset(now.Add(45 * time.Second)) {
		t.Fatalf("Lease.Reset returned false when extending")
	}
	if want := now.Add(45 * time.Second); l.Deadline() != want {
		t.Errorf("After Reset: Lease.Deadline() = %v, want %v", l.Deadline(), want)
	}

	clock.AdvanceTime(1 * time.Minute) // simulate lease expiration

	if l.IsValid() {
		t.Errorf("lease should be invalid after expiration")
	}

	// Reset should return false if lease is expired.
	if l.Reset(time.Now().Add(20 * time.Second)) {
		t.Errorf("Lease.Reset should return false after expiration")
	}
}

func TestLeaseNotifyExpiration(t *testing.T) {
	now := time.Now()
	clock := timeutil.NewSimulatedClock(now)

	l := NewLease(now.Add(30 * time.Second))
	l.Clock = clock

	select {
	case <-l.Done():
		t.Fatalf("Lease.Done() did not block")
	default:
	}

	if l.NotifyExpiration() {
		t.Fatalf("Lease.NotifyExpiration() should return false when lease is still valid")
	}

	clock.AdvanceTime(1 * time.Minute) // simulate lease expiration

	if l.IsValid() {
		t.Errorf("Lease should be invalid after expiration")
	}
	if !l.NotifyExpiration() {
		t.Errorf("Lease.NotifyExpiration() return return true after expiration")
	}
	if !l.NotifyExpiration() {
		t.Errorf("It should be leagal to call Lease.NotifyExpiration multiple times")
	}

	select {
	case <-l.Done():
		// expected
	default:
		t.Errorf("Lease.Done() blocked after call to Lease.NotifyExpiration()")
	}
}
