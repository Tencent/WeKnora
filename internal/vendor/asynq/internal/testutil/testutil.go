// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

// Package testutil defines test helpers for asynq and its internal packages.
package testutil

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/timeutil"
	"github.com/redis/go-redis/v9"
)

// EquateInt64Approx returns a Comparer option that treats int64 values
// to be equal if they are within the given margin.
func EquateInt64Approx(margin int64) cmp.Option {
	return cmp.Comparer(func(a, b int64) bool {
		return math.Abs(float64(a-b)) <= float64(margin)
	})
}

// SortMsgOpt is a cmp.Option to sort base.TaskMessage for comparing slice of task messages.
var SortMsgOpt = cmp.Transformer("SortTaskMessages", func(in []*base.TaskMessage) []*base.TaskMessage {
	out := append([]*base.TaskMessage(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
})

// SortZSetEntryOpt is an cmp.Option to sort ZSetEntry for comparing slice of zset entries.
var SortZSetEntryOpt = cmp.Transformer("SortZSetEntries", func(in []base.Z) []base.Z {
	out := append([]base.Z(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		return out[i].Message.ID < out[j].Message.ID
	})
	return out
})

// SortServerInfoOpt is a cmp.Option to sort base.ServerInfo for comparing slice of process info.
var SortServerInfoOpt = cmp.Transformer("SortServerInfo", func(in []*base.ServerInfo) []*base.ServerInfo {
	out := append([]*base.ServerInfo(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].PID < out[j].PID
	})
	return out
})

// SortWorkerInfoOpt is a cmp.Option to sort base.WorkerInfo for comparing slice of worker info.
var SortWorkerInfoOpt = cmp.Transformer("SortWorkerInfo", func(in []*base.WorkerInfo) []*base.WorkerInfo {
	out := append([]*base.WorkerInfo(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
})

// SortSchedulerEntryOpt is a cmp.Option to sort base.SchedulerEntry for comparing slice of entries.
var SortSchedulerEntryOpt = cmp.Transformer("SortSchedulerEntry", func(in []*base.SchedulerEntry) []*base.SchedulerEntry {
	out := append([]*base.SchedulerEntry(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		return out[i].Spec < out[j].Spec
	})
	return out
})

// SortSchedulerEnqueueEventOpt is a cmp.Option to sort base.SchedulerEnqueueEvent for comparing slice of events.
var SortSchedulerEnqueueEventOpt = cmp.Transformer("SortSchedulerEnqueueEvent", func(in []*base.SchedulerEnqueueEvent) []*base.SchedulerEnqueueEvent {
	out := append([]*base.SchedulerEnqueueEvent(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].EnqueuedAt.Unix() < out[j].EnqueuedAt.Unix()
	})
	return out
})

// SortStringSliceOpt is a cmp.Option to sort string slice.
var SortStringSliceOpt = cmp.Transformer("SortStringSlice", func(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
})

var SortRedisZSetEntryOpt = cmp.Transformer("SortZSetEntries", func(in []redis.Z) []redis.Z {
	out := append([]redis.Z(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		// TODO: If member is a comparable type (int, string, etc) compare by the member
		// Use generic comparable type here once update to go1.18
		if _, ok := out[i].Member.(string); ok {
			// If member is a string, compare the member
			return out[i].Member.(string) < out[j].Member.(string)
		}
		return out[i].Score < out[j].Score
	})
	return out
})

// IgnoreIDOpt is an cmp.Option to ignore ID field in task messages when comparing.
var IgnoreIDOpt = cmpopts.IgnoreFields(base.TaskMessage{}, "ID")

// NewTaskMessage returns a new instance of TaskMessage given a task type and payload.
func NewTaskMessage(taskType string, payload []byte) *base.TaskMessage {
	return NewTaskMessageWithQueue(taskType, payload, base.DefaultQueueName)
}

// NewTaskMessageWithQueue returns a new instance of TaskMessage given a
// task type, payload and queue name.
func NewTaskMessageWithQueue(taskType string, payload []byte, qname string) *base.TaskMessage {
	return &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     taskType,
		Queue:    qname,
		Retry:    25,
		Payload:  payload,
		Timeout:  1800, // default timeout of 30 mins
		Deadline: 0,    // no deadline
	}
}

// NewLeaseWithClock returns a new lease with the given expiration time and clock.
func NewLeaseWithClock(expirationTime time.Time, clock timeutil.Clock) *base.Lease {
	l := base.NewLease(expirationTime)
	l.Clock = clock
	return l
}

// JSON serializes the given key-value pairs into stream of bytes in JSON.
func JSON(kv map[string]interface{}) []byte {
	b, err := json.Marshal(kv)
	if err != nil {
		panic(err)
	}
	return b
}

// TaskMessageAfterRetry returns an updated copy of t after retry.
// It increments retry count and sets the error message and last_failed_at time.
func TaskMessageAfterRetry(t base.TaskMessage, errMsg string, failedAt time.Time) *base.TaskMessage {
	t.Retried = t.Retried + 1
	t.ErrorMsg = errMsg
	t.LastFailedAt = failedAt.Unix()
	return &t
}

// TaskMessageWithError returns an updated copy of t with the given error message.
func TaskMessageWithError(t base.TaskMessage, errMsg string, failedAt time.Time) *base.TaskMessage {
	t.ErrorMsg = errMsg
	t.LastFailedAt = failedAt.Unix()
	return &t
}

// TaskMessageWithCompletedAt returns an updated copy of t after completion.
func TaskMessageWithCompletedAt(t base.TaskMessage, completedAt time.Time) *base.TaskMessage {
	t.CompletedAt = completedAt.Unix()
	return &t
}

// MustMarshal marshals given task message and returns a json string.
// Calling test will fail if marshaling errors out.
func MustMarshal(tb testing.TB, msg *base.TaskMessage) string {
	tb.Helper()
	data, err := base.EncodeMessage(msg)
	if err != nil {
		tb.Fatal(err)
	}
	return string(data)
}

// MustUnmarshal unmarshals given string into task message struct.
// Calling test will fail if unmarshaling errors out.
func MustUnmarshal(tb testing.TB, data string) *base.TaskMessage {
	tb.Helper()
	msg, err := base.DecodeMessage([]byte(data))
	if err != nil {
		tb.Fatal(err)
	}
	return msg
}

// FlushDB deletes all the keys of the currently selected DB.
func FlushDB(tb testing.TB, r redis.UniversalClient) {
	tb.Helper()
	switch r := r.(type) {
	case *redis.Client:
		if err := r.FlushDB(context.Background()).Err(); err != nil {
			tb.Fatal(err)
		}
	case *redis.ClusterClient:
		err := r.ForEachMaster(context.Background(), func(ctx context.Context, c *redis.Client) error {
			if err := c.FlushAll(ctx).Err(); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			tb.Fatal(err)
		}
	}
}

// SeedPendingQueue initializes the specified queue with the given messages.
func SeedPendingQueue(tb testing.TB, r redis.UniversalClient, msgs []*base.TaskMessage, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisList(tb, r, base.PendingKey(qname), msgs, base.TaskStatePending)
}

// SeedActiveQueue initializes the active queue with the given messages.
func SeedActiveQueue(tb testing.TB, r redis.UniversalClient, msgs []*base.TaskMessage, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisList(tb, r, base.ActiveKey(qname), msgs, base.TaskStateActive)
}

// SeedScheduledQueue initializes the scheduled queue with the given messages.
func SeedScheduledQueue(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisZSet(tb, r, base.ScheduledKey(qname), entries, base.TaskStateScheduled)
}

// SeedRetryQueue initializes the retry queue with the given messages.
func SeedRetryQueue(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisZSet(tb, r, base.RetryKey(qname), entries, base.TaskStateRetry)
}

// SeedArchivedQueue initializes the archived queue with the given messages.
func SeedArchivedQueue(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisZSet(tb, r, base.ArchivedKey(qname), entries, base.TaskStateArchived)
}

// SeedLease initializes the lease set with the given entries.
func SeedLease(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisZSet(tb, r, base.LeaseKey(qname), entries, base.TaskStateActive)
}

// SeedCompletedQueue initializes the completed set with the given entries.
func SeedCompletedQueue(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisZSet(tb, r, base.CompletedKey(qname), entries, base.TaskStateCompleted)
}

// SeedGroup initializes the group with the given entries.
func SeedGroup(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname, gname string) {
	tb.Helper()
	ctx := context.Background()
	r.SAdd(ctx, base.AllQueues, qname)
	r.SAdd(ctx, base.AllGroups(qname), gname)
	seedRedisZSet(tb, r, base.GroupKey(qname, gname), entries, base.TaskStateAggregating)
}

func SeedAggregationSet(tb testing.TB, r redis.UniversalClient, entries []base.Z, qname, gname, setID string) {
	tb.Helper()
	r.SAdd(context.Background(), base.AllQueues, qname)
	seedRedisZSet(tb, r, base.AggregationSetKey(qname, gname, setID), entries, base.TaskStateAggregating)
}

// SeedAllPendingQueues initializes all of the specified queues with the given messages.
//
// pending maps a queue name to a list of messages.
func SeedAllPendingQueues(tb testing.TB, r redis.UniversalClient, pending map[string][]*base.TaskMessage) {
	tb.Helper()
	for q, msgs := range pending {
		SeedPendingQueue(tb, r, msgs, q)
	}
}

// SeedAllActiveQueues initializes all of the specified active queues with the given messages.
func SeedAllActiveQueues(tb testing.TB, r redis.UniversalClient, active map[string][]*base.TaskMessage) {
	tb.Helper()
	for q, msgs := range active {
		SeedActiveQueue(tb, r, msgs, q)
	}
}

// SeedAllScheduledQueues initializes all of the specified scheduled queues with the given entries.
func SeedAllScheduledQueues(tb testing.TB, r redis.UniversalClient, scheduled map[string][]base.Z) {
	tb.Helper()
	for q, entries := range scheduled {
		SeedScheduledQueue(tb, r, entries, q)
	}
}

// SeedAllRetryQueues initializes all of the specified retry queues with the given entries.
func SeedAllRetryQueues(tb testing.TB, r redis.UniversalClient, retry map[string][]base.Z) {
	tb.Helper()
	for q, entries := range retry {
		SeedRetryQueue(tb, r, entries, q)
	}
}

// SeedAllArchivedQueues initializes all of the specified archived queues with the given entries.
func SeedAllArchivedQueues(tb testing.TB, r redis.UniversalClient, archived map[string][]base.Z) {
	tb.Helper()
	for q, entries := range archived {
		SeedArchivedQueue(tb, r, entries, q)
	}
}

// SeedAllLease initializes all of the lease sets with the given entries.
func SeedAllLease(tb testing.TB, r redis.UniversalClient, lease map[string][]base.Z) {
	tb.Helper()
	for q, entries := range lease {
		SeedLease(tb, r, entries, q)
	}
}

// SeedAllCompletedQueues initializes all of the completed queues with the given entries.
func SeedAllCompletedQueues(tb testing.TB, r redis.UniversalClient, completed map[string][]base.Z) {
	tb.Helper()
	for q, entries := range completed {
		SeedCompletedQueue(tb, r, entries, q)
	}
}

// SeedAllGroups initializes all groups in all queues.
// The map maps queue names to group names which maps to a list of task messages and the time it was
// added to the group.
func SeedAllGroups(tb testing.TB, r redis.UniversalClient, groups map[string]map[string][]base.Z) {
	tb.Helper()
	for qname, g := range groups {
		for gname, entries := range g {
			SeedGroup(tb, r, entries, qname, gname)
		}
	}
}

func seedRedisList(tb testing.TB, c redis.UniversalClient, key string,
	msgs []*base.TaskMessage, state base.TaskState) {
	tb.Helper()
	for _, msg := range msgs {
		encoded := MustMarshal(tb, msg)
		if err := c.LPush(context.Background(), key, msg.ID).Err(); err != nil {
			tb.Fatal(err)
		}
		taskKey := base.TaskKey(msg.Queue, msg.ID)
		data := map[string]interface{}{
			"msg":        encoded,
			"state":      state.String(),
			"unique_key": msg.UniqueKey,
			"group":      msg.GroupKey,
		}
		if err := c.HSet(context.Background(), taskKey, data).Err(); err != nil {
			tb.Fatal(err)
		}
		if len(msg.UniqueKey) > 0 {
			err := c.SetNX(context.Background(), msg.UniqueKey, msg.ID, 1*time.Minute).Err()
			if err != nil {
				tb.Fatalf("Failed to set unique lock in redis: %v", err)
			}
		}
	}
}

func seedRedisZSet(tb testing.TB, c redis.UniversalClient, key string,
	items []base.Z, state base.TaskState) {
	tb.Helper()
	for _, item := range items {
		msg := item.Message
		encoded := MustMarshal(tb, msg)
		z := redis.Z{Member: msg.ID, Score: float64(item.Score)}
		if err := c.ZAdd(context.Background(), key, z).Err(); err != nil {
			tb.Fatal(err)
		}
		taskKey := base.TaskKey(msg.Queue, msg.ID)
		data := map[string]interface{}{
			"msg":        encoded,
			"state":      state.String(),
			"unique_key": msg.UniqueKey,
			"group":      msg.GroupKey,
		}
		if err := c.HSet(context.Background(), taskKey, data).Err(); err != nil {
			tb.Fatal(err)
		}
		if len(msg.UniqueKey) > 0 {
			err := c.SetNX(context.Background(), msg.UniqueKey, msg.ID, 1*time.Minute).Err()
			if err != nil {
				tb.Fatalf("Failed to set unique lock in redis: %v", err)
			}
		}
	}
}

// GetPendingMessages returns all pending messages in the given queue.
// It also asserts the state field of the task.
func GetPendingMessages(tb testing.TB, r redis.UniversalClient, qname string) []*base.TaskMessage {
	tb.Helper()
	return getMessagesFromList(tb, r, qname, base.PendingKey, base.TaskStatePending)
}

// GetActiveMessages returns all active messages in the given queue.
// It also asserts the state field of the task.
func GetActiveMessages(tb testing.TB, r redis.UniversalClient, qname string) []*base.TaskMessage {
	tb.Helper()
	return getMessagesFromList(tb, r, qname, base.ActiveKey, base.TaskStateActive)
}

// GetScheduledMessages returns all scheduled task messages in the given queue.
// It also asserts the state field of the task.
func GetScheduledMessages(tb testing.TB, r redis.UniversalClient, qname string) []*base.TaskMessage {
	tb.Helper()
	return getMessagesFromZSet(tb, r, qname, base.ScheduledKey, base.TaskStateScheduled)
}

// GetRetryMessages returns all retry messages in the given queue.
// It also asserts the state field of the task.
func GetRetryMessages(tb testing.TB, r redis.UniversalClient, qname string) []*base.TaskMessage {
	tb.Helper()
	return getMessagesFromZSet(tb, r, qname, base.RetryKey, base.TaskStateRetry)
}

// GetArchivedMessages returns all archived messages in the given queue.
// It also asserts the state field of the task.
func GetArchivedMessages(tb testing.TB, r redis.UniversalClient, qname string) []*base.TaskMessage {
	tb.Helper()
	return getMessagesFromZSet(tb, r, qname, base.ArchivedKey, base.TaskStateArchived)
}

// GetCompletedMessages returns all completed task messages in the given queue.
// It also asserts the state field of the task.
func GetCompletedMessages(tb testing.TB, r redis.UniversalClient, qname string) []*base.TaskMessage {
	tb.Helper()
	return getMessagesFromZSet(tb, r, qname, base.CompletedKey, base.TaskStateCompleted)
}

// GetScheduledEntries returns all scheduled messages and its score in the given queue.
// It also asserts the state field of the task.
func GetScheduledEntries(tb testing.TB, r redis.UniversalClient, qname string) []base.Z {
	tb.Helper()
	return getMessagesFromZSetWithScores(tb, r, qname, base.ScheduledKey, base.TaskStateScheduled)
}

// GetRetryEntries returns all retry messages and its score in the given queue.
// It also asserts the state field of the task.
func GetRetryEntries(tb testing.TB, r redis.UniversalClient, qname string) []base.Z {
	tb.Helper()
	return getMessagesFromZSetWithScores(tb, r, qname, base.RetryKey, base.TaskStateRetry)
}

// GetArchivedEntries returns all archived messages and its score in the given queue.
// It also asserts the state field of the task.
func GetArchivedEntries(tb testing.TB, r redis.UniversalClient, qname string) []base.Z {
	tb.Helper()
	return getMessagesFromZSetWithScores(tb, r, qname, base.ArchivedKey, base.TaskStateArchived)
}

// GetLeaseEntries returns all task IDs and its score in the lease set for the given queue.
// It also asserts the state field of the task.
func GetLeaseEntries(tb testing.TB, r redis.UniversalClient, qname string) []base.Z {
	tb.Helper()
	return getMessagesFromZSetWithScores(tb, r, qname, base.LeaseKey, base.TaskStateActive)
}

// GetCompletedEntries returns all completed messages and its score in the given queue.
// It also asserts the state field of the task.
func GetCompletedEntries(tb testing.TB, r redis.UniversalClient, qname string) []base.Z {
	tb.Helper()
	return getMessagesFromZSetWithScores(tb, r, qname, base.CompletedKey, base.TaskStateCompleted)
}

// GetGroupEntries returns all scheduled messages and its score in the given queue.
// It also asserts the state field of the task.
func GetGroupEntries(tb testing.TB, r redis.UniversalClient, qname, groupKey string) []base.Z {
	tb.Helper()
	return getMessagesFromZSetWithScores(tb, r, qname,
		func(qname string) string { return base.GroupKey(qname, groupKey) }, base.TaskStateAggregating)
}

// Retrieves all messages stored under `keyFn(qname)` key in redis list.
func getMessagesFromList(tb testing.TB, r redis.UniversalClient, qname string,
	keyFn func(qname string) string, state base.TaskState) []*base.TaskMessage {
	tb.Helper()
	ids := r.LRange(context.Background(), keyFn(qname), 0, -1).Val()
	var msgs []*base.TaskMessage
	for _, id := range ids {
		taskKey := base.TaskKey(qname, id)
		data := r.HGet(context.Background(), taskKey, "msg").Val()
		msgs = append(msgs, MustUnmarshal(tb, data))
		if gotState := r.HGet(context.Background(), taskKey, "state").Val(); gotState != state.String() {
			tb.Errorf("task (id=%q) is in %q state, want %v", id, gotState, state)
		}
	}
	return msgs
}

// Retrieves all messages stored under `keyFn(qname)` key in redis zset (sorted-set).
func getMessagesFromZSet(tb testing.TB, r redis.UniversalClient, qname string,
	keyFn func(qname string) string, state base.TaskState) []*base.TaskMessage {
	tb.Helper()
	ids := r.ZRange(context.Background(), keyFn(qname), 0, -1).Val()
	var msgs []*base.TaskMessage
	for _, id := range ids {
		taskKey := base.TaskKey(qname, id)
		msg := r.HGet(context.Background(), taskKey, "msg").Val()
		msgs = append(msgs, MustUnmarshal(tb, msg))
		if gotState := r.HGet(context.Background(), taskKey, "state").Val(); gotState != state.String() {
			tb.Errorf("task (id=%q) is in %q state, want %v", id, gotState, state)
		}
	}
	return msgs
}

// Retrieves all messages along with their scores stored under `keyFn(qname)` key in redis zset (sorted-set).
func getMessagesFromZSetWithScores(tb testing.TB, r redis.UniversalClient,
	qname string, keyFn func(qname string) string, state base.TaskState) []base.Z {
	tb.Helper()
	zs := r.ZRangeWithScores(context.Background(), keyFn(qname), 0, -1).Val()
	var res []base.Z
	for _, z := range zs {
		taskID := z.Member.(string)
		taskKey := base.TaskKey(qname, taskID)
		msg := r.HGet(context.Background(), taskKey, "msg").Val()
		res = append(res, base.Z{Message: MustUnmarshal(tb, msg), Score: int64(z.Score)})
		if gotState := r.HGet(context.Background(), taskKey, "state").Val(); gotState != state.String() {
			tb.Errorf("task (id=%q) is in %q state, want %v", taskID, gotState, state)
		}
	}
	return res
}

// TaskSeedData holds the data required to seed tasks under the task key in test.
type TaskSeedData struct {
	Msg          *base.TaskMessage
	State        base.TaskState
	PendingSince time.Time
}

func SeedTasks(tb testing.TB, r redis.UniversalClient, taskData []*TaskSeedData) {
	for _, data := range taskData {
		msg := data.Msg
		ctx := context.Background()
		key := base.TaskKey(msg.Queue, msg.ID)
		v := map[string]interface{}{
			"msg":        MustMarshal(tb, msg),
			"state":      data.State.String(),
			"unique_key": msg.UniqueKey,
			"group":      msg.GroupKey,
		}
		if !data.PendingSince.IsZero() {
			v["pending_since"] = data.PendingSince.Unix()
		}
		if err := r.HSet(ctx, key, v).Err(); err != nil {
			tb.Fatalf("Failed to write task data in redis: %v", err)
		}
		if len(msg.UniqueKey) > 0 {
			err := r.SetNX(ctx, msg.UniqueKey, msg.ID, 1*time.Minute).Err()
			if err != nil {
				tb.Fatalf("Failed to set unique lock in redis: %v", err)
			}
		}
	}
}

func SeedRedisZSets(tb testing.TB, r redis.UniversalClient, zsets map[string][]redis.Z) {
	for key, zs := range zsets {
		// FIXME: How come we can't simply do ZAdd(ctx, key, zs...) here?
		for _, z := range zs {
			if err := r.ZAdd(context.Background(), key, z).Err(); err != nil {
				tb.Fatalf("Failed to seed zset (key=%q): %v", key, err)
			}
		}
	}
}

func SeedRedisSets(tb testing.TB, r redis.UniversalClient, sets map[string][]string) {
	for key, set := range sets {
		SeedRedisSet(tb, r, key, set)
	}
}

func SeedRedisSet(tb testing.TB, r redis.UniversalClient, key string, members []string) {
	for _, mem := range members {
		if err := r.SAdd(context.Background(), key, mem).Err(); err != nil {
			tb.Fatalf("Failed to seed set (key=%q): %v", key, err)
		}
	}
}

func SeedRedisLists(tb testing.TB, r redis.UniversalClient, lists map[string][]string) {
	for key, vals := range lists {
		for _, v := range vals {
			if err := r.LPush(context.Background(), key, v).Err(); err != nil {
				tb.Fatalf("Failed to seed list (key=%q): %v", key, err)
			}
		}
	}
}

func AssertRedisLists(t *testing.T, r redis.UniversalClient, wantLists map[string][]string) {
	for key, want := range wantLists {
		got, err := r.LRange(context.Background(), key, 0, -1).Result()
		if err != nil {
			t.Fatalf("Failed to read list (key=%q): %v", key, err)
		}
		if diff := cmp.Diff(want, got, SortStringSliceOpt); diff != "" {
			t.Errorf("mismatch found in list (key=%q): (-want,+got)\n%s", key, diff)
		}
	}
}

func AssertRedisSets(t *testing.T, r redis.UniversalClient, wantSets map[string][]string) {
	for key, want := range wantSets {
		got, err := r.SMembers(context.Background(), key).Result()
		if err != nil {
			t.Fatalf("Failed to read set (key=%q): %v", key, err)
		}
		if diff := cmp.Diff(want, got, SortStringSliceOpt); diff != "" {
			t.Errorf("mismatch found in set (key=%q): (-want,+got)\n%s", key, diff)
		}
	}
}

func AssertRedisZSets(t *testing.T, r redis.UniversalClient, wantZSets map[string][]redis.Z) {
	for key, want := range wantZSets {
		got, err := r.ZRangeWithScores(context.Background(), key, 0, -1).Result()
		if err != nil {
			t.Fatalf("Failed to read zset (key=%q): %v", key, err)
		}
		if diff := cmp.Diff(want, got, SortRedisZSetEntryOpt); diff != "" {
			t.Errorf("mismatch found in zset (key=%q): (-want,+got)\n%s", key, diff)
		}
	}
}
