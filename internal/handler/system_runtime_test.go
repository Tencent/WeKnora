package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type runtimeTestSettings struct{}

func (runtimeTestSettings) GetInt(_ context.Context, key, _ string, def int64) int64 {
	switch key {
	case "asynq.core_concurrency":
		return 8
	case "asynq.postprocess_concurrency":
		return 2
	case "asynq.enrichment_concurrency":
		return 12
	case "asynq.maintenance_concurrency":
		return 4
	case "asynq.shared_concurrency":
		return 6
	case "asynq.wiki_concurrency":
		return 8
	default:
		return def
	}
}
func (runtimeTestSettings) GetString(_ context.Context, _, _, def string) string  { return def }
func (runtimeTestSettings) GetBool(_ context.Context, _, _ string, def bool) bool { return def }
func (runtimeTestSettings) GetStringList(_ context.Context, _, _ string, def []string) []string {
	return def
}
func (runtimeTestSettings) List(context.Context) ([]*types.SystemSetting, error) { return nil, nil }
func (runtimeTestSettings) Get(context.Context, string) (*types.SystemSetting, error) {
	return nil, nil
}
func (runtimeTestSettings) Update(context.Context, string, any) (*types.SystemSetting, error) {
	return nil, nil
}
func (runtimeTestSettings) Reset(context.Context, string) error  { return nil }
func (runtimeTestSettings) SubscribeRedis(context.Context) error { return nil }

type runtimeInvalidSettings struct{ runtimeTestSettings }

func (runtimeInvalidSettings) GetInt(_ context.Context, _ string, _ string, _ int64) int64 {
	return 0
}

type runtimeTestInspector struct{}

func (runtimeTestInspector) CancelTasksForKnowledge(context.Context, string) (int, int, error) {
	return 0, 0, nil
}
func (runtimeTestInspector) HasQueuedTasksForKnowledge(context.Context, string) (bool, error) {
	return false, nil
}
func (runtimeTestInspector) QueueStats(context.Context) ([]types.QueueStat, bool, error) {
	return []types.QueueStat{}, true, nil
}
func (runtimeTestInspector) WorkerServerStats(context.Context) ([]types.WorkerServerStat, bool, error) {
	return []types.WorkerServerStat{
		{Concurrency: 8, Active: 4, Status: "active", Queues: types.QueueWeightsForPool(types.WorkerPoolCore)},
		{Concurrency: 2, Active: 1, Status: "active", Queues: types.QueueWeightsForPool(types.WorkerPoolPostProcess)},
		{Concurrency: 12, Active: 6, Status: "active", Queues: types.QueueWeightsForPool(types.WorkerPoolEnrichment)},
		{Concurrency: 4, Active: 1, Status: "active", Queues: types.QueueWeightsForPool(types.WorkerPoolMaintenance)},
		{Concurrency: 6, Active: 3, Status: "active", Queues: types.QueueWeightsForSharedPool()},
		{Concurrency: 8, Active: 2, Status: "active", Queues: types.QueueWeightsForPool(types.WorkerPoolWiki)},
		{Concurrency: 99, Active: 0, Status: "stopped", Queues: types.QueueWeightsForPool(types.WorkerPoolCore)},
	}, true, nil
}

type runtimeFailedTestInspector struct {
	runtimeTestInspector
	tasks        []types.FailedTaskInfo
	retriedTask  string
	deletedTask  string
	mutatedQueue string
}

func (r *runtimeFailedTestInspector) ListFailedTasks(
	context.Context, string, int, int,
) ([]types.FailedTaskInfo, bool, error) {
	return r.tasks, true, nil
}

func (r *runtimeFailedTestInspector) RetryFailedTask(
	_ context.Context, queue, taskID string,
) (bool, error) {
	r.mutatedQueue = queue
	r.retriedTask = taskID
	return true, nil
}

func (r *runtimeFailedTestInspector) DeleteFailedTask(
	_ context.Context, queue, taskID string,
) (bool, error) {
	r.mutatedQueue = queue
	r.deletedTask = taskID
	return true, nil
}

func TestGetRuntimeQueuesReportsIsolatedPoolCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{
		systemSettingSvc: runtimeTestSettings{},
		taskInspector:    runtimeTestInspector{},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/runtime/queues", nil)

	handler.GetRuntimeQueues(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response RuntimeQueuesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Available {
		t.Fatal("queue inspection should be available")
	}
	if response.UpstreamConcurrency != 32 || response.ParseConcurrency != 32 {
		t.Fatalf("upstream compatibility values are wrong: %+v", response)
	}
	want := map[string]struct {
		concurrency int
		queueCount  int
	}{
		types.WorkerPoolCore:        {8, 1},
		types.WorkerPoolPostProcess: {2, 1},
		types.WorkerPoolEnrichment:  {12, 4},
		types.WorkerPoolMaintenance: {4, 2},
		types.WorkerPoolShared:      {6, 5},
		types.WorkerPoolWiki:        {8, 1},
	}
	if len(response.Pools) != len(want) {
		t.Fatalf("pool count = %d, want %d", len(response.Pools), len(want))
	}
	for _, pool := range response.Pools {
		expected, ok := want[pool.Name]
		if !ok {
			t.Fatalf("unexpected pool %q", pool.Name)
		}
		if pool.Concurrency != expected.concurrency || pool.QueueCount != expected.queueCount {
			t.Fatalf("pool %q = %+v, want concurrency=%d queue_count=%d",
				pool.Name, pool, expected.concurrency, expected.queueCount)
		}
		if pool.Instances != 1 || pool.ClusterCapacity != expected.concurrency {
			t.Fatalf("pool %q live capacity = %+v", pool.Name, pool)
		}
	}
}

func TestGetRuntimeQueuesFallsBackFromInvalidHistoricalConcurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{
		systemSettingSvc: runtimeInvalidSettings{},
		taskInspector:    runtimeTestInspector{},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/runtime/queues", nil)

	handler.GetRuntimeQueues(ctx)

	var response RuntimeQueuesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UpstreamConcurrency != types.DefaultUpstreamWorkerConcurrency ||
		response.WikiConcurrency != types.DefaultWikiWorkerConcurrency {
		t.Fatalf("invalid stored values should use worker defaults: %+v", response)
	}
	for _, pool := range response.Pools {
		if pool.Concurrency < 1 {
			t.Fatalf("pool %q reported non-positive concurrency: %+v", pool.Name, pool)
		}
	}
}

func TestListRuntimeFailedTasksReturnsSafeTaskDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeFailedTestInspector{tasks: []types.FailedTaskInfo{{
		ID:              "task-1",
		Queue:           types.QueueDefault,
		Type:            types.TypeDocumentProcess,
		LastError:       "model unavailable",
		Retried:         5,
		MaxRetry:        5,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
	}}}
	handler := &SystemHandler{taskInspector: inspector}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "queue", Value: types.QueueDefault}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/runtime/queues/default/failed-tasks", nil)

	handler.ListRuntimeFailedTasks(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response RuntimeFailedTasksResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Available || len(response.Tasks) != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Tasks[0].KnowledgeID != "knowledge-1" || response.Tasks[0].LastError != "model unavailable" {
		t.Fatalf("task details missing: %+v", response.Tasks[0])
	}
}

func TestRuntimeFailedTaskMutationsDelegateToInspector(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeFailedTestInspector{}
	handler := &SystemHandler{taskInspector: inspector}

	retryRecorder := httptest.NewRecorder()
	retryCtx, _ := gin.CreateTestContext(retryRecorder)
	retryCtx.Params = gin.Params{
		{Key: "queue", Value: types.QueueDefault},
		{Key: "task_id", Value: "task-1"},
	}
	retryCtx.Request = httptest.NewRequest(http.MethodPost, "/retry", nil)
	handler.RetryRuntimeFailedTask(retryCtx)
	if retryRecorder.Code != http.StatusOK || inspector.retriedTask != "task-1" {
		t.Fatalf("retry failed: status=%d inspector=%+v", retryRecorder.Code, inspector)
	}

	deleteRecorder := httptest.NewRecorder()
	deleteCtx, _ := gin.CreateTestContext(deleteRecorder)
	deleteCtx.Params = gin.Params{
		{Key: "queue", Value: types.QueueDefault},
		{Key: "task_id", Value: "task-2"},
	}
	deleteCtx.Request = httptest.NewRequest(http.MethodDelete, "/task-2", nil)
	handler.DeleteRuntimeFailedTask(deleteCtx)
	if deleteRecorder.Code != http.StatusOK || inspector.deletedTask != "task-2" {
		t.Fatalf("delete failed: status=%d inspector=%+v", deleteRecorder.Code, inspector)
	}
}

func TestListRuntimeFailedTasksRejectsUnknownQueue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{taskInspector: &runtimeFailedTestInspector{}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "queue", Value: "unknown"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/failed-tasks", nil)

	handler.ListRuntimeFailedTasks(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
