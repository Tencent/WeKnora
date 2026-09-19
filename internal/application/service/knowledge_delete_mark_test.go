package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type markDeletingRepoStub struct {
	interfaces.KnowledgeRepository
	rows   []*types.Knowledge
	marked []string
}

func (r *markDeletingRepoStub) GetKnowledgeBatch(
	_ context.Context,
	_ uint64,
	_ []string,
) ([]*types.Knowledge, error) {
	return r.rows, nil
}

func (r *markDeletingRepoStub) MarkKnowledgeDeleting(
	_ context.Context,
	_ uint64,
	ids []string,
) error {
	r.marked = append([]string(nil), ids...)
	return nil
}

type markDeletingInspectorStub struct {
	cancelled []string
}

func (f *markDeletingInspectorStub) CancelTasksForKnowledge(
	_ context.Context, knowledgeID string,
) (int, int, error) {
	f.cancelled = append(f.cancelled, knowledgeID)
	return 1, 0, nil
}

func (f *markDeletingInspectorStub) HasQueuedTasksForKnowledge(context.Context, string) (bool, error) {
	return false, nil
}

func (f *markDeletingInspectorStub) HasQueuedDeleteTasksForKnowledge(context.Context, string) (bool, error) {
	return false, nil
}

func (f *markDeletingInspectorStub) QueueStats(context.Context) ([]types.QueueStat, bool, error) {
	return nil, false, nil
}

func (f *markDeletingInspectorStub) WorkerServerStats(context.Context) ([]types.WorkerServerStat, bool, error) {
	return nil, false, nil
}

func TestMarkKnowledgesDeleting_MarksAndDequeuesInFlight(t *testing.T) {
	repo := &markDeletingRepoStub{rows: []*types.Knowledge{
		{ID: "k-processing", ParseStatus: types.ParseStatusProcessing},
		{ID: "k-done", ParseStatus: types.ParseStatusCompleted},
		{ID: "k-pending", ParseStatus: types.ParseStatusPending},
	}}
	inspector := &markDeletingInspectorStub{}
	svc := &knowledgeService{repo: repo, taskInspector: inspector}
	ctx := types.WithExecutionTenant(context.Background(), 7)

	require.NoError(t, svc.MarkKnowledgesDeleting(ctx, []string{"k-processing", "k-done", "k-pending"}))
	require.Equal(t, []string{"k-processing", "k-done", "k-pending"}, repo.marked)
	require.Equal(t, []string{"k-processing", "k-pending"}, inspector.cancelled)
}

func TestMarkKnowledgesDeleting_EmptyIDsNoOp(t *testing.T) {
	repo := &markDeletingRepoStub{}
	svc := &knowledgeService{repo: repo}
	ctx := types.WithExecutionTenant(context.Background(), 7)

	require.NoError(t, svc.MarkKnowledgesDeleting(ctx, nil))
	require.Nil(t, repo.marked)
}
