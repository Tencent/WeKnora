package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

type imageProducerTaskEnqueuer struct {
	tasks []*asynq.Task
	err   error
}

func (e *imageProducerTaskEnqueuer) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	if e.err != nil {
		return nil, e.err
	}
	return &asynq.TaskInfo{}, nil
}

func TestEnqueueImageMultimodalTasksCarriesAttemptAndImageCount(t *testing.T) {
	enqueuer := &imageProducerTaskEnqueuer{}
	svc := &knowledgeService{task: enqueuer}
	images := []docparser.StoredImage{
		{ServingURL: "local://images/one.png"},
		{ServingURL: "local://images/two.png"},
	}
	chunks := []types.ParsedChunk{
		{ChunkID: "chunk-1", Content: "![one](local://images/one.png)"},
		{ChunkID: "chunk-2", Content: "![two](local://images/two.png)"},
	}

	err := svc.enqueueImageMultimodalTasks(
		withAttempt(context.Background(), 7),
		&types.Knowledge{ID: "knowledge-1", TenantID: 11},
		&types.KnowledgeBase{ID: "kb-1"},
		images,
		chunks,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(enqueuer.tasks) != 2 {
		t.Fatalf("enqueued %d tasks, want 2", len(enqueuer.tasks))
	}
	for index, task := range enqueuer.tasks {
		var payload types.ImageMultimodalPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Attempt != 7 || payload.ImageCount != 2 || payload.ImageIndex != index {
			t.Fatalf("payload[%d] attempt/count/index = %d/%d/%d, want 7/2/%d",
				index, payload.Attempt, payload.ImageCount, payload.ImageIndex, index)
		}
		if payload.ChunkID != chunks[index].ChunkID {
			t.Fatalf("payload[%d] chunk = %q, want %q", index, payload.ChunkID, chunks[index].ChunkID)
		}
	}
}

func TestEnqueueImageMultimodalTasksFailsClosed(t *testing.T) {
	svc := &knowledgeService{task: &imageProducerTaskEnqueuer{err: errors.New("queue unavailable")}}
	err := svc.enqueueImageMultimodalTasks(
		withAttempt(context.Background(), 3),
		&types.Knowledge{ID: "knowledge-1"},
		&types.KnowledgeBase{ID: "kb-1"},
		[]docparser.StoredImage{{ServingURL: "local://images/one.png"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("enqueue failure must be returned so the knowledge cannot remain stuck in processing")
	}
}

func TestEnqueueImageMultimodalTasksTreatsTaskIDConflictAsAlreadyQueued(t *testing.T) {
	svc := &knowledgeService{task: &imageProducerTaskEnqueuer{err: asynq.ErrTaskIDConflict}}
	err := svc.enqueueImageMultimodalTasks(
		withAttempt(context.Background(), 3),
		&types.Knowledge{ID: "knowledge-1"},
		&types.KnowledgeBase{ID: "kb-1"},
		[]docparser.StoredImage{{ServingURL: "local://images/one.png"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("an active task with the deterministic ID is already queued: %v", err)
	}
}
