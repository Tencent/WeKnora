package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationHeartbeatRenewsRunningLeaseWithoutChangingVersion(t *testing.T) {
	now := time.Now().UTC()
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(41, "heartbeat-version")
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "heartbeat-owner"
	entity.Version = 7
	entity.HeartbeatAt = now.Add(-time.Minute)
	currentLease := now.Add(time.Minute)
	entity.LeaseExpiresAt = &currentLease
	repository.register(entity)

	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        time.Hour,
		runningLeaseDuration:     2 * time.Minute,
	}
	runState, err := newEvaluationRunState(entity)
	require.NoError(t, err)
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	heartbeat := service.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	require.NotNil(t, heartbeat)
	require.Eventually(t, func() bool {
		return repository.countCalls("HeartbeatTask") == 1
	}, time.Second, time.Millisecond)
	require.NoError(t, heartbeat.StopAndWait())

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), stored.Version)
	assert.True(t, stored.HeartbeatAt.After(entity.HeartbeatAt))
	require.NotNil(t, stored.LeaseExpiresAt)
	assert.Equal(t, 2*time.Minute, stored.LeaseExpiresAt.Sub(stored.HeartbeatAt))
}

func TestEvaluationHeartbeatOwnershipLossCancelsRun(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErr = interfaces.ErrEvaluationTaskOwnerConflict
	entity := newPersistentLifecycleEntity(42, "heartbeat-owner-lost")
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "heartbeat-owner"
	entity.Version = 2
	repository.register(entity)

	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        time.Hour,
	}
	runState, err := newEvaluationRunState(entity)
	require.NoError(t, err)
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	heartbeat := service.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	require.NotNil(t, heartbeat)
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("run context was not canceled after heartbeat ownership loss")
	}
	assert.ErrorIs(t, context.Cause(runCtx), interfaces.ErrEvaluationTaskOwnerConflict)
	assert.ErrorIs(t, heartbeat.StopAndWait(), interfaces.ErrEvaluationTaskOwnerConflict)
}

func TestEvaluationHeartbeatRetriesTransientFailureWithoutCancelingRun(t *testing.T) {
	transientErr := errors.New("heartbeat database unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErrs = []error{transientErr, nil}
	entity := newPersistentLifecycleEntity(42, "heartbeat-transient")
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "heartbeat-owner"
	entity.Version = 2
	leaseExpiresAt := time.Now().UTC().Add(time.Minute)
	entity.LeaseExpiresAt = &leaseExpiresAt
	repository.register(entity)

	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        5 * time.Millisecond,
	}
	runState, err := newEvaluationRunState(entity)
	require.NoError(t, err)
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	heartbeat := service.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	require.NotNil(t, heartbeat)
	require.Eventually(t, func() bool {
		return repository.countCalls("HeartbeatTask") >= 2
	}, time.Second, time.Millisecond)
	assert.NoError(t, runCtx.Err())
	assert.NoError(t, context.Cause(runCtx))
	require.NoError(t, heartbeat.StopAndWait())

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.True(t, stored.HeartbeatAt.After(entity.HeartbeatAt))
}

func TestEvaluationHeartbeatBoundsEachCallAndRetriesTimeout(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatBlockCount = 1
	now := time.Now().UTC()
	entity := newPersistentLifecycleEntity(42, "heartbeat-call-timeout")
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "heartbeat-owner"
	entity.Version = 2
	entity.HeartbeatAt = now
	leaseExpiresAt := now.Add(time.Minute)
	entity.LeaseExpiresAt = &leaseExpiresAt
	repository.register(entity)

	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        5 * time.Millisecond,
		heartbeatTimeout:         10 * time.Millisecond,
	}
	runState, err := newEvaluationRunState(entity)
	require.NoError(t, err)
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	startedAt := time.Now()
	heartbeat := service.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	assert.Less(t, time.Since(startedAt), 250*time.Millisecond)
	require.Eventually(t, func() bool {
		return repository.countCalls("HeartbeatTask") >= 2
	}, time.Second, time.Millisecond)
	assert.NoError(t, runCtx.Err())
	require.NoError(t, heartbeat.StopAndWait())
}

func TestEvaluationHeartbeatStopsRunAfterTransientFailuresOutliveLease(t *testing.T) {
	transientErr := errors.New("heartbeat database unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErr = transientErr
	now := time.Now().UTC()
	entity := newPersistentLifecycleEntity(42, "heartbeat-lease-expired")
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "heartbeat-owner"
	entity.Version = 2
	entity.HeartbeatAt = now
	leaseExpiresAt := now.Add(35 * time.Millisecond)
	entity.LeaseExpiresAt = &leaseExpiresAt
	repository.register(entity)

	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        5 * time.Millisecond,
		heartbeatTimeout:         2 * time.Millisecond,
		runningLeaseDuration:     35 * time.Millisecond,
	}
	runState, err := newEvaluationRunState(entity)
	require.NoError(t, err)
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	heartbeat := service.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("run context remained active after transient heartbeat failures exhausted the lease")
	}
	heartbeatErr := heartbeat.StopAndWait()
	assert.ErrorIs(t, heartbeatErr, errEvaluationTaskHeartbeatLeaseExpired)
	assert.ErrorIs(t, heartbeatErr, transientErr)
}

func TestEvaluationHeartbeatDoesNotExtendExpiredKnownLeaseAfterTransientFailure(t *testing.T) {
	transientErr := errors.New("heartbeat database unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErr = transientErr
	now := time.Now().UTC()
	entity := newPersistentLifecycleEntity(42, "heartbeat-known-lease-expired")
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "heartbeat-owner"
	entity.Version = 2
	entity.HeartbeatAt = now.Add(-time.Second)
	expiredLease := now.Add(-time.Millisecond)
	entity.LeaseExpiresAt = &expiredLease
	repository.register(entity)

	service := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        time.Hour,
		heartbeatTimeout:         10 * time.Millisecond,
		runningLeaseDuration:     time.Second,
	}
	runState, err := newEvaluationRunState(entity)
	require.NoError(t, err)
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	heartbeat := service.startEvaluationHeartbeat(runCtx, runState, cancelRun)
	t.Cleanup(func() { _ = heartbeat.StopAndWait() })
	select {
	case <-runCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("run context remained active after the persisted lease had already expired")
	}
	assert.ErrorIs(t, context.Cause(runCtx), errEvaluationTaskHeartbeatLeaseExpired)
	assert.ErrorIs(t, heartbeat.StopAndWait(), transientErr)
}

type evaluationHeartbeatCleanupKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
	entered chan<- struct{}
	release <-chan struct{}
}

func (s *evaluationHeartbeatCleanupKnowledgeBaseStub) DeleteKnowledgeBase(ctx context.Context, _ string) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRunEvaluationKeepsHeartbeatAliveThroughCleanupAndStopsBeforeTerminal(t *testing.T) {
	datasetErr := errors.New("dataset unavailable")
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(43, "heartbeat-cleanup")
	entity.OwnerID = "heartbeat-owner"
	repository.register(entity)

	cleanupEntered := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	service := &EvaluationService{
		config:  &config.Config{},
		dataset: &persistentLifecycleDatasetStub{err: datasetErr},
		knowledgeBaseService: &evaluationHeartbeatCleanupKnowledgeBaseStub{
			entered: cleanupEntered,
			release: cleanupRelease,
		},
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        5 * time.Millisecond,
		runningLeaseDuration:     50 * time.Millisecond,
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
		t.Fatal("evaluation did not enter temporary knowledge base cleanup")
	}
	require.Eventually(t, func() bool {
		return repository.countCalls("HeartbeatTask") >= 2
	}, time.Second, time.Millisecond)
	close(cleanupRelease)

	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, datasetErr)
	case <-time.After(time.Second):
		t.Fatal("evaluation did not finish after cleanup was released")
	}
	heartbeatCalls := repository.countCalls("HeartbeatTask")
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, heartbeatCalls, repository.countCalls("HeartbeatTask"))
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.EndTime)
	assert.False(t, stored.EndTime.Before(stored.HeartbeatAt))
}

func TestRunEvaluationSkipsCleanupAndTerminalAfterHeartbeatOwnershipLoss(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErr = interfaces.ErrEvaluationTaskOwnerConflict
	entity := newPersistentLifecycleEntity(45, "heartbeat-run-owner-lost")
	entity.OwnerID = "heartbeat-owner"
	repository.register(entity)

	datasetEntered := make(chan struct{}, 1)
	recorder := newEvaluationCleanupRecorder()
	service := &EvaluationService{
		config:                   &config.Config{},
		dataset:                  &persistentLifecycleDatasetStub{entered: datasetEntered},
		knowledgeBaseService:     &evaluationCleanupKnowledgeBaseStub{recorder: recorder},
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        time.Hour,
	}
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(context.Background(), detail, entity.TemporaryKnowledgeBaseID)
	require.ErrorIs(t, runErr, interfaces.ErrEvaluationTaskOwnerConflict)
	assert.Empty(t, recorder.snapshot())
	assert.Equal(t, 0, repository.countCalls("PublishTerminal"))
	select {
	case <-datasetEntered:
		t.Fatal("evaluation dataset started after heartbeat ownership loss")
	default:
	}

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueRunning, stored.Status)
	require.NotNil(t, stored.LeaseExpiresAt)
}

func TestRunEvaluationSkipsCleanupWhenHeartbeatLosesOwnershipBeforeCleanup(t *testing.T) {
	datasetErr := errors.New("dataset unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErrs = []error{nil, interfaces.ErrEvaluationTaskOwnerConflict}
	entity := newPersistentLifecycleEntity(46, "heartbeat-owner-lost-before-cleanup")
	entity.OwnerID = "heartbeat-owner"
	repository.register(entity)

	datasetEntered := make(chan struct{})
	datasetRelease := make(chan struct{})
	recorder := newEvaluationCleanupRecorder()
	service := &EvaluationService{
		config: &config.Config{},
		dataset: &evaluationCleanupDatasetStub{
			err:     datasetErr,
			entered: datasetEntered,
			release: datasetRelease,
		},
		knowledgeBaseService:     &evaluationCleanupKnowledgeBaseStub{recorder: recorder},
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
	select {
	case <-datasetEntered:
	case <-time.After(time.Second):
		t.Fatal("dataset load did not start")
	}
	require.Eventually(t, func() bool {
		return repository.countCalls("HeartbeatTask") >= 2
	}, time.Second, time.Millisecond)
	close(datasetRelease)

	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, interfaces.ErrEvaluationTaskOwnerConflict)
	case <-time.After(time.Second):
		t.Fatal("run did not stop after heartbeat ownership loss")
	}
	assert.Empty(t, recorder.snapshot())
	assert.Equal(t, 0, repository.countCalls("PublishTerminal"))
}

func TestRunEvaluationCancelsCleanupWhenHeartbeatLosesOwnershipDuringCleanup(t *testing.T) {
	datasetErr := errors.New("dataset unavailable")
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(47, "heartbeat-owner-lost-during-cleanup")
	entity.OwnerID = "heartbeat-owner"
	repository.register(entity)

	cleanupEntered := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	defer close(cleanupRelease)
	service := &EvaluationService{
		config:  &config.Config{},
		dataset: &persistentLifecycleDatasetStub{err: datasetErr},
		knowledgeBaseService: &evaluationHeartbeatCleanupKnowledgeBaseStub{
			entered: cleanupEntered,
			release: cleanupRelease,
		},
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
	select {
	case <-cleanupEntered:
	case <-time.After(time.Second):
		t.Fatal("knowledge base cleanup did not start")
	}
	repository.setHeartbeatErr(interfaces.ErrEvaluationTaskOwnerConflict)

	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, interfaces.ErrEvaluationTaskOwnerConflict)
	case <-time.After(time.Second):
		t.Fatal("in-flight cleanup was not canceled after ownership loss")
	}
	assert.Equal(t, 0, repository.countCalls("PublishTerminal"))
}

type evaluationDeadlineDatasetStub struct {
	interfaces.DatasetService
}

func (s *evaluationDeadlineDatasetStub) GetDatasetByID(ctx context.Context, _ string) ([]*types.QAPair, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRunEvaluationObservesAuthorityLossAfterTaskTimeoutCauseWins(t *testing.T) {
	transientErr := errors.New("heartbeat database unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.heartbeatErr = transientErr
	entity := newPersistentLifecycleEntity(48, "heartbeat-loss-after-timeout")
	entity.OwnerID = "heartbeat-owner"
	repository.register(entity)

	cleanupEntered := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	defer close(cleanupRelease)
	service := &EvaluationService{
		config: &config.Config{
			Evaluation: &config.EvaluationConfig{TaskTimeout: 10 * time.Millisecond},
		},
		dataset: &evaluationDeadlineDatasetStub{},
		knowledgeBaseService: &evaluationHeartbeatCleanupKnowledgeBaseStub{
			entered: cleanupEntered,
			release: cleanupRelease,
		},
		evaluationTaskRepository: repository,
		ownerID:                  entity.OwnerID,
		heartbeatInterval:        5 * time.Millisecond,
		heartbeatTimeout:         2 * time.Millisecond,
		runningLeaseDuration:     40 * time.Millisecond,
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
		t.Fatal("timeout path did not enter independent cleanup")
	}
	select {
	case runErr := <-runDone:
		assert.ErrorIs(t, runErr, context.DeadlineExceeded)
		assert.ErrorIs(t, runErr, errEvaluationTaskHeartbeatLeaseExpired)
	case <-time.After(time.Second):
		t.Fatal("authority loss did not cancel cleanup after task timeout")
	}
	assert.Equal(t, 0, repository.countCalls("PublishTerminal"))
}

func TestEvaluationTaskWriteAuthorityLossClassification(t *testing.T) {
	for _, authorityErr := range []error{
		interfaces.ErrEvaluationTaskNotFound,
		interfaces.ErrEvaluationTaskOwnerConflict,
		interfaces.ErrEvaluationTaskStateConflict,
		interfaces.ErrEvaluationTaskVersionConflict,
	} {
		assert.True(t, evaluationTaskWriteAuthorityLost(authorityErr), authorityErr)
	}
	assert.False(t, evaluationTaskWriteAuthorityLost(errors.New("database unavailable")))
}

func TestRunEvaluationSkipsCleanupAndTerminalAfterWriteAuthorityLoss(t *testing.T) {
	tests := []struct {
		name         string
		progressErr  error
		knowledgeErr error
	}{
		{
			name:        "progress owner conflict",
			progressErr: interfaces.ErrEvaluationTaskOwnerConflict,
		},
		{
			name:        "progress version conflict",
			progressErr: interfaces.ErrEvaluationTaskVersionConflict,
		},
		{
			name:         "knowledge state conflict",
			knowledgeErr: interfaces.ErrEvaluationTaskStateConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newFakeEvaluationTaskRepository()
			repository.progressErr = test.progressErr
			repository.knowledgeErr = test.knowledgeErr
			entity := newPersistentLifecycleEntity(44, "write-authority-loss-"+test.name)
			entity.OwnerID = "heartbeat-owner"
			repository.register(entity)

			recorder := newEvaluationCleanupRecorder()
			knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
			service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
			service.ownerID = entity.OwnerID
			service.heartbeatInterval = time.Hour
			detail, err := evaluationEntityToDetail(entity)
			require.NoError(t, err)

			runErr := service.runEvaluation(context.Background(), detail, entity.TemporaryKnowledgeBaseID)
			require.Error(t, runErr)
			if test.progressErr != nil {
				assert.ErrorIs(t, runErr, test.progressErr)
			} else {
				assert.ErrorIs(t, runErr, test.knowledgeErr)
			}
			assert.Empty(t, recorder.snapshot())
			assert.Equal(t, 0, repository.countCalls("PublishTerminal"))

			stored, err := repository.get(entity.TenantID, entity.ID)
			require.NoError(t, err)
			assert.Equal(t, types.EvaluationStatueRunning, stored.Status)
			require.NotNil(t, stored.LeaseExpiresAt)
		})
	}
}
