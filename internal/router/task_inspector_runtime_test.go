package router

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
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
