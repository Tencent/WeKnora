package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// batchPendingRepo implements both KB guards with a real batch call, so
// enqueueFinalize exercises the one-transaction path.
type batchPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	batchCalls  [][]*types.TaskPendingOp
	batchAccept int
	batchErr    error
}

func (r *batchPendingRepo) EnqueueIfKnowledgeBaseActive(
	_ context.Context, _ *types.TaskPendingOp,
) (bool, error) {
	return false, errors.New("single-row guard must not run when the batch guard is present")
}

func (r *batchPendingRepo) EnqueueBatchIfKnowledgeBaseActive(
	_ context.Context, ops []*types.TaskPendingOp,
) (int, error) {
	r.batchCalls = append(r.batchCalls, ops)
	if r.batchErr != nil {
		return 0, r.batchErr
	}
	return r.batchAccept, nil
}

// rowPendingRepo has only the single-row guard, matching repositories and
// test doubles predating the batch guard: enqueueFinalize must fall back to
// the per-row loop.
type rowPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	rows     []*types.TaskPendingOp
	rejectOp string
	rowErr   error
}

func (r *rowPendingRepo) EnqueueIfKnowledgeBaseActive(
	_ context.Context, op *types.TaskPendingOp,
) (bool, error) {
	if r.rowErr != nil {
		return false, r.rowErr
	}
	if op.Op == r.rejectOp {
		return false, errors.New("row rejected")
	}
	r.rows = append(r.rows, op)
	return true, nil
}

type recordingWikiEnqueuer struct {
	tasks []*asynq.Task
}

func (e *recordingWikiEnqueuer) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, t)
	return &asynq.TaskInfo{ID: "queued", Type: t.Type()}, nil
}

func newFinalizeTestService(repo interfaces.TaskPendingOpsRepository) (*wikiIngestService, *recordingWikiEnqueuer) {
	enqueuer := &recordingWikiEnqueuer{}
	return &wikiIngestService{
		pendingRepo: repo,
		task:        enqueuer,
	}, enqueuer
}

func finalizeTestPayload() WikiIngestPayload {
	return WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"}
}

// The batch path writes every finalize row through one guarded call and
// reports the accepted/failed split, so a dropped row is countable instead of
// silent (#5716).
func TestEnqueueFinalizeBatchPathCountsAndSchedules(t *testing.T) {
	repo := &batchPendingRepo{batchAccept: 3}
	svc, enqueuer := newFinalizeTestService(repo)

	enqueued, failed := svc.enqueueFinalize(
		context.Background(), finalizeTestPayload(),
		[]string{"slug-a", "slug-b"}, map[string]string{"slug-a": "A"},
		[]wikiFinalizeChange{{Action: wikiFinalizeAdded}}, nil,
	)

	require.Equal(t, 3, enqueued)
	require.Equal(t, 0, failed)
	require.Len(t, repo.batchCalls, 1, "all rows must share one batch call")
	assert.Len(t, repo.batchCalls[0], 3, "2 slug rows + 1 change row")
	require.Len(t, enqueuer.tasks, 1, "an accepted batch schedules the debounced finalize")
	assert.Equal(t, types.TypeWikiFinalize, enqueuer.tasks[0].Type())
}

// A failing batch reports every row as failed and schedules nothing, instead
// of the old behavior where one accepted row hid the rest of the loss.
func TestEnqueueFinalizeBatchFailureIsTotalAndVisible(t *testing.T) {
	repo := &batchPendingRepo{batchErr: errors.New("db unavailable")}
	svc, enqueuer := newFinalizeTestService(repo)

	enqueued, failed := svc.enqueueFinalize(
		context.Background(), finalizeTestPayload(),
		[]string{"slug-a"}, nil, nil, nil,
	)

	assert.Zero(t, enqueued)
	assert.Equal(t, 1, failed)
	assert.Empty(t, enqueuer.tasks, "nothing accepted means no finalize trigger")
}

// A partial batch acceptance (deleted KB mid-flight) is visible as a
// nonzero failed count.
func TestEnqueueFinalizeBatchPartialAcceptanceIsVisible(t *testing.T) {
	repo := &batchPendingRepo{batchAccept: 1}
	svc, enqueuer := newFinalizeTestService(repo)

	enqueued, failed := svc.enqueueFinalize(
		context.Background(), finalizeTestPayload(),
		[]string{"slug-a", "slug-b", "slug-c"}, nil, nil, nil,
	)

	assert.Equal(t, 1, enqueued)
	assert.Equal(t, 2, failed)
	assert.Len(t, enqueuer.tasks, 1, "any accepted row still schedules the drain")
}

// Repositories without the batch guard keep the per-row behavior, now with
// per-row accounting.
func TestEnqueueFinalizeRowFallbackCounts(t *testing.T) {
	repo := &rowPendingRepo{rejectOp: wikiFinalizeOpChange}
	svc, enqueuer := newFinalizeTestService(repo)

	enqueued, failed := svc.enqueueFinalize(
		context.Background(), finalizeTestPayload(),
		[]string{"slug-a", "slug-b"}, nil,
		[]wikiFinalizeChange{{Action: wikiFinalizeRemoved}}, nil,
	)

	assert.Equal(t, 2, enqueued)
	assert.Equal(t, 1, failed)
	assert.Len(t, repo.rows, 2, "only the slug rows reach the row guard")
	assert.Len(t, enqueuer.tasks, 1)
}

// The enqueue budget must scale with the row count so a thousand-page batch
// is not racing a flat window, while staying capped for runaway batches.
func TestWikiFinalizeEnqueueBudgetScalesWithRows(t *testing.T) {
	assert.Equal(t, 30*time.Second, wikiFinalizeEnqueueBudget(0))
	assert.Equal(t, 80*time.Second, wikiFinalizeEnqueueBudget(1000))
	assert.Equal(t, 10*time.Minute, wikiFinalizeEnqueueBudget(1_000_000))
}
