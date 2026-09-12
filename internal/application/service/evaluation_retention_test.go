package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRetentionFixture(
	repository *fakeEvaluationTaskRepository,
	id string,
	status types.EvaluationStatue,
	endTime *time.Time,
) {
	entity := newPersistentLifecycleEntity(99, id)
	entity.Status = status
	entity.EndTime = endTime
	entity.LeaseExpiresAt = nil
	repository.register(entity)
}

func TestEvaluationTaskRetentionRunnerDrainsBacklogInBatches(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -91)
	recent := now.AddDate(0, 0, -10)
	for _, id := range []string{"old-a", "old-b", "old-c"} {
		newRetentionFixture(repository, id, types.EvaluationStatueSuccess, &old)
	}
	newRetentionFixture(repository, "recent", types.EvaluationStatueSuccess, &recent)
	newRetentionFixture(repository, "active", types.EvaluationStatueRunning, &old)

	runner := NewEvaluationTaskRetentionRunner(repository, 90)
	runner.now = func() time.Time { return now }
	runner.batchSize = 2

	require.NoError(t, runner.runOnce(context.Background()))
	// Three expired rows drained across batches of two; recent and active stay.
	assert.Equal(t, 2, repository.countCalls("DeleteExpiredTerminalTasks"))
	for _, id := range []string{"recent", "active"} {
		_, err := repository.get(99, id)
		require.NoError(t, err, "%s must survive retention", id)
	}
	for _, id := range []string{"old-a", "old-b", "old-c"} {
		_, err := repository.get(99, id)
		require.Error(t, err, "%s must be physically deleted", id)
	}
}

func TestEvaluationTaskRetentionRunnerPropagatesDatabaseErrors(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.retentionErr = errors.New("database unavailable")
	runner := NewEvaluationTaskRetentionRunner(repository, 90)

	err := runner.runOnce(context.Background())
	require.ErrorContains(t, err, "database unavailable")
}

func TestEvaluationTaskRetentionRunnerHonorsCancellation(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -400)
	for range 3 {
		newRetentionFixture(repository, "stale", types.EvaluationStatueSuccess, &old)
	}
	runner := NewEvaluationTaskRetentionRunner(repository, 90)
	runner.batchSize = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runner.runOnce(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEvaluationTaskRetentionRunnerStartsAfterDelayAndStops(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	runner := NewEvaluationTaskRetentionRunner(repository, 90)
	runner.startDelay = 10 * time.Millisecond
	runner.interval = time.Hour

	runner.Start(context.Background())
	require.Eventually(t, func() bool {
		return repository.countCalls("DeleteExpiredTerminalTasks") == 1
	}, time.Second, time.Millisecond)
	runner.Stop()
	runner.Stop()
}

func TestEvaluationTaskRetentionRunnerDisabledWithoutPositiveWindow(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	runner := NewEvaluationTaskRetentionRunner(repository, 0)
	runner.startDelay = time.Millisecond

	runner.Start(context.Background())
	runner.Stop()
	assert.Equal(t, 0, repository.countCalls("DeleteExpiredTerminalTasks"))

	err := runner.runOnce(context.Background())
	require.Error(t, err)
}
