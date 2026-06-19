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
	mu        sync.Mutex
	activeID  string
	successID string
	done      chan struct{}
}

func newSyncLedgerStub() *syncLedgerStub {
	return &syncLedgerStub{done: make(chan struct{})}
}

func (s *syncLedgerStub) CreateJobAndExecution(context.Context, *types.TaskJob, *types.TaskExecution) error {
	return nil
}
func (s *syncLedgerStub) MarkJobProcessingIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkJobFinalizingIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector) (bool, error) {
	return false, nil
}
func (s *syncLedgerStub) MarkJobSucceededIfCurrentAttempt(context.Context, interfaces.TaskJobAttemptSelector, time.Time) (bool, error) {
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
	close(s.done)
	return true, nil
}
func (s *syncLedgerStub) MarkExecFailedIfNonTerminal(context.Context, string, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return true, nil
}
func (s *syncLedgerStub) MarkExecCanceledIfNonTerminal(context.Context, string, interfaces.TaskLedgerFailure, time.Time) (bool, error) {
	return true, nil
}
func (s *syncLedgerStub) PrepareManualRetry(context.Context, string, string, string, string, time.Time) (*types.TaskExecution, bool, error) {
	return nil, false, nil
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
