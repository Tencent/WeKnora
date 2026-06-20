package router

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncLedgerStub struct {
	mu             sync.Mutex
	activeID       string
	successID      string
	jobByExecution map[string]*types.TaskJob
	successJobSel  *interfaces.TaskJobAttemptSelector
	done           chan struct{}
	jobDone        chan struct{}
	doneOnce       sync.Once
	jobDoneOnce    sync.Once
}

func newSyncLedgerStub() *syncLedgerStub {
	return &syncLedgerStub{
		jobByExecution: make(map[string]*types.TaskJob),
		done:           make(chan struct{}),
		jobDone:        make(chan struct{}),
	}
}

func (s *syncLedgerStub) CreateJobAndExecution(context.Context, *types.TaskJob, *types.TaskExecution) error {
	return nil
}
func (s *syncLedgerStub) CreateExecutionForJob(context.Context, *types.TaskExecution) error {
	return nil
}
func (s *syncLedgerStub) Summary(context.Context, interfaces.TaskJobQuery) (*interfaces.TaskJobSummary, error) {
	return nil, nil
}
func (s *syncLedgerStub) ListJobs(context.Context, interfaces.TaskJobQuery) ([]*types.TaskJob, int64, error) {
	return nil, 0, nil
}
func (s *syncLedgerStub) GetJob(context.Context, uint64, string) (*types.TaskJob, error) {
	return nil, nil
}
func (s *syncLedgerStub) GetJobByExecutionID(_ context.Context, executionID string) (*types.TaskJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobByExecution[executionID], nil
}
func (s *syncLedgerStub) GetLatestJobForScopeAttempt(context.Context, uint64, string, string, int) (*types.TaskJob, error) {
	return nil, nil
}
func (s *syncLedgerStub) ListExecutions(context.Context, uint64, string) ([]*types.TaskExecution, error) {
	return nil, nil
}
func (s *syncLedgerStub) ListExecutionsForJobs(context.Context, uint64, []string) (map[string][]*types.TaskExecution, error) {
	return nil, nil
}
func (s *syncLedgerStub) MarkJobProcessingIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkJobFinalizingIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkJobSucceededIfCurrentAttempt(_ context.Context, sel interfaces.TaskJobAttemptSelector, _ time.Time) (bool, error) {
	s.mu.Lock()
	s.successJobSel = &sel
	s.mu.Unlock()
	s.jobDoneOnce.Do(func() { close(s.jobDone) })
	return false, nil
}
func (s *syncLedgerStub) MarkJobFailedIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkJobCanceledIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkDispatched(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkDispatchFailed(context.Context, string, string, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkExecActiveIfExists(_ context.Context, executionID string, _ int, _ time.Time) (bool, error) {
	s.mu.Lock()
	s.activeID = executionID
	s.mu.Unlock()
	return true, nil
}
func (s *syncLedgerStub) MarkExecRetryingIfNonTerminal(context.Context, string, int, interfaces.TaskLedgerFailure) (bool, error) {
	return true, nil
}
func (s *syncLedgerStub) MarkExecSucceededIfNonTerminal(_ context.Context, executionID string, _ time.Time) (bool, error) {
	s.mu.Lock()
	s.successID = executionID
	s.mu.Unlock()
	s.doneOnce.Do(func() { close(s.done) })
	return true, nil
}
func (s *syncLedgerStub) MarkExecFailedIfNonTerminal(context.Context, string, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return true, nil
}
func (s *syncLedgerStub) MarkExecCanceledIfNonTerminal(context.Context, string, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return true, nil
}
func (s *syncLedgerStub) MarkExecRescheduled(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}
func (s *syncLedgerStub) MarkExecutionsCanceledForJob(context.Context, uint64, string, interfaces.TaskLedgerFailure, time.Time) (int64, error) {
	return 0, nil
}
func (s *syncLedgerStub) FindStaleDispatches(context.Context, time.Time, int) ([]*types.TaskExecution, error) {
	return nil, nil
}
func (s *syncLedgerStub) MarkStaleDispatchFailed(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) DeleteTerminalJobsFinishedBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestSyncTaskExecutor_RespectsTaskIDAndUpdatesLedger(t *testing.T) {
	exec := NewSyncTaskExecutor()
	ledger := newSyncLedgerStub()
	exec.SetLedger(ledger)
	exec.RegisterHandler("test:task", func(context.Context, *asynq.Task) error {
		return nil
	})

	info, err := exec.Enqueue(asynq.NewTask("test:task", []byte(`{}`)), asynq.TaskID("exec-fixed"), asynq.Queue("critical"))
	require.NoError(t, err)
	assert.Equal(t, "exec-fixed", info.ID)
	assert.Equal(t, "critical", info.Queue)

	select {
	case <-ledger.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sync task completion")
	}

	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	assert.Equal(t, "exec-fixed", ledger.activeID)
	assert.Equal(t, "exec-fixed", ledger.successID)
}

func TestSyncTaskExecutor_CompletesRootJobForNonDocumentKind(t *testing.T) {
	exec := NewSyncTaskExecutor()
	ledger := newSyncLedgerStub()
	ledger.jobByExecution["exec-delete"] = &types.TaskJob{
		TenantID:       9,
		Kind:           types.TaskJobKindDelete,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        "knowledge-1",
		ProcessAttempt: 4,
	}
	exec.SetLedger(ledger)
	exec.RegisterHandler("knowledge:delete", func(context.Context, *asynq.Task) error {
		return nil
	})

	_, err := exec.Enqueue(asynq.NewTask("knowledge:delete", []byte(`{}`)), asynq.TaskID("exec-delete"), asynq.MaxRetry(0))
	require.NoError(t, err)

	select {
	case <-ledger.jobDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for root job completion")
	}

	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	require.NotNil(t, ledger.successJobSel)
	assert.Equal(t, uint64(9), ledger.successJobSel.TenantID)
	assert.Equal(t, types.TaskScopeKnowledge, ledger.successJobSel.Scope)
	assert.Equal(t, "knowledge-1", ledger.successJobSel.ScopeID)
	assert.Equal(t, 4, ledger.successJobSel.ProcessAttempt)
}

func TestSyncTaskExecutor_DoesNotCompleteDocumentJobOnRootExecution(t *testing.T) {
	exec := NewSyncTaskExecutor()
	ledger := newSyncLedgerStub()
	ledger.jobByExecution["exec-document"] = &types.TaskJob{
		TenantID:       9,
		Kind:           types.TaskJobKindReparse,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        "knowledge-1",
		ProcessAttempt: 4,
	}
	exec.SetLedger(ledger)
	exec.RegisterHandler(types.TypeDocumentProcess, func(context.Context, *asynq.Task) error {
		return nil
	})

	_, err := exec.Enqueue(asynq.NewTask(types.TypeDocumentProcess, []byte(`{}`)), asynq.TaskID("exec-document"), asynq.MaxRetry(0))
	require.NoError(t, err)

	select {
	case <-ledger.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution completion")
	}

	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	assert.Nil(t, ledger.successJobSel)
}
