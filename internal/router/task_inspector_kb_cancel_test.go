package router

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func TestMatchesKnowledgeBase(t *testing.T) {
	knowledgeIDs := map[string]struct{}{
		"knowledge-1": {},
		"knowledge-2": {},
	}
	dataSourceIDs := map[string]struct{}{"datasource-1": {}}
	tests := []struct {
		name     string
		taskType string
		payload  string
		want     bool
	}{
		{name: "knowledge base id", taskType: types.TypeDocumentProcess, payload: `{"knowledge_base_id":"kb-1"}`, want: true},
		{name: "legacy kb id", taskType: types.TypeFAQImport, payload: `{"kb_id":"kb-1"}`, want: true},
		{name: "clone source", taskType: types.TypeKBClone, payload: `{"source_id":"kb-1"}`, want: true},
		{name: "clone target", taskType: types.TypeKBClone, payload: `{"target_id":"kb-1"}`, want: true},
		{name: "source id is task specific", taskType: types.TypeDocumentProcess, payload: `{"source_id":"kb-1"}`},
		{name: "move source", taskType: types.TypeKnowledgeMove, payload: `{"source_kb_id":"kb-1"}`, want: true},
		{name: "move target", taskType: types.TypeKnowledgeMove, payload: `{"target_kb_id":"kb-1"}`, want: true},
		{name: "move fields are task specific", taskType: types.TypeDocumentProcess, payload: `{"source_kb_id":"kb-1"}`},
		{name: "single knowledge", taskType: types.TypeChunkExtract, payload: `{"knowledge_id":"knowledge-1"}`, want: true},
		{
			name:     "knowledge collection",
			taskType: types.TypeKnowledgeListReparse,
			payload:  `{"knowledge_ids":["other","knowledge-2"]}`,
			want:     true,
		},
		{
			name:     "unrelated",
			taskType: types.TypeDocumentProcess,
			payload:  `{"knowledge_base_id":"other","knowledge_id":"other"}`,
		},
		{name: "malformed payload", taskType: types.TypeDocumentProcess, payload: `{`},
		{name: "preserve kb delete by kb", taskType: types.TypeKBDelete, payload: `{"knowledge_base_id":"kb-1"}`},
		{name: "preserve kb delete by knowledge", taskType: types.TypeKBDelete, payload: `{"knowledge_id":"knowledge-1"}`},
		{name: "preserve index delete by kb", taskType: types.TypeIndexDelete, payload: `{"knowledge_base_id":"kb-1"}`},
		{
			name: "preserve index delete by knowledge", taskType: types.TypeIndexDelete,
			payload: `{"knowledge_id":"knowledge-1"}`,
		},
		{
			name: "data source sync", taskType: types.TypeDataSourceSync,
			payload: `{"data_source_id":"datasource-1"}`, want: true,
		},
		{
			name: "other data source sync", taskType: types.TypeDataSourceSync,
			payload: `{"data_source_id":"datasource-other"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := matchesKnowledgeBase(
				test.taskType, []byte(test.payload), "kb-1", knowledgeIDs, dataSourceIDs,
			)
			if got != test.want {
				t.Fatalf("matchesKnowledgeBase() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMatchesKnowledgePreservesPerKnowledgeAllowList(t *testing.T) {
	if !matchesKnowledge(types.TypeDocumentProcess, []byte(`{"knowledge_id":"knowledge-1"}`), "knowledge-1") {
		t.Fatal("document task should remain cancellable by knowledge ID")
	}
	if matchesKnowledge(types.TypeKBClone, []byte(`{"knowledge_id":"knowledge-1"}`), "knowledge-1") {
		t.Fatal("KB clone must not become cancellable through the per-knowledge API")
	}
}

func TestCancelTasksForKnowledgeBaseRescansMutatedPages(t *testing.T) {
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	asynqClient := asynq.NewClientFromRedisClient(redisClient)
	t.Cleanup(func() {
		_ = asynqClient.Close()
		_ = redisClient.Close()
	})
	inspector := &asynqTaskInspector{
		inspector: asynq.NewInspectorFromRedisClient(redisClient),
		redis:     redisClient,
	}

	const matchingPending = 205
	for i := 0; i < matchingPending; i++ {
		enqueueTask(t, asynqClient, types.TypeDocumentProcess,
			fmt.Sprintf(`{"knowledge_base_id":"kb-delete","knowledge_id":"knowledge-%d"}`, i),
			fmt.Sprintf("matching-%03d", i),
		)
	}
	for i := 0; i < 5; i++ {
		enqueueTask(t, asynqClient, types.TypeDocumentProcess,
			`{"knowledge_base_id":"kb-keep","knowledge_id":"keep"}`,
			fmt.Sprintf("survivor-%03d", i),
		)
	}
	enqueueTask(t, asynqClient, types.TypeKBDelete,
		`{"knowledge_base_id":"kb-delete"}`, "kb-delete-cleanup",
	)
	enqueueTask(t, asynqClient, types.TypeIndexDelete,
		`{"knowledge_base_id":"kb-delete"}`, "index-delete-cleanup",
	)
	enqueueTask(t, asynqClient, types.TypeDataSourceSync,
		`{"data_source_id":"datasource-delete"}`, "datasource-sync",
	)

	scheduleTask(t, asynqClient, types.TypeFAQImport,
		`{"kb_id":"kb-delete"}`, "scheduled-kb-match",
	)
	scheduleTask(t, asynqClient, types.TypeKnowledgeListReparse,
		`{"knowledge_ids":["knowledge-associated"]}`, "scheduled-knowledge-match",
	)
	scheduleTask(t, asynqClient, types.TypeFAQImport,
		`{"kb_id":"kb-keep"}`, "scheduled-survivor",
	)

	deleted, cancelled, err := inspector.CancelTasksForKnowledgeBase(
		context.Background(), "kb-delete", []string{"knowledge-associated"}, []string{"datasource-delete"},
	)
	if err != nil {
		t.Fatalf("cancel tasks: %v", err)
	}
	if want := matchingPending + 3; deleted != want {
		t.Fatalf("deleted = %d, want %d", deleted, want)
	}
	if cancelled != 0 {
		t.Fatalf("cancelled active = %d, want 0", cancelled)
	}

	pending, err := inspector.inspector.ListPendingTasks(
		types.QueueDefault, asynq.PageSize(listPageSize), asynq.Page(1),
	)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 7 {
		t.Fatalf("pending survivors = %d, want 7", len(pending))
	}
	if !hasTaskID(pending, "kb-delete-cleanup") {
		t.Fatal("kb:delete cleanup task was removed")
	}
	if !hasTaskID(pending, "index-delete-cleanup") {
		t.Fatal("index:delete cleanup task was removed")
	}

	scheduled, err := inspector.inspector.ListScheduledTasks(
		types.QueueDefault, asynq.PageSize(listPageSize), asynq.Page(1),
	)
	if err != nil {
		t.Fatalf("list scheduled: %v", err)
	}
	if len(scheduled) != 1 || scheduled[0].ID != "scheduled-survivor" {
		t.Fatalf("scheduled survivors = %v, want scheduled-survivor", taskIDs(scheduled))
	}
}

func TestCancelTasksForKnowledgeBaseRemovesCancelledActiveRetry(t *testing.T) {
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	asynqClient := asynq.NewClientFromRedisClient(redisClient)
	t.Cleanup(func() {
		_ = asynqClient.Close()
		_ = redisClient.Close()
	})

	inspector := &asynqTaskInspector{
		inspector: asynq.NewInspectorFromRedisClient(redisClient),
		redis:     redisClient,
	}
	worker := asynq.NewServerFromRedisClient(redisClient, asynq.Config{
		Concurrency:              1,
		Queues:                   map[string]int{types.QueueDefault: 1},
		TaskCheckInterval:        10 * time.Millisecond,
		DelayedTaskCheckInterval: time.Hour,
		RetryDelayFunc: func(_ int, _ error, _ *asynq.Task) time.Duration {
			return time.Hour
		},
		ShutdownTimeout: time.Second,
		LogLevel:        asynq.FatalLevel,
	})

	handlerStarted := make(chan struct{})
	handlerReturned := make(chan struct{})
	mux := asynq.NewServeMux()
	mux.HandleFunc(types.TypeDocumentProcess, func(ctx context.Context, _ *asynq.Task) error {
		close(handlerStarted)
		<-ctx.Done()
		close(handlerReturned)
		return ctx.Err()
	})

	if err := worker.Start(mux); err != nil {
		t.Fatalf("start asynq worker: %v", err)
	}
	t.Cleanup(worker.Shutdown)
	waitForAsynqCancellationSubscriber(t, redisClient)

	const taskID = "active-kb-match"
	if _, err := asynqClient.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte(`{"knowledge_base_id":"kb-delete"}`)),
		asynq.Queue(types.QueueDefault),
		asynq.TaskID(taskID),
		asynq.MaxRetry(3),
	); err != nil {
		t.Fatalf("enqueue active task: %v", err)
	}

	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	waitForTaskState(t, inspector.inspector, taskID, asynq.TaskStateActive)

	deleted, cancelled, err := inspector.CancelTasksForKnowledgeBase(
		context.Background(), "kb-delete", nil, nil,
	)
	if err != nil {
		t.Fatalf("cancel active task: %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("cancelled active = %d, want 1", cancelled)
	}
	if deleted != 1 {
		t.Fatalf("deleted after active cancellation = %d, want 1", deleted)
	}

	select {
	case <-handlerReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled handler did not return")
	}
	waitForTaskToLeaveLiveStates(t, inspector.inspector, taskID)
}

func enqueueTask(t *testing.T, client *asynq.Client, taskType, payload, taskID string) {
	t.Helper()
	if _, err := client.Enqueue(
		asynq.NewTask(taskType, []byte(payload)),
		asynq.Queue(types.QueueDefault),
		asynq.TaskID(taskID),
	); err != nil {
		t.Fatalf("enqueue %s: %v", taskID, err)
	}
}

func scheduleTask(t *testing.T, client *asynq.Client, taskType, payload, taskID string) {
	t.Helper()
	if _, err := client.Enqueue(
		asynq.NewTask(taskType, []byte(payload)),
		asynq.Queue(types.QueueDefault),
		asynq.TaskID(taskID),
		asynq.ProcessAt(time.Now().Add(time.Hour)),
	); err != nil {
		t.Fatalf("schedule %s: %v", taskID, err)
	}
}

func hasTaskID(tasks []*asynq.TaskInfo, taskID string) bool {
	for _, task := range tasks {
		if task.ID == taskID {
			return true
		}
	}
	return false
}

func taskIDs(tasks []*asynq.TaskInfo) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func waitForTaskState(t *testing.T, inspector *asynq.Inspector, taskID string, want asynq.TaskState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastState asynq.TaskState
	for time.Now().Before(deadline) {
		task, err := inspector.GetTaskInfo(types.QueueDefault, taskID)
		if err == nil {
			lastState = task.State
			if task.State == want {
				return
			}
		} else if !errors.Is(err, asynq.ErrTaskNotFound) {
			t.Fatalf("get task %s: %v", taskID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s state = %v, want %v", taskID, lastState, want)
}

func waitForAsynqCancellationSubscriber(t *testing.T, redisClient *redis.Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		subscribers, err := redisClient.PubSubNumSub(context.Background(), "asynq:cancel").Result()
		if err != nil {
			t.Fatalf("query cancellation subscribers: %v", err)
		}
		if subscribers["asynq:cancel"] > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("asynq cancellation subscriber did not start")
}

func waitForTaskToLeaveLiveStates(t *testing.T, inspector *asynq.Inspector, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastState asynq.TaskState
	for time.Now().Before(deadline) {
		task, err := inspector.GetTaskInfo(types.QueueDefault, taskID)
		if errors.Is(err, asynq.ErrTaskNotFound) {
			return
		}
		if err != nil {
			t.Fatalf("get task %s: %v", taskID, err)
		}
		lastState = task.State
		switch task.State {
		case asynq.TaskStatePending, asynq.TaskStateScheduled, asynq.TaskStateRetry, asynq.TaskStateActive:
			time.Sleep(10 * time.Millisecond)
		default:
			return
		}
	}
	t.Fatalf("task %s remained in live state %v", taskID, lastState)
}
