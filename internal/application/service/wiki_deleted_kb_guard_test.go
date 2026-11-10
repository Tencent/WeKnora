package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiKBGuardPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	accepted   bool
	guardErr   error
	guardedOps []*types.TaskPendingOp
	deleteErr  error
	deletedKBs []string
	rows       []*types.TaskPendingOp
}

func (r *wikiKBGuardPendingRepo) EnqueueIfKnowledgeBaseActive(
	_ context.Context,
	op *types.TaskPendingOp,
) (bool, error) {
	r.guardedOps = append(r.guardedOps, op)
	return r.accepted, r.guardErr
}

func (r *wikiKBGuardPendingRepo) DeleteByScope(_ context.Context, scope, scopeID string) error {
	if scope == types.TaskScopeKnowledgeBase {
		r.deletedKBs = append(r.deletedKBs, scopeID)
	}
	return r.deleteErr
}

func (r *wikiKBGuardPendingRepo) PeekBatch(
	context.Context,
	string,
	string,
	string,
	int,
) ([]*types.TaskPendingOp, error) {
	return r.rows, nil
}

type wikiGuardTaskQueue struct {
	interfaces.TaskEnqueuer
	tasks []*asynq.Task
}

func (q *wikiGuardTaskQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: "guard-test", Type: task.Type()}, nil
}

type wikiGuardKBService struct {
	interfaces.KnowledgeBaseService
	kb  *types.KnowledgeBase
	err error
}

func (s *wikiGuardKBService) GetKnowledgeBaseByIDOnly(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return s.kb, s.err
}

func TestEnqueueWikiWorkSkipsDeletedKnowledgeBase(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, interfaces.TaskEnqueuer, interfaces.TaskPendingOpsRepository)
	}{
		{
			name: "ingest",
			run: func(ctx context.Context, task interfaces.TaskEnqueuer, repo interfaces.TaskPendingOpsRepository) {
				EnqueueWikiIngest(ctx, task, repo, 7, "kb-deleted", "knowledge-1")
			},
		},
		{
			name: "retract",
			run: func(ctx context.Context, task interfaces.TaskEnqueuer, repo interfaces.TaskPendingOpsRepository) {
				EnqueueWikiRetract(ctx, task, repo, WikiRetractPayload{
					TenantID: 7, KnowledgeBaseID: "kb-deleted", KnowledgeID: "knowledge-1",
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &wikiKBGuardPendingRepo{accepted: false}
			queue := &wikiGuardTaskQueue{}
			test.run(context.Background(), queue, repo)

			require.Len(t, repo.guardedOps, 1)
			assert.Equal(t, "kb-deleted", repo.guardedOps[0].ScopeID)
			assert.Empty(t, queue.tasks)
		})
	}
}

func TestEnqueueWikiFinalizeOnlySchedulesAcceptedRows(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted", true: "active"}[accepted], func(t *testing.T) {
			repo := &wikiKBGuardPendingRepo{accepted: accepted}
			queue := &wikiGuardTaskQueue{}
			svc := &wikiIngestService{pendingRepo: repo, task: queue}

			svc.enqueueFinalize(
				context.Background(),
				WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
				[]string{"slug-1"},
				map[string]string{"slug-1": "Title"},
				[]wikiFinalizeChange{{Action: wikiFinalizeAdded, DocTitle: "Document"}},
				[]string{"folder-1"},
			)

			require.Len(t, repo.guardedOps, 3)
			if accepted {
				require.Len(t, queue.tasks, 1)
				assert.Equal(t, types.TypeWikiFinalize, queue.tasks[0].Type())
			} else {
				assert.Empty(t, queue.tasks)
			}
		})
	}
}

func TestWikiHandlersDrainDeletedKnowledgeBaseQueue(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-deleted"})
	require.NoError(t, err)

	for _, taskType := range []string{types.TypeWikiIngest, types.TypeWikiFinalize} {
		t.Run(taskType, func(t *testing.T) {
			repo := &wikiKBGuardPendingRepo{
				rows: []*types.TaskPendingOp{{ID: 1, ScopeID: "kb-deleted"}},
			}
			svc := &wikiIngestService{
				kbService:   &wikiGuardKBService{err: apprepo.ErrKnowledgeBaseNotFound},
				pendingRepo: repo,
			}

			err := svc.Handle(context.Background(), asynq.NewTask(taskType, payload))

			require.NoError(t, err)
			assert.Equal(t, []string{"kb-deleted"}, repo.deletedKBs)
		})
	}
}

func TestWikiDeletedKnowledgeBaseCleanupFailureRetries(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-deleted"})
	require.NoError(t, err)
	wantErr := errors.New("cleanup failed")
	repo := &wikiKBGuardPendingRepo{deleteErr: wantErr}
	svc := &wikiIngestService{
		kbService:   &wikiGuardKBService{err: apprepo.ErrKnowledgeBaseNotFound},
		pendingRepo: repo,
	}

	err = svc.ProcessWikiIngest(
		context.Background(),
		asynq.NewTask(types.TypeWikiIngest, payload),
	)

	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, []string{"kb-deleted"}, repo.deletedKBs)
}
