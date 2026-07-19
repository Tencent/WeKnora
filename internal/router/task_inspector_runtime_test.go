package router

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func TestProjectRuntimeTaskRedactsPayloadAndBuildsSafeActions(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"tenant_id":         42,
		"knowledge_base_id": "kb-1",
		"knowledge_id":      "knowledge-1",
		"file_url":          "secret://signed-document-url",
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1_700_000_000, 0)
	info, err := projectRuntimeTask(&asynq.TaskInfo{
		ID: "task-1", Queue: types.QueueDefault, Type: types.TypeDocumentProcess,
		Payload: payload, State: asynq.TaskStateActive, MaxRetry: 3, Retried: 1,
	}, runtimeWorkerMetadata{started: started, worker: "worker-a:123"})
	if err != nil {
		t.Fatalf("project task: %v", err)
	}
	if info.State != types.RuntimeTaskActive || info.TenantID != 42 ||
		info.KnowledgeBaseID != "kb-1" || info.KnowledgeID != "knowledge-1" {
		t.Fatalf("safe routing metadata missing: %+v", info)
	}
	if info.StartedAt == nil || !info.StartedAt.Equal(started) || info.Worker != "worker-a:123" {
		t.Fatalf("worker metadata missing: %+v", info)
	}
	if len(info.AllowedActions) != 1 || info.AllowedActions[0] != types.RuntimeTaskActionCancel {
		t.Fatalf("active document actions = %v", info.AllowedActions)
	}
}

func TestProjectRuntimeTaskActionsFollowCurrentState(t *testing.T) {
	payload := []byte(`{"tenant_id":7,"knowledge_id":"knowledge-7"}`)
	cases := []struct {
		state asynq.TaskState
		want  []types.RuntimeTaskAction
	}{
		{asynq.TaskStateScheduled, []types.RuntimeTaskAction{types.RuntimeTaskActionCancel, types.RuntimeTaskActionRunNow}},
		{asynq.TaskStateRetry, []types.RuntimeTaskAction{types.RuntimeTaskActionCancel, types.RuntimeTaskActionRunNow}},
		{asynq.TaskStateArchived, []types.RuntimeTaskAction{types.RuntimeTaskActionRunNow, types.RuntimeTaskActionDelete}},
		{asynq.TaskStateCompleted, []types.RuntimeTaskAction{}},
	}
	for _, tc := range cases {
		info, err := projectRuntimeTask(&asynq.TaskInfo{
			ID: "task", Queue: types.QueueDefault, Type: types.TypeDocumentProcess,
			Payload: payload, State: tc.state,
		}, runtimeWorkerMetadata{})
		if err != nil {
			t.Fatalf("state %v: %v", tc.state, err)
		}
		if len(info.AllowedActions) != len(tc.want) {
			t.Fatalf("state %v actions = %v, want %v", tc.state, info.AllowedActions, tc.want)
		}
		for i := range tc.want {
			if info.AllowedActions[i] != tc.want[i] {
				t.Fatalf("state %v actions = %v, want %v", tc.state, info.AllowedActions, tc.want)
			}
		}
	}
}

func TestProjectRuntimeTaskUsesAllowListedBatchMetadata(t *testing.T) {
	payload := []byte(`{
		"tenant_id":9,
		"task_id":"move-1",
		"source_kb_id":"source-kb",
		"target_kb_id":"target-kb",
		"knowledge_ids":["a","b"],
		"created_at":1700000000,
		"content":"must-not-be-projected"
	}`)
	info, err := projectRuntimeTask(&asynq.TaskInfo{
		ID: "task-move", Queue: types.QueueMaintenance, Type: types.TypeKnowledgeMove,
		Payload: payload, State: asynq.TaskStatePending,
	}, runtimeWorkerMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if info.TaskID != "move-1" || info.SourceKBID != "source-kb" ||
		info.TargetKBID != "target-kb" || info.KnowledgeCount != 2 || info.EnqueuedAt == nil {
		t.Fatalf("batch projection mismatch: %+v", info)
	}
	if len(info.AllowedActions) != 0 {
		t.Fatalf("generic maintenance task must not expose raw deletion: %v", info.AllowedActions)
	}
}

func TestRuntimeTaskCursorUsesStateTimeOrderAndSurvivingAnchors(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	inspector := &asynqTaskInspector{redis: client}
	ctx := context.Background()

	pendingKey, _ := runtimeTaskStateKey(types.QueueDefault, types.RuntimeTaskPending)
	if err := client.LPush(ctx, pendingKey, "old", "middle", "new").Err(); err != nil {
		t.Fatal(err)
	}
	ids, err := inspector.listRuntimeTaskIDs(ctx, types.QueueDefault, types.RuntimeTaskPending, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids, []string{"new", "middle"}; !equalStrings(got, want) {
		t.Fatalf("pending order = %v, want %v", got, want)
	}
	if err = client.LRem(ctx, pendingKey, 0, "middle").Err(); err != nil {
		t.Fatal(err)
	}
	ids, err = inspector.listRuntimeTaskIDs(
		ctx, types.QueueDefault, types.RuntimeTaskPending, []string{"new", "middle"}, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids, []string{"old"}; !equalStrings(got, want) {
		t.Fatalf("anchor fallback order = %v, want %v", got, want)
	}

	scheduledKey, _ := runtimeTaskStateKey(types.QueueDefault, types.RuntimeTaskScheduled)
	if err = client.ZAdd(ctx, scheduledKey,
		redis.Z{Score: 30, Member: "later"},
		redis.Z{Score: 10, Member: "next"},
		redis.Z{Score: 20, Member: "after-next"},
	).Err(); err != nil {
		t.Fatal(err)
	}
	ids, err = inspector.listRuntimeTaskIDs(ctx, types.QueueDefault, types.RuntimeTaskScheduled, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids, []string{"next", "after-next", "later"}; !equalStrings(got, want) {
		t.Fatalf("scheduled order = %v, want %v", got, want)
	}

	archivedKey, _ := runtimeTaskStateKey(types.QueueDefault, types.RuntimeTaskArchived)
	if err = client.ZAdd(ctx, archivedKey,
		redis.Z{Score: 10, Member: "old-failure"},
		redis.Z{Score: 30, Member: "new-failure"},
		redis.Z{Score: 20, Member: "middle-failure"},
	).Err(); err != nil {
		t.Fatal(err)
	}
	ids, err = inspector.listRuntimeTaskIDs(ctx, types.QueueDefault, types.RuntimeTaskArchived, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids, []string{"new-failure", "middle-failure", "old-failure"}; !equalStrings(got, want) {
		t.Fatalf("archived order = %v, want %v", got, want)
	}
}

func TestRuntimeTaskCursorIsBoundToQueueAndState(t *testing.T) {
	raw, err := encodeRuntimeTaskCursor(
		types.QueueDefault,
		types.RuntimeTaskArchived,
		[]string{"task-1", "task-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := decodeRuntimeTaskCursor(raw, types.QueueDefault, types.RuntimeTaskArchived)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(anchors, []string{"task-1", "task-2"}) {
		t.Fatalf("anchors = %v", anchors)
	}
	if _, err = decodeRuntimeTaskCursor(raw, types.QueueDefault, types.RuntimeTaskRetry); err == nil {
		t.Fatal("cursor from another state should be rejected")
	}
	if _, err = decodeRuntimeTaskCursor("not-base64", types.QueueDefault, types.RuntimeTaskArchived); err == nil {
		t.Fatal("malformed cursor should be rejected")
	}
}

func TestListRuntimeTasksPaginatesNewestPendingTasksWithoutOverlap(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	asynqClient := asynq.NewClientFromRedisClient(client)
	inspector := &asynqTaskInspector{
		inspector: asynq.NewInspectorFromRedisClient(client),
		redis:     client,
	}

	for _, id := range []string{"old", "middle", "new"} {
		_, err := asynqClient.Enqueue(
			asynq.NewTask(types.TypeDocumentProcess, []byte(`{"tenant_id":1,"knowledge_id":"knowledge-1"}`)),
			asynq.Queue(types.QueueDefault),
			asynq.TaskID(id),
		)
		if err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}
	pendingKey, _ := runtimeTaskStateKey(types.QueueDefault, types.RuntimeTaskPending)
	if err := client.LInsert(context.Background(), pendingKey, "AFTER", "new", "already-gone").Err(); err != nil {
		t.Fatalf("insert stale task id: %v", err)
	}

	first, supported, err := inspector.ListRuntimeTasks(
		context.Background(), types.QueueDefault, types.RuntimeTaskPending, "", 2,
	)
	if err != nil || !supported {
		t.Fatalf("first page: supported=%v err=%v", supported, err)
	}
	if got, want := runtimeTaskIDs(first.Tasks), []string{"new", "middle"}; !equalStrings(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page cursor missing: %+v", first)
	}

	second, supported, err := inspector.ListRuntimeTasks(
		context.Background(), types.QueueDefault, types.RuntimeTaskPending, first.NextCursor, 2,
	)
	if err != nil || !supported {
		t.Fatalf("second page: supported=%v err=%v", supported, err)
	}
	if got, want := runtimeTaskIDs(second.Tasks), []string{"old"}; !equalStrings(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if second.HasMore || second.NextCursor != "" {
		t.Fatalf("unexpected continuation after final page: %+v", second)
	}
}

func runtimeTaskIDs(tasks []types.RuntimeTaskInfo) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
