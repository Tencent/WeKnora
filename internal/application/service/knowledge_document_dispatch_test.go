package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type documentDispatchPendingStub struct {
	interfaces.TaskPendingOpsRepository
	ops     []*types.TaskPendingOp
	deleted []int64
	pending int64
}

func (s *documentDispatchPendingStub) ClaimBatch(context.Context, string, string, string, int, time.Time) ([]*types.TaskPendingOp, error) {
	return s.ops, nil
}

func (s *documentDispatchPendingStub) DeleteByIDs(_ context.Context, ids []int64) error {
	s.deleted = append(s.deleted, ids...)
	return nil
}

func (s *documentDispatchPendingStub) PendingCount(context.Context, string, string, string) (int64, error) {
	return s.pending, nil
}

type documentDispatchTaskStub struct {
	tasks []*asynq.Task
}

func (s *documentDispatchTaskStub) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	s.tasks = append(s.tasks, task)
	return &asynq.TaskInfo{ID: "task", Queue: task.Type()}, nil
}

func TestProcessDocumentDispatchKeepsOnlyNewestPayloadPerKnowledge(t *testing.T) {
	oldPayload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: "doc-1", Attempt: 1,
	})
	require.NoError(t, err)
	newPayload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: "doc-1", Attempt: 2,
	})
	require.NoError(t, err)

	pending := &documentDispatchPendingStub{ops: []*types.TaskPendingOp{
		{ID: 10, DedupKey: "doc-1", Payload: oldPayload},
		{ID: 11, DedupKey: "doc-1", Payload: newPayload},
	}, pending: 0}
	tasks := &documentDispatchTaskStub{}
	svc := &knowledgeService{
		config:          &config.Config{},
		task:            tasks,
		taskPendingRepo: pending,
	}

	err = svc.ProcessDocumentDispatch(context.Background(), asynq.NewTask(
		types.TypeDocumentDispatch,
		[]byte(`{"tenant_id":1,"knowledge_base_id":"kb-1"}`),
	))
	require.NoError(t, err)
	require.Len(t, tasks.tasks, 1)
	require.Contains(t, pending.deleted, int64(10))

	var got types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(tasks.tasks[0].Payload(), &got))
	require.Equal(t, 2, got.Attempt)
	require.Equal(t, int64(11), got.PendingOpID)
}

func TestProcessDocumentDispatchRearmsWhenOtherDocumentsRemain(t *testing.T) {
	payload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: "doc-1",
	})
	require.NoError(t, err)

	pending := &documentDispatchPendingStub{
		ops:     []*types.TaskPendingOp{{ID: 10, DedupKey: "doc-1", Payload: payload}},
		pending: 2,
	}
	tasks := &documentDispatchTaskStub{}
	svc := &knowledgeService{
		config:          &config.Config{},
		task:            tasks,
		taskPendingRepo: pending,
	}

	err = svc.ProcessDocumentDispatch(context.Background(), asynq.NewTask(
		types.TypeDocumentDispatch,
		[]byte(`{"tenant_id":1,"knowledge_base_id":"kb-1"}`),
	))
	require.NoError(t, err)
	require.Len(t, tasks.tasks, 2)
	require.Equal(t, types.TypeDocumentProcess, tasks.tasks[0].Type())
	require.Equal(t, types.TypeDocumentDispatch, tasks.tasks[1].Type())
}

func TestSettleDocumentPendingOpRearmsAfterCompletion(t *testing.T) {
	pending := &documentDispatchPendingStub{pending: 1}
	tasks := &documentDispatchTaskStub{}
	svc := &knowledgeService{
		task:            tasks,
		taskPendingRepo: pending,
	}
	payload := &types.DocumentProcessPayload{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		PendingOpID:     11,
	}

	svc.settleDocumentPendingOp(context.Background(), payload, nil)
	require.Contains(t, pending.deleted, int64(11))
	require.Len(t, tasks.tasks, 1)
	require.Equal(t, types.TypeDocumentDispatch, tasks.tasks[0].Type())
}
