package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withEvaluationCancelTenant(ctx context.Context, tenantID uint64) context.Context {
	return context.WithValue(ctx, types.TenantIDContextKey, tenantID)
}

func TestCancelEvaluationPersistsRequestAndCancelsLocalRun(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(81, "cancel-local-run")
	entity.OwnerID = "cancel-owner"
	repository.register(entity)

	service := &EvaluationService{
		config:                   &config.Config{},
		dataset:                  &evaluationDeadlineDatasetStub{},
		knowledgeBaseService:     &evaluationCleanupKnowledgeBaseStub{recorder: newEvaluationCleanupRecorder()},
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        time.Hour,
	}
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() {
		runDone <- service.runEvaluation(context.Background(), detail, entity.TemporaryKnowledgeBaseID)
	}()
	runHandleKey := evaluationRunHandleKey(entity.TenantID, entity.ID)
	require.Eventually(t, func() bool {
		_, ok := service.runHandles.Load(runHandleKey)
		return ok
	}, time.Second, time.Millisecond)

	cancelDetail, err := service.CancelEvaluation(
		withEvaluationCancelTenant(context.Background(), entity.TenantID),
		entity.ID,
	)
	require.NoError(t, err)
	require.NotNil(t, cancelDetail.Task.CancelRequestedAt)
	assert.Equal(t, types.EvaluationStatueRunning, cancelDetail.Task.Status)

	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, errEvaluationTaskCancelRequested)
	case <-time.After(time.Second):
		t.Fatal("local run was not canceled after the cancel request")
	}

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, stored.Status)
	assert.Equal(t, evaluationTaskCanceledMessage, stored.ErrMsg)
	require.NotNil(t, stored.CancelRequestedAt)
	require.NotNil(t, stored.EndTime)
	assert.Nil(t, stored.LeaseExpiresAt)
}

func TestCancelEvaluationRepeatedAndTerminalKeepState(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(82, "cancel-repeat")
	entity.OwnerID = "cancel-owner"
	repository.register(entity)
	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
	}
	ctx := withEvaluationCancelTenant(context.Background(), entity.TenantID)

	first, err := service.CancelEvaluation(ctx, entity.ID)
	require.NoError(t, err)
	require.NotNil(t, first.Task.CancelRequestedAt)
	firstTime := *first.Task.CancelRequestedAt

	time.Sleep(time.Millisecond)
	second, err := service.CancelEvaluation(ctx, entity.ID)
	require.NoError(t, err)
	require.NotNil(t, second.Task.CancelRequestedAt)
	assert.Equal(t, firstTime, *second.Task.CancelRequestedAt)

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.Version, stored.Version)

	// Terminal tasks keep their original state.
	terminal, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	terminal.Status = types.EvaluationStatueSuccess
	terminal.CancelRequestedAt = nil
	endTime := time.Now().UTC()
	terminal.EndTime = &endTime
	terminal.LeaseExpiresAt = nil
	repository.register(terminal)

	third, err := service.CancelEvaluation(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueSuccess, third.Task.Status)
	assert.Nil(t, third.Task.CancelRequestedAt)
}

func TestCancelEvaluationMissingTaskReturnsNotFound(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  "cancel-owner",
	}
	_, err := service.CancelEvaluation(
		withEvaluationCancelTenant(context.Background(), 99),
		"missing-task",
	)
	require.ErrorIs(t, err, interfaces.ErrEvaluationTaskNotFound)
}

func TestRunEvaluationPublishesCanceledWhenHeartbeatObservesCancel(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(83, "cancel-via-heartbeat")
	entity.OwnerID = "cancel-owner"
	repository.register(entity)

	service := &EvaluationService{
		config:                   &config.Config{},
		dataset:                  &evaluationDeadlineDatasetStub{},
		knowledgeBaseService:     &evaluationCleanupKnowledgeBaseStub{recorder: newEvaluationCleanupRecorder()},
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        5 * time.Millisecond,
	}
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() {
		runDone <- service.runEvaluation(context.Background(), detail, entity.TemporaryKnowledgeBaseID)
	}()
	require.Eventually(t, func() bool {
		return repository.countCalls("HeartbeatTask") >= 1
	}, time.Second, time.Millisecond)

	// Another replica persists the cancel request; the local heartbeat
	// observes it on the next beat and stops the worker.
	_, err = repository.RequestCancel(context.Background(), types.EvaluationTaskCancelCommand{
		TenantID: entity.TenantID,
		TaskID:   entity.ID,
		Now:      time.Now().UTC(),
	})
	require.NoError(t, err)

	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, errEvaluationTaskCancelRequested)
	case <-time.After(time.Second):
		t.Fatal("run did not stop after the heartbeat observed the cancel request")
	}

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, stored.Status)
	assert.Equal(t, evaluationTaskCanceledMessage, stored.ErrMsg)
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))
}

func TestRunEvaluationRepublishesCanceledAfterTerminalCancelRace(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(84, "cancel-terminal-race")
	entity.OwnerID = "cancel-owner"
	repository.register(entity)

	// The knowledge base cleanup blocks until the cancel request is
	// persisted, so the cancel lands deterministically after execution
	// succeeded but before the terminal publish.
	cleanupEntered := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	service := &EvaluationService{
		config:           &config.Config{},
		dataset:          &evaluationCleanupContextDatasetStub{},
		knowledgeService: &evaluationCleanupContextKnowledgeStub{recorder: &evaluationCleanupContextRecorder{}},
		knowledgeBaseService: &evaluationHeartbeatCleanupKnowledgeBaseStub{
			entered: cleanupEntered,
			release: cleanupRelease,
		},
		sessionService:           &evaluationCleanupContextSessionStub{},
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        time.Hour,
	}
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runDone := make(chan error, 1)
	go func() {
		runDone <- service.runEvaluation(context.Background(), detail, entity.TemporaryKnowledgeBaseID)
	}()
	select {
	case <-cleanupEntered:
	case <-time.After(time.Second):
		t.Fatal("evaluation did not reach the knowledge base cleanup")
	}
	_, err = repository.RequestCancel(context.Background(), types.EvaluationTaskCancelCommand{
		TenantID: entity.TenantID,
		TaskID:   entity.ID,
		Now:      time.Now().UTC(),
	})
	require.NoError(t, err)
	close(cleanupRelease)

	// The Success CAS fails on the cancel truth; the executor re-reads the
	// task and publishes Canceled with the same version.
	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, errEvaluationTaskCancelRequested)
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish after the terminal cancel race")
	}

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, stored.Status)
	assert.Equal(t, evaluationTaskCanceledMessage, stored.ErrMsg)
	require.NotNil(t, stored.CancelRequestedAt)
}

func TestEvaluationTaskRecoveryPublishesCanceledForExpiredTaskWithCancelRequest(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(85, "expired-cancel", now)
	cancelAt := now.Add(-2 * time.Minute)
	entity.CancelRequestedAt = &cancelAt
	repository.register(entity)

	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(repository, recorder, nil, nil, now)
	require.NoError(t, runner.runOnce(context.Background()))

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, stored.Status)
	assert.Equal(t, evaluationTaskCanceledMessage, stored.ErrMsg)
	assert.Equal(t, 2, len(recorder.snapshot()))
}

func TestEvaluationTaskRecoveryPublishesCanceledWhenRequestArrivesDuringCleanup(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(86, "cancel-during-recovery", now)
	repository.register(entity)

	cleanupEntered := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	runner := NewEvaluationTaskRecoveryRunner(
		repository,
		&evaluationRecoveryTenantStub{},
		&evaluationHeartbeatCleanupKnowledgeBaseStub{
			entered: cleanupEntered,
			release: cleanupRelease,
		},
		nil,
	)
	runner.ownerID = "recovery-owner"
	runner.now = func() time.Time { return now }
	runner.recoveryLeaseDuration = 2 * time.Minute

	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.runOnce(context.Background())
	}()
	select {
	case <-cleanupEntered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not reach knowledge base cleanup")
	}
	_, err := repository.RequestCancel(context.Background(), types.EvaluationTaskCancelCommand{
		TenantID: entity.TenantID,
		TaskID:   entity.ID,
		Now:      now.Add(time.Second),
	})
	require.NoError(t, err)
	close(cleanupRelease)

	select {
	case runErr := <-runDone:
		require.NoError(t, runErr)
	case <-time.After(time.Second):
		t.Fatal("recovery did not finish after cleanup was released")
	}

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, stored.Status)
	assert.Equal(t, evaluationTaskCanceledMessage, stored.ErrMsg)
	require.NotNil(t, stored.CancelRequestedAt)
}
