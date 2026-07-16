package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
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

type runtimeTaskTestInspector struct {
	runtimeTestInspector
	tasks               []types.RuntimeTaskInfo
	retriedTask         string
	deletedTask         string
	forceDeleted        string
	cancelKnowledge     string
	cancelDeleted       int
	mutatedQueue        string
	nextCursor          string
	hasMore             bool
	inputCursor         string
	inputPageSize       int
	listErr             error
	forceDeleteErr      error
	getRuntimeErr       error
	getRuntimeErrFrom   int
	getRuntimeTaskCalls int
}

func (r *runtimeTaskTestInspector) CancelTasksForKnowledge(
	_ context.Context, knowledgeID string,
) (int, int, error) {
	r.cancelKnowledge = knowledgeID
	if r.cancelDeleted > 0 {
		return r.cancelDeleted, 0, nil
	}
	return 0, 0, nil
}

type runtimeKnowledgeCancelTest struct {
	tenantID    uint64
	knowledgeID string
	err         error
}

func (r *runtimeKnowledgeCancelTest) CancelKnowledgeParse(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	r.tenantID, _ = ctx.Value(types.TenantIDContextKey).(uint64)
	r.knowledgeID = knowledgeID
	if r.err != nil {
		return nil, r.err
	}
	return &types.Knowledge{ID: knowledgeID, TenantID: r.tenantID}, nil
}

func (r *runtimeTaskTestInspector) ListRuntimeTasks(
	_ context.Context, _ string, state types.RuntimeTaskState, cursor string, pageSize int,
) (types.RuntimeTaskPage, bool, error) {
	r.inputCursor = cursor
	r.inputPageSize = pageSize
	if r.listErr != nil {
		return types.RuntimeTaskPage{}, true, r.listErr
	}
	for i := range r.tasks {
		if r.tasks[i].State == "" {
			r.tasks[i].State = state
		}
		if r.tasks[i].AllowedActions == nil && state == types.RuntimeTaskArchived {
			r.tasks[i].AllowedActions = []types.RuntimeTaskAction{
				types.RuntimeTaskActionRunNow,
				types.RuntimeTaskActionDelete,
			}
		}
	}
	return types.RuntimeTaskPage{
		Tasks: r.tasks, NextCursor: r.nextCursor, HasMore: r.hasMore,
	}, true, nil
}

func (r *runtimeTaskTestInspector) GetRuntimeTask(
	_ context.Context, queue, taskID string,
) (*types.RuntimeTaskInfo, bool, error) {
	r.getRuntimeTaskCalls++
	if r.getRuntimeErr != nil && r.getRuntimeTaskCalls >= r.getRuntimeErrFrom {
		return nil, true, r.getRuntimeErr
	}
	for i := range r.tasks {
		if r.tasks[i].ID == taskID {
			return &r.tasks[i], true, nil
		}
	}
	return &types.RuntimeTaskInfo{
		ID: taskID, Queue: queue, State: types.RuntimeTaskArchived,
		AllowedActions: []types.RuntimeTaskAction{
			types.RuntimeTaskActionRunNow,
			types.RuntimeTaskActionDelete,
		},
	}, true, nil
}

func (r *runtimeTaskTestInspector) RunRuntimeTask(
	_ context.Context, queue, taskID string,
) (bool, error) {
	r.mutatedQueue = queue
	r.retriedTask = taskID
	return true, nil
}

func (r *runtimeTaskTestInspector) DeleteRuntimeTask(
	_ context.Context, queue, taskID string,
) (bool, error) {
	r.mutatedQueue = queue
	r.deletedTask = taskID
	return true, nil
}

func (r *runtimeTaskTestInspector) ForceDeleteRuntimeTask(
	_ context.Context, queue, taskID string,
) (bool, error) {
	r.mutatedQueue = queue
	r.forceDeleted = taskID
	return true, r.forceDeleteErr
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
		types.WorkerPoolCore:        {8, 2},
		types.WorkerPoolPostProcess: {2, 1},
		types.WorkerPoolEnrichment:  {12, 4},
		types.WorkerPoolMaintenance: {4, 2},
		types.WorkerPoolShared:      {6, 6},
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

func TestListRuntimeTasksReturnsSafeTaskDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{tasks: []types.RuntimeTaskInfo{{
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
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/runtime/queues/default/tasks?state=archived", nil)

	handler.ListRuntimeTasks(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response RuntimeTasksResponse
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

func TestListRuntimeTasksReturnsAndForwardsCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{
		tasks:      []types.RuntimeTaskInfo{{ID: "task-1"}},
		nextCursor: "next-page", hasMore: true,
	}
	handler := &SystemHandler{taskInspector: inspector}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "queue", Value: types.QueueDefault}}
	ctx.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/admin/runtime/queues/default/tasks?state=archived&cursor=previous-page&page_size=25",
		nil,
	)

	handler.ListRuntimeTasks(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response RuntimeTasksResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if inspector.inputCursor != "previous-page" || inspector.inputPageSize != 25 {
		t.Fatalf("cursor request not forwarded: cursor=%q size=%d", inspector.inputCursor, inspector.inputPageSize)
	}
	if !response.HasMore || response.NextCursor != "next-page" {
		t.Fatalf("cursor response missing: %+v", response)
	}
}

func TestListRuntimeTasksReportsExpiredCursorForFrontendRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{taskInspector: &runtimeTaskTestInspector{
		listErr: types.ErrExpiredRuntimeTaskCursor,
	}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "queue", Value: types.QueueDefault}}
	ctx.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/admin/runtime/queues/default/tasks?state=pending&cursor=expired",
		nil,
	)

	handler.ListRuntimeTasks(ctx)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["code"] != "runtime_task_cursor_expired" {
		t.Fatalf("unexpected error response: %+v", response)
	}
}

func TestRuntimeTaskMutationsDelegateToInspector(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{}
	handler := &SystemHandler{taskInspector: inspector}

	retryRecorder := httptest.NewRecorder()
	retryCtx, _ := gin.CreateTestContext(retryRecorder)
	retryCtx.Params = gin.Params{
		{Key: "queue", Value: types.QueueDefault},
		{Key: "task_id", Value: "task-1"},
		{Key: "action", Value: string(types.RuntimeTaskActionRunNow)},
	}
	retryCtx.Request = httptest.NewRequest(http.MethodPost, "/retry", nil)
	handler.MutateRuntimeTask(retryCtx)
	if retryRecorder.Code != http.StatusOK || inspector.retriedTask != "task-1" {
		t.Fatalf("retry failed: status=%d inspector=%+v", retryRecorder.Code, inspector)
	}

	deleteRecorder := httptest.NewRecorder()
	deleteCtx, _ := gin.CreateTestContext(deleteRecorder)
	deleteCtx.Params = gin.Params{
		{Key: "queue", Value: types.QueueDefault},
		{Key: "task_id", Value: "task-2"},
		{Key: "action", Value: string(types.RuntimeTaskActionDelete)},
	}
	deleteCtx.Request = httptest.NewRequest(http.MethodDelete, "/task-2", nil)
	handler.MutateRuntimeTask(deleteCtx)
	if deleteRecorder.Code != http.StatusOK || inspector.deletedTask != "task-2" {
		t.Fatalf("delete failed: status=%d inspector=%+v", deleteRecorder.Code, inspector)
	}
}

func TestListRuntimeTasksRejectsUnknownQueue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{taskInspector: &runtimeTaskTestInspector{}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "queue", Value: "unknown"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/tasks?state=archived", nil)

	handler.ListRuntimeTasks(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestListRuntimeTasksRejectsUnknownState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{taskInspector: &runtimeTaskTestInspector{}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "queue", Value: types.QueueDefault}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/tasks?state=unknown", nil)

	handler.ListRuntimeTasks(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestRuntimeTaskCancelUsesDomainCancellationWithTaskTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{tasks: []types.RuntimeTaskInfo{{
		ID: "task-cancel", Queue: types.QueueDefault, Type: types.TypeDocumentProcess,
		State: types.RuntimeTaskActive, TenantID: 42, KnowledgeID: "knowledge-42",
		AllowedActions: []types.RuntimeTaskAction{types.RuntimeTaskActionCancel},
	}}}
	canceller := &runtimeKnowledgeCancelTest{}
	handler := &SystemHandler{taskInspector: inspector, knowledgeSvc: canceller}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{
		{Key: "queue", Value: types.QueueDefault},
		{Key: "task_id", Value: "task-cancel"},
		{Key: "action", Value: string(types.RuntimeTaskActionCancel)},
	}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/cancel", nil)

	handler.MutateRuntimeTask(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if canceller.tenantID != 42 || canceller.knowledgeID != "knowledge-42" {
		t.Fatalf("domain cancellation context mismatch: %+v", canceller)
	}
}

func TestRuntimeTaskCancelPurgesOrphanWhenKnowledgeGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{tasks: []types.RuntimeTaskInfo{{
		ID: "task-orphan", Queue: types.QueueMultimodal, Type: types.TypeImageMultimodal,
		State: types.RuntimeTaskRetry, TenantID: 42, KnowledgeID: "knowledge-gone",
		AllowedActions: []types.RuntimeTaskAction{types.RuntimeTaskActionCancel},
	}}}
	canceller := &runtimeKnowledgeCancelTest{err: repository.ErrKnowledgeNotFound}
	handler := &SystemHandler{taskInspector: inspector, knowledgeSvc: canceller}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{
		{Key: "queue", Value: types.QueueMultimodal},
		{Key: "task_id", Value: "task-orphan"},
		{Key: "action", Value: string(types.RuntimeTaskActionCancel)},
	}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/cancel", nil)

	handler.MutateRuntimeTask(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if inspector.forceDeleted != "task-orphan" {
		t.Fatalf("expected force delete, got deleted=%q force=%q cancel=%q",
			inspector.deletedTask, inspector.forceDeleted, inspector.cancelKnowledge)
	}
}

func TestRuntimeTaskCancelPurgesOrphanAfterSiblingSweep(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{
		cancelDeleted: 2,
		tasks: []types.RuntimeTaskInfo{{
			ID: "task-orphan", Queue: types.QueueMultimodal, Type: types.TypeImageMultimodal,
			State: types.RuntimeTaskArchived, TenantID: 42, KnowledgeID: "knowledge-gone",
			AllowedActions: []types.RuntimeTaskAction{types.RuntimeTaskActionCancel},
		}},
	}
	canceller := &runtimeKnowledgeCancelTest{err: repository.ErrKnowledgeNotFound}
	handler := &SystemHandler{taskInspector: inspector, knowledgeSvc: canceller}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{
		{Key: "queue", Value: types.QueueMultimodal},
		{Key: "task_id", Value: "task-orphan"},
		{Key: "action", Value: string(types.RuntimeTaskActionCancel)},
	}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/cancel", nil)

	handler.MutateRuntimeTask(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if inspector.cancelKnowledge != "knowledge-gone" || inspector.forceDeleted != "task-orphan" {
		t.Fatalf("expected sibling sweep then force delete, got cancel=%q force=%q",
			inspector.cancelKnowledge, inspector.forceDeleted)
	}
}

func TestRuntimeTaskCancelPurgesOrphanWhenSweepAlreadyRemovedTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{
		cancelDeleted:     1,
		forceDeleteErr:    errors.New("task not found"),
		getRuntimeErr:     errors.New("task not found"),
		getRuntimeErrFrom: 2,
		tasks: []types.RuntimeTaskInfo{{
			ID: "task-orphan", Queue: types.QueueMultimodal, Type: types.TypeImageMultimodal,
			State: types.RuntimeTaskRetry, TenantID: 42, KnowledgeID: "knowledge-gone",
			AllowedActions: []types.RuntimeTaskAction{types.RuntimeTaskActionCancel},
		}},
	}
	canceller := &runtimeKnowledgeCancelTest{err: repository.ErrKnowledgeNotFound}
	handler := &SystemHandler{taskInspector: inspector, knowledgeSvc: canceller}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{
		{Key: "queue", Value: types.QueueMultimodal},
		{Key: "task_id", Value: "task-orphan"},
		{Key: "action", Value: string(types.RuntimeTaskActionCancel)},
	}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/cancel", nil)

	handler.MutateRuntimeTask(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if inspector.forceDeleted != "task-orphan" {
		t.Fatalf("expected force delete attempt, got %q", inspector.forceDeleted)
	}
}
