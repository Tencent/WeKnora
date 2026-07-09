package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

func TestProcessingQueueSnapshotReaderAggregatesReadOnlyTasks(t *testing.T) {
	fake := newFakeProcessingLister()
	fake.pending[types.QueueDefault] = []*asynq.TaskInfo{
		taskInfo(types.TypeDocumentProcess, map[string]any{"knowledge_id": "doc-1", "attempt": 2}),
		taskInfo(types.TypeKBDelete, map[string]any{"knowledge_id": "doc-ignored"}),
	}
	fake.active[types.QueueMultimodal] = []*asynq.TaskInfo{
		taskInfo(types.TypeImageMultimodal, map[string]any{"knowledge_id": "doc-1", "attempt": 2, "image_index": 0}),
		taskInfo(types.TypeImageMultimodal, map[string]any{"knowledge_id": "doc-1", "attempt": 2, "image_index": 0}),
	}
	fake.retry[types.QueueQuestion] = []*asynq.TaskInfo{
		taskInfo(types.TypeQuestionGeneration, map[string]any{"knowledge_id": "doc-1", "attempt": 2, "batch_index": 3}),
	}
	fake.scheduled[types.QueueGraph] = []*asynq.TaskInfo{
		taskInfo(types.TypeChunkExtract, map[string]any{"knowledge_id": "doc-2", "attempt": 1, "chunk_id": "chunk-a", "chunk_index": 83}),
	}

	reader := newProcessingQueueSnapshotReader(fake, time.Second, 50)
	snap, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != types.ProcessingQueueSnapshotOK {
		t.Fatalf("status = %s, want ok", snap.Status)
	}
	doc := snap.Aggregates["doc-1:2:docreader"]
	if doc == nil || doc.PendingCount != 1 {
		t.Fatalf("docreader aggregate = %#v, want pending 1", doc)
	}
	mm := snap.Aggregates["doc-1:2:multimodal"]
	if mm == nil || mm.ActiveCount != 2 || len(mm.Children) != 1 {
		t.Fatalf("multimodal aggregate = %#v, children=%d; want active 2 dedup child 1", mm, len(mm.Children))
	}
	question := snap.Aggregates["doc-1:2:question"]
	if question == nil || question.RetryCount != 1 || question.Children["3"] == nil {
		t.Fatalf("question aggregate = %#v, want retry child 3", question)
	}
	graph := snap.Aggregates["doc-2:1:graph"]
	if graph == nil || graph.ScheduledCount != 1 || graph.Children["83"] == nil {
		t.Fatalf("graph aggregate = %#v, want scheduled child 83", graph)
	}
	if _, ok := snap.Aggregates["doc-ignored:1:docreader"]; ok {
		t.Fatalf("unrelated task type leaked into snapshot")
	}
	if fake.calls["pending:"+types.QueueDefault] != 1 {
		t.Fatalf("expected injected lister to be used once, calls=%v", fake.calls)
	}
}

func TestProcessingQueueSnapshotReaderPartialAndCache(t *testing.T) {
	fake := newFakeProcessingLister()
	for i := 0; i < processingQueueSnapshotPageSize; i++ {
		fake.pending[types.QueueDefault] = append(fake.pending[types.QueueDefault],
			taskInfo(types.TypeDocumentProcess, map[string]any{"knowledge_id": "doc-1"}))
	}
	reader := newProcessingQueueSnapshotReader(fake, time.Hour, 1)
	snap, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != types.ProcessingQueueSnapshotPartial {
		t.Fatalf("status = %s, want partial", snap.Status)
	}
	if len(snap.TruncatedQueues) == 0 {
		t.Fatalf("expected truncated queue marker")
	}
	if _, err := reader.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.calls["pending:"+types.QueueDefault] != 1 {
		t.Fatalf("snapshot did not use cache, calls=%v", fake.calls)
	}
}

func TestProcessingQueueSnapshotReaderDegradedAndLite(t *testing.T) {
	fake := newFakeProcessingLister()
	fake.err = errors.New("redis unavailable")
	reader := newProcessingQueueSnapshotReader(fake, time.Second, 50)
	snap, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != types.ProcessingQueueSnapshotDegraded {
		t.Fatalf("status = %s, want degraded", snap.Status)
	}

	lite, err := NewNoopProcessingQueueSnapshotReader().Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lite.ExecutorMode != types.ProcessingExecutorModeLite || lite.Status != types.ProcessingQueueSnapshotNotApplicable {
		t.Fatalf("lite snapshot = %#v", lite)
	}
}

type fakeProcessingLister struct {
	pending   map[string][]*asynq.TaskInfo
	scheduled map[string][]*asynq.TaskInfo
	retry     map[string][]*asynq.TaskInfo
	active    map[string][]*asynq.TaskInfo
	calls     map[string]int
	err       error
}

func newFakeProcessingLister() *fakeProcessingLister {
	return &fakeProcessingLister{
		pending:   map[string][]*asynq.TaskInfo{},
		scheduled: map[string][]*asynq.TaskInfo{},
		retry:     map[string][]*asynq.TaskInfo{},
		active:    map[string][]*asynq.TaskInfo{},
		calls:     map[string]int{},
	}
}

func (f *fakeProcessingLister) ListPendingTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.page("pending", queue, f.pending[queue], opts...)
}

func (f *fakeProcessingLister) ListScheduledTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.page("scheduled", queue, f.scheduled[queue], opts...)
}

func (f *fakeProcessingLister) ListRetryTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.page("retry", queue, f.retry[queue], opts...)
}

func (f *fakeProcessingLister) ListActiveTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.page("active", queue, f.active[queue], opts...)
}

func (f *fakeProcessingLister) page(state, queue string, rows []*asynq.TaskInfo, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	f.calls[state+":"+queue]++
	if f.err != nil {
		return nil, f.err
	}
	return rows, nil
}

func taskInfo(taskType string, payload map[string]any) *asynq.TaskInfo {
	b, _ := json.Marshal(payload)
	next := time.Date(2026, 6, 21, 10, 5, 0, 0, time.UTC)
	failed := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	return &asynq.TaskInfo{Type: taskType, Payload: b, NextProcessAt: next, LastFailedAt: failed, LastErr: "boom"}
}
