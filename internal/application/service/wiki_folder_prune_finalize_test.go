package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type folderPruneWikiServiceStub struct {
	interfaces.WikiPageService
	calls     int
	folderIDs []string
}

func (s *folderPruneWikiServiceStub) PruneEmptyFolderChains(
	_ context.Context, _ string, folderIDs []string,
) ([]string, error) {
	s.calls++
	s.folderIDs = append([]string(nil), folderIDs...)
	return folderIDs, nil
}

type folderPruneKBServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (s *folderPruneKBServiceStub) GetKnowledgeBaseByIDOnly(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID: id,
		IndexingStrategy: types.IndexingStrategy{
			WikiEnabled: true,
		},
	}, nil
}

type folderPrunePendingRepoStub struct {
	interfaces.TaskPendingOpsRepository
	rows           []*types.TaskPendingOp
	ingestPending  int64
	deletedRowIDs  []int64
	finalizeCounts int
}

func (s *folderPrunePendingRepoStub) PeekBatch(
	_ context.Context, _, _, _ string, _ int,
) ([]*types.TaskPendingOp, error) {
	return s.rows, nil
}

func (s *folderPrunePendingRepoStub) PendingCount(
	_ context.Context, taskType, _, _ string,
) (int64, error) {
	if taskType == wikiTaskType {
		return s.ingestPending, nil
	}
	s.finalizeCounts++
	return int64(len(s.rows)), nil
}

func (s *folderPrunePendingRepoStub) DeleteByIDs(_ context.Context, ids []int64) error {
	s.deletedRowIDs = append(s.deletedRowIDs, ids...)
	return nil
}

type folderPruneTaskStub struct {
	interfaces.TaskEnqueuer
	enqueuedTypes []string
}

func (s *folderPruneTaskStub) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	s.enqueuedTypes = append(s.enqueuedTypes, task.Type())
	return &asynq.TaskInfo{}, nil
}

func makeFolderPruneFinalizeTask(t *testing.T) (*asynq.Task, *types.TaskPendingOp) {
	t.Helper()
	rowPayload, err := json.Marshal(wikiFinalizeRow{FolderIDs: []string{"folder-a"}})
	require.NoError(t, err)
	taskPayload, err := json.Marshal(WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb-1"})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeWikiFinalize, taskPayload), &types.TaskPendingOp{
		ID: 11, Op: wikiFinalizeOpFolderPrune, Payload: rowPayload,
	}
}

func TestProcessWikiFinalizeDefersFolderPruneWhileIngestIsPending(t *testing.T) {
	task, row := makeFolderPruneFinalizeTask(t)
	wikiSvc := &folderPruneWikiServiceStub{}
	pendingRepo := &folderPrunePendingRepoStub{rows: []*types.TaskPendingOp{row}, ingestPending: 1}
	taskQueue := &folderPruneTaskStub{}
	svc := &wikiIngestService{
		wikiService: wikiSvc, kbService: &folderPruneKBServiceStub{},
		pendingRepo: pendingRepo, task: taskQueue,
	}

	require.NoError(t, svc.ProcessWikiFinalize(context.Background(), task))
	require.Zero(t, wikiSvc.calls, "must not prune a folder reserved by an in-flight ingest")
	require.Empty(t, pendingRepo.deletedRowIDs, "durable prune row must remain for retry")
	require.Equal(t, []string{types.TypeWikiFinalize}, taskQueue.enqueuedTypes)
}

func TestProcessWikiFinalizePrunesFolderAfterIngestDrains(t *testing.T) {
	task, row := makeFolderPruneFinalizeTask(t)
	wikiSvc := &folderPruneWikiServiceStub{}
	pendingRepo := &folderPrunePendingRepoStub{rows: []*types.TaskPendingOp{row}}
	svc := &wikiIngestService{
		wikiService: wikiSvc, kbService: &folderPruneKBServiceStub{},
		pendingRepo: pendingRepo, task: &folderPruneTaskStub{},
	}

	require.NoError(t, svc.ProcessWikiFinalize(context.Background(), task))
	require.Equal(t, 1, wikiSvc.calls)
	require.Equal(t, []string{"folder-a"}, wikiSvc.folderIDs)
	require.Equal(t, []int64{11}, pendingRepo.deletedRowIDs)
}
