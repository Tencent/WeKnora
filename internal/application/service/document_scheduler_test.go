package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestDocumentExecutionRetryBudgetAndPanic(t *testing.T) {
	for failures := 0; failures < 4; failures++ {
		err := invokeDocumentTask(context.Background(), failures, func(ctx context.Context) error {
			got, retryLimit, ok := types.TaskRetryMetadataFromContext(ctx)
			require.True(t, ok)
			require.Equal(t, failures, got)
			require.Equal(t, 3, retryLimit)
			return errors.New("model unavailable")
		})
		outcome := documentOutcome(err, failures)
		if failures < 3 {
			require.Equal(t, "retry", outcome)
		} else {
			require.Equal(t, "terminal", outcome)
		}
	}
	require.Equal(t, "success", documentOutcome(nil, 0))
	require.Equal(t, "terminal", documentOutcome(asynq.SkipRetry, 0))
	err := invokeDocumentTask(context.Background(), 0, func(context.Context) error { panic("reader crash") })
	require.ErrorContains(t, err, "reader crash")
}

func TestDocumentRepresentativeIDsBounded(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 1000; i++ {
		ids[documentDispatchID(42, i%2)] = true
	}
	require.Len(t, ids, 2)
	require.NotEqual(t, documentDispatchID(42, 0), documentDispatchID(43, 0))
}

type documentTestScheduler struct {
	interfaces.TaskPendingOpsRepository
	op        *types.TaskPendingOp
	outcomes  []string
	finishErr error
	loseLease bool
}

func (r *documentTestScheduler) EnqueueDocument(_ context.Context, op *types.TaskPendingOp) (bool, error) {
	op.ID = 71
	r.op = op
	return true, nil
}

func (r *documentTestScheduler) ClaimDocument(context.Context, int, time.Duration) (*types.DocumentClaim, error) {
	if r.op == nil {
		return nil, nil
	}
	return &types.DocumentClaim{Op: r.op, Owner: "owner"}, nil
}

func (r *documentTestScheduler) RenewDocument(context.Context, *types.DocumentClaim, time.Duration) (bool, error) {
	return !r.loseLease, nil
}

func (r *documentTestScheduler) FinishDocument(
	_ context.Context, _ *types.DocumentClaim, outcome string, _ time.Time, _ string,
) (bool, error) {
	r.outcomes = append(r.outcomes, outcome)
	if r.finishErr != nil {
		return false, r.finishErr
	}
	if outcome == "retry" {
		r.op.FailCount++
	} else if outcome != "release" {
		r.op = nil
	}
	return true, nil
}
func (r *documentTestScheduler) RecoverDocuments(context.Context) error { return nil }
func (r *documentTestScheduler) DocumentTenants(context.Context) ([]uint64, error) {
	return []uint64{1}, nil
}

func (r *documentTestScheduler) HasDocumentWork(context.Context, uint64) (bool, error) {
	return r.op != nil, nil
}

func (r *documentTestScheduler) DocumentKnowledgeIDs(context.Context) (map[string]struct{}, error) {
	return nil, nil
}

func (r *documentTestScheduler) DeleteByDedupKey(
	_ context.Context, taskType, _, _, _, _ string,
) error {
	if taskType == types.TypeDocumentProcess {
		r.op = nil
	}
	return nil
}

func (r *documentTestScheduler) SnapshotDocumentPending(context.Context, uint64, string) ([]int64, error) {
	if r.op == nil {
		return nil, nil
	}
	return []int64{r.op.ID}, nil
}

func (r *documentTestScheduler) DeleteDocumentPending(_ context.Context, _ uint64, _ string, ids []int64) error {
	for _, id := range ids {
		if r.op != nil && r.op.ID == id {
			r.op = nil
		}
	}
	return nil
}

type documentTestQueue struct {
	tasks []*asynq.Task
	err   error
}

func (q *documentTestQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{}, q.err
}

type documentTestKnowledge struct {
	interfaces.KnowledgeRepository
	row *types.Knowledge
	err error
}

func (r *documentTestKnowledge) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.row, r.err
}

func (r *documentTestKnowledge) UpdateKnowledgeColumns(
	_ context.Context, _ string, values map[string]interface{},
) error {
	if status, ok := values["parse_status"].(string); ok {
		r.row.ParseStatus = status
	}
	return nil
}

type documentTestTenant struct {
	interfaces.TenantRepository
	calls int
	err   error
	crash bool
}

func (r *documentTestTenant) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	r.calls++
	if r.crash {
		panic("reader crash")
	}
	return nil, r.err
}

func documentTestService() (*knowledgeService, *documentTestScheduler, *documentTestQueue, *documentTestTenant) {
	raw, _ := json.Marshal(types.DocumentProcessPayload{
		TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc", Attempt: 7, Passages: []string{"original"},
	})
	r := &documentTestScheduler{op: &types.TaskPendingOp{
		ID: 71, TenantID: 1, ScopeID: "kb", DedupKey: "doc", Payload: raw,
	}}
	q := &documentTestQueue{}
	tenant := &documentTestTenant{err: errors.New("temporary reader error")}
	s := &knowledgeService{
		taskPendingRepo: r, task: q, tenantRepo: tenant,
		repo: &documentTestKnowledge{row: &types.Knowledge{
			ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusPending,
		}},
	}
	return s, r, q, tenant
}

func documentTestToken() *asynq.Task {
	return asynq.NewTask(types.TypeDocumentDispatch, []byte(`{"tenant_id":1,"phase":0}`))
}

func TestDocumentDispatchFourExecutionsAndPanic(t *testing.T) {
	for _, crash := range []bool{false, true} {
		s, r, _, tenant := documentTestService()
		tenant.crash = crash
		for i := 0; i < 6; i++ {
			require.NoError(t, s.ProcessDocumentDispatch(context.Background(), documentTestToken()))
		}
		require.Equal(t, 4, tenant.calls)
		require.Equal(t, []string{"retry", "retry", "retry", "terminal"}, r.outcomes)
	}
}

func TestDocumentDispatchTerminalReplayAndInfrastructureFailure(t *testing.T) {
	for _, status := range []string{
		types.ParseStatusCompleted, types.ParseStatusFinalizing, types.ParseStatusCancelled, types.ParseStatusDeleting,
	} {
		s, r, _, tenant := documentTestService()
		s.repo.(*documentTestKnowledge).row.ParseStatus = status
		require.NoError(t, s.ProcessDocumentDispatch(context.Background(), documentTestToken()))
		require.Zero(t, tenant.calls)
		require.Equal(t, []string{"success"}, r.outcomes)
	}
	s, r, _, tenant := documentTestService()
	s.repo.(*documentTestKnowledge).err = errors.New("database unavailable")
	require.Error(t, s.ProcessDocumentDispatch(context.Background(), documentTestToken()))
	require.Zero(t, tenant.calls)
	require.Equal(t, []string{"release"}, r.outcomes)
	require.Zero(t, r.op.FailCount)
}

func TestDocumentDurableAcceptancePreservesPayloadOnWakeupFailure(t *testing.T) {
	s, r, q, _ := documentTestService()
	q.err = errors.New("Redis disconnected")
	original := append([]byte(nil), r.op.Payload...)
	info, err := s.enqueueDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, original))
	require.NoError(t, err)
	require.Equal(t, "document-pending-71", info.ID)
	require.JSONEq(t, string(original), string(r.op.Payload))
	require.Equal(t, types.TypeDocumentDispatch, q.tasks[0].Type())
}

func TestDocumentLegacyRetryCountPreserved(t *testing.T) {
	s, r, _, _ := documentTestService()
	ctx := types.WithTaskRetryMetadata(context.Background(), 2, 3)
	require.NoError(t, s.AdmitLegacyDocument(ctx, asynq.NewTask(types.TypeDocumentProcess, r.op.Payload)))
	require.Equal(t, 2, r.op.FailCount)
}

type documentTestInspector struct {
	interfaces.TaskInspector
	interfaces.RuntimeTaskInspector
	state      types.RuntimeTaskState
	inspectErr error
	runs       []string
}

func (i *documentTestInspector) GetRuntimeTask(
	_ context.Context, _, id string,
) (*types.RuntimeTaskInfo, bool, error) {
	if i.inspectErr != nil {
		return nil, true, i.inspectErr
	}
	return &types.RuntimeTaskInfo{ID: id, Type: types.TypeDocumentDispatch, State: i.state}, true, nil
}

func (i *documentTestInspector) RunRuntimeTask(_ context.Context, _, id string) (bool, error) {
	i.runs = append(i.runs, id)
	return true, nil
}

func TestDocumentReconcileArchivedAndUnknownTokens(t *testing.T) {
	s, _, q, _ := documentTestService()
	inspector := &documentTestInspector{state: types.RuntimeTaskArchived}
	s.taskInspector = inspector
	require.NoError(t, s.ReconcileDocuments(context.Background()))
	require.Equal(t, []string{documentDispatchID(1, 0), documentDispatchID(1, 1)}, inspector.runs)
	require.Empty(t, q.tasks)
	inspector.runs = nil
	inspector.inspectErr = errors.New("Redis timeout")
	require.Error(t, s.ReconcileDocuments(context.Background()))
	require.Empty(t, inspector.runs)
	require.Empty(t, q.tasks)
	inspector.inspectErr = asynq.ErrTaskNotFound
	require.NoError(t, s.ReconcileDocuments(context.Background()))
	require.Len(t, q.tasks, 2)
}

func TestDocumentDispatcherRearmsOriginNotSelectedTenant(t *testing.T) {
	s, r, q, _ := documentTestService()
	r.op.TenantID = 2
	// Transport origin remains tenant 1 even when global admission picks another tenant.
	require.NoError(t, s.ProcessDocumentDispatch(context.Background(), documentTestToken()))
	require.NotEmpty(t, q.tasks)
	var token documentDispatchPayload
	require.NoError(t, json.Unmarshal(q.tasks[len(q.tasks)-1].Payload(), &token))
	require.Equal(t, uint64(1), token.TenantID)
	require.Equal(t, 1, token.Phase)
}

func TestDocumentLegacyFinalizingReplayDoesNotEnqueue(t *testing.T) {
	s, r, q, _ := documentTestService()
	s.repo.(*documentTestKnowledge).row.ParseStatus = types.ParseStatusFinalizing
	require.NoError(t, s.AdmitLegacyDocument(context.Background(),
		asynq.NewTask(types.TypeDocumentProcess, r.op.Payload)))
	require.Empty(t, q.tasks)
}

func TestDocumentLostLeaseBeforeProcessing(t *testing.T) {
	s, r, _, tenant := documentTestService()
	r.loseLease = true
	require.NoError(t, s.ProcessDocumentDispatch(context.Background(), documentTestToken()))
	require.Zero(t, tenant.calls)
}

type documentTransportProbe struct {
	context.Context
	reads int
}

func (c *documentTransportProbe) Value(key any) any {
	if reflect.TypeOf(key).PkgPath() == "github.com/hibiken/asynq/internal/context" {
		c.reads++
	}
	return c.Context.Value(key)
}

func TestDocumentNestedRetryUsesExecutionMetadataFirst(t *testing.T) {
	transport := &documentTransportProbe{Context: context.Background()}
	ctx := types.WithTaskRetryMetadata(transport, 3, 3)
	require.False(t, summaryTaskWillRetry(ctx))
	require.Zero(t, transport.reads, "explicit document execution budget must override transport retry state")
}

type documentFailureTestRepo struct {
	*documentTestScheduler
	failures []*types.TaskPendingOp
}

func (r *documentFailureTestRepo) RecoverDocumentFailures(context.Context) ([]*types.TaskPendingOp, error) {
	failures := r.failures
	r.failures = nil
	return failures, nil
}

type documentFailureTestTracker struct {
	SpanTracker
	latest    int
	finalized []int
}

func (t *documentFailureTestTracker) LatestAttempt(context.Context, string) int { return t.latest }
func (t *documentFailureTestTracker) FinalizeAttempt(
	_ context.Context, _ string, attempt int, _ string, _ types.JSONMap, _, _ string,
) {
	t.finalized = append(t.finalized, attempt)
}

func TestDocumentRecoveryClosesOnlyOriginalTerminalAttempt(t *testing.T) {
	s, r, _, _ := documentTestService()
	s.taskPendingRepo = &documentFailureTestRepo{documentTestScheduler: r, failures: []*types.TaskPendingOp{r.op}}
	tracker := &documentFailureTestTracker{}
	s.spanTracker = tracker
	require.NoError(t, s.ReconcileDocuments(context.Background()))
	require.Equal(t, []int{7}, tracker.finalized)
	require.NoError(t, s.ReconcileDocuments(context.Background()))
	require.Equal(t, []int{7}, tracker.finalized)
	tracker.latest = 8
	s.taskPendingRepo.(*documentFailureTestRepo).failures = []*types.TaskPendingOp{r.op}
	require.NoError(t, s.ReconcileDocuments(context.Background()))
	require.Equal(t, []int{7}, tracker.finalized)
}

func TestDocumentOlderReparseCleanupPreservesNewPendingAttempt(t *testing.T) {
	s, r, _, _ := documentTestService()
	s.prepareWikiForReparse(context.Background(), s.repo.(*documentTestKnowledge).row)
	require.NotNil(t, r.op, "new attempt replacement must be serialized by durable enqueue")
}

func TestDocumentRepeatedCancellationScrubsDurableWork(t *testing.T) {
	s, r, _, _ := documentTestService()
	s.repo.(*documentTestKnowledge).row.ParseStatus = types.ParseStatusCancelled
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	_, err := s.CancelKnowledgeParse(ctx, "doc")
	require.NoError(t, err)
	require.Nil(t, r.op)
}

func TestDocumentPipelineFinalizingReplay(t *testing.T) {
	s, r, _, tenant := documentTestService()
	s.repo.(*documentTestKnowledge).row.ParseStatus = types.ParseStatusFinalizing
	tenant.err = nil
	err := invokeDocumentTask(context.Background(), 0, func(ctx context.Context) error {
		return s.ProcessDocument(ctx, asynq.NewTask(types.TypeDocumentProcess, r.op.Payload))
	})
	require.NoError(t, err, "a handed-off document must return before accessing parsing dependencies")
}

type documentCancelTestInspector struct {
	interfaces.TaskInspector
	onCancel func()
}

func (i *documentCancelTestInspector) CancelTasksForKnowledge(context.Context, string) (int, int, error) {
	i.onCancel()
	return 0, 0, nil
}

func TestDocumentCancellationKeepsConcurrentNewAttempt(t *testing.T) {
	for _, status := range []string{types.ParseStatusPending, types.ParseStatusCancelled} {
		t.Run(status, func(t *testing.T) {
			s, r, _, _ := documentTestService()
			s.repo.(*documentTestKnowledge).row.ParseStatus = status
			s.taskInspector = &documentCancelTestInspector{onCancel: func() {
				next := *r.op
				next.ID = 72
				r.op = &next
				s.repo.(*documentTestKnowledge).row.ParseStatus = types.ParseStatusPending
			}}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			_, err := s.CancelKnowledgeParse(ctx, "doc")
			require.NoError(t, err)
			require.NotNil(t, r.op, "old cancel must not delete the concurrently enqueued reparse")
			require.Equal(t, int64(72), r.op.ID)
		})
	}
}
