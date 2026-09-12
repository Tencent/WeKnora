package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluationRecoveryResourceCall struct {
	resource string
	id       string
	tenantID uint64
	tenant   *types.Tenant
}

type evaluationRecoveryResourceRecorder struct {
	mu    sync.Mutex
	calls []evaluationRecoveryResourceCall
}

func (r *evaluationRecoveryResourceRecorder) record(ctx context.Context, resource, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	r.calls = append(r.calls, evaluationRecoveryResourceCall{
		resource: resource,
		id:       id,
		tenantID: tenantID,
		tenant:   tenant,
	})
}

func (r *evaluationRecoveryResourceRecorder) snapshot() []evaluationRecoveryResourceCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evaluationRecoveryResourceCall(nil), r.calls...)
}

type evaluationRecoveryKnowledgeStub struct {
	interfaces.KnowledgeService
	recorder *evaluationRecoveryResourceRecorder
	err      error
}

func (s *evaluationRecoveryKnowledgeStub) DeleteKnowledge(ctx context.Context, id string) error {
	s.recorder.record(ctx, "knowledge", id)
	return s.err
}

type evaluationRecoveryKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
	recorder *evaluationRecoveryResourceRecorder
	err      error
}

type evaluationRecoveryTenantStub struct {
	interfaces.TenantService
	tenant *types.Tenant
	err    error
}

var errEvaluationRecoveryValidationWithoutDeadline = errors.New(
	"evaluation recovery ownership validation has no deadline",
)

type evaluationRecoveryDeadlineRepository struct {
	interfaces.EvaluationTaskRepository
	validationCalls int
}

func (r *evaluationRecoveryDeadlineRepository) GetTask(
	ctx context.Context,
	tenantID uint64,
	taskID string,
) (*types.EvaluationTaskEntity, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errEvaluationRecoveryValidationWithoutDeadline
	}
	r.validationCalls++
	return r.EvaluationTaskRepository.GetTask(ctx, tenantID, taskID)
}

func (s *evaluationRecoveryTenantStub) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.tenant != nil {
		return s.tenant, nil
	}
	return &types.Tenant{ID: id, Name: "recovery tenant"}, nil
}

func (s *evaluationRecoveryKnowledgeBaseStub) DeleteKnowledgeBase(ctx context.Context, id string) error {
	s.recorder.record(ctx, "knowledge-base", id)
	return s.err
}

func newExpiredRecoveryTask(tenantID uint64, taskID string, now time.Time) *types.EvaluationTaskEntity {
	entity := newPersistentLifecycleEntity(tenantID, taskID)
	entity.Status = types.EvaluationStatueRunning
	entity.OwnerID = "dead-owner"
	entity.Version = 4
	entity.TemporaryKnowledgeID = "temporary-knowledge"
	entity.Metric = types.JSON(`{"retrieval_metrics":{"precision":0.5}}`)
	entity.HeartbeatAt = now.Add(-2 * time.Minute)
	expiredLease := now.Add(-time.Minute)
	entity.LeaseExpiresAt = &expiredLease
	entity.UpdatedAt = entity.HeartbeatAt
	return entity
}

func newTestEvaluationTaskRecoveryRunner(
	repository interfaces.EvaluationTaskRepository,
	recorder *evaluationRecoveryResourceRecorder,
	knowledgeErr error,
	knowledgeBaseErr error,
	now time.Time,
) *EvaluationTaskRecoveryRunner {
	runner := NewEvaluationTaskRecoveryRunner(
		repository,
		&evaluationRecoveryTenantStub{},
		&evaluationRecoveryKnowledgeBaseStub{recorder: recorder, err: knowledgeBaseErr},
		&evaluationRecoveryKnowledgeStub{recorder: recorder, err: knowledgeErr},
	)
	runner.ownerID = "recovery-owner"
	runner.now = func() time.Time { return now }
	runner.recoveryLeaseDuration = 2 * time.Minute
	return runner
}

func TestEvaluationTaskRecoveryRunnerCleansExpiredTaskAndPublishesInterrupted(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(51, "expired-running", now)
	repository.register(entity)
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(repository, recorder, nil, nil, now)

	require.NoError(t, runner.runOnce(context.Background()))
	assert.Equal(t, []evaluationRecoveryResourceCall{
		{
			resource: "knowledge",
			id:       entity.TemporaryKnowledgeID,
			tenantID: entity.TenantID,
			tenant:   &types.Tenant{ID: entity.TenantID, Name: "recovery tenant"},
		},
		{
			resource: "knowledge-base",
			id:       entity.TemporaryKnowledgeBaseID,
			tenantID: entity.TenantID,
			tenant:   &types.Tenant{ID: entity.TenantID, Name: "recovery tenant"},
		},
	}, recorder.snapshot())
	assert.Equal(t, 1, repository.countCalls("ClaimExpiredTasks"))
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueInterrupted, stored.Status)
	assert.Equal(t, evaluationTaskInterruptedMessage, stored.ErrMsg)
	assert.Nil(t, stored.LeaseExpiresAt)
	assert.Equal(t, entity.Metric, stored.Metric)
	require.NotNil(t, stored.EndTime)
}

func TestEvaluationTaskRecoveryRunnerTreatsMissingResourcesAsAlreadyClean(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(52, "already-clean", now)
	repository.register(entity)
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(
		repository,
		recorder,
		apprepo.ErrKnowledgeNotFound,
		apprepo.ErrKnowledgeBaseNotFound,
		now,
	)

	require.NoError(t, runner.runOnce(context.Background()))
	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueInterrupted, stored.Status)
	cleanupErrors, err := decodeEvaluationCleanupErrors(stored.CleanupErrors)
	require.NoError(t, err)
	assert.Empty(t, cleanupErrors)
}

func TestEvaluationTaskRecoveryRunnerPreservesCleanupWarnings(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	knowledgeErr := errors.New("knowledge delete unavailable")
	knowledgeBaseErr := errors.New("knowledge base delete unavailable")
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(53, "cleanup-warnings", now)
	entity.CleanupErrors = types.JSON(`["previous warning"]`)
	repository.register(entity)
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(
		repository,
		recorder,
		knowledgeErr,
		knowledgeBaseErr,
		now,
	)

	require.NoError(t, runner.runOnce(context.Background()))
	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueInterrupted, stored.Status)
	cleanupErrors, err := decodeEvaluationCleanupErrors(stored.CleanupErrors)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"previous warning",
		"delete knowledge temporary-knowledge: knowledge delete unavailable",
		"delete knowledge base temporary-evaluation-kb: knowledge base delete unavailable",
	}, cleanupErrors)
}

func TestEvaluationTaskRecoveryRunnerKeepsLeaseWhenTerminalPublicationFails(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	publicationErr := errors.New("terminal unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.terminalErr = publicationErr
	entity := newExpiredRecoveryTask(54, "retry-recovery", now)
	repository.register(entity)
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(repository, recorder, nil, nil, now)

	runErr := runner.runOnce(context.Background())
	require.ErrorIs(t, runErr, publicationErr)
	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueRunning, stored.Status)
	assert.Equal(t, "recovery-owner", stored.OwnerID)
	require.NotNil(t, stored.LeaseExpiresAt)
	assert.Equal(t, now.Add(2*time.Minute), *stored.LeaseExpiresAt)
}

func TestEvaluationTaskRecoveryRunnerStartsWithImmediateScanAndStopsIdempotently(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.claimEntered = make(chan struct{}, 1)
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := NewEvaluationTaskRecoveryRunner(
		repository,
		&evaluationRecoveryTenantStub{},
		&evaluationRecoveryKnowledgeBaseStub{recorder: recorder},
		&evaluationRecoveryKnowledgeStub{recorder: recorder},
	)
	runner.recoveryInterval = time.Hour

	runner.Start(context.Background())
	select {
	case <-repository.claimEntered:
	case <-time.After(time.Second):
		t.Fatal("recovery runner did not scan immediately after Start")
	}
	runner.Stop()
	runner.Stop()
	assert.Equal(t, 1, repository.countCalls("ClaimExpiredTasks"))
}

type evaluationRecoveryBlockingKnowledgeStub struct {
	interfaces.KnowledgeService
	entered chan<- struct{}
}

func (s *evaluationRecoveryBlockingKnowledgeStub) DeleteKnowledge(ctx context.Context, _ string) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestEvaluationTaskRecoveryRunnerStopCancelsBlockingCleanup(t *testing.T) {
	now := time.Now().UTC()
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(55, "stop-blocking-cleanup", now)
	repository.register(entity)
	cleanupEntered := make(chan struct{}, 1)
	runner := NewEvaluationTaskRecoveryRunner(
		repository,
		&evaluationRecoveryTenantStub{},
		&evaluationRecoveryKnowledgeBaseStub{recorder: &evaluationRecoveryResourceRecorder{}},
		&evaluationRecoveryBlockingKnowledgeStub{entered: cleanupEntered},
	)
	runner.now = func() time.Time { return now }
	runner.recoveryInterval = time.Hour

	runner.Start(context.Background())
	select {
	case <-cleanupEntered:
	case <-time.After(time.Second):
		t.Fatal("recovery cleanup did not start")
	}
	stopped := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop did not cancel blocking recovery cleanup")
	}

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueRunning, stored.Status)
	require.NotNil(t, stored.LeaseExpiresAt)
	assert.Equal(t, 0, repository.countCalls("PublishTerminal"))
}

func TestEvaluationTaskRecoveryRunnerLoadsTenantBeforeCleanup(t *testing.T) {
	now := time.Now().UTC()
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(56, "tenant-context", now)
	repository.register(entity)
	tenant := &types.Tenant{ID: entity.TenantID, Name: "tenant context"}
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := NewEvaluationTaskRecoveryRunner(
		repository,
		&evaluationRecoveryTenantStub{tenant: tenant},
		&evaluationRecoveryKnowledgeBaseStub{recorder: recorder},
		&evaluationRecoveryKnowledgeStub{recorder: recorder},
	)
	runner.ownerID = "recovery-owner"
	runner.now = func() time.Time { return now }

	require.NoError(t, runner.runOnce(context.Background()))
	for _, call := range recorder.snapshot() {
		assert.Equal(t, entity.TenantID, call.tenantID)
		assert.Same(t, tenant, call.tenant)
	}
}

func TestEvaluationTaskRecoveryRunnerSkipsCleanupAndTerminalWhenTaskIsStolen(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	repository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(57, "stolen-task", now)
	repository.register(entity)

	claimed, err := repository.ClaimExpiredTasks(context.Background(), types.EvaluationTaskClaimExpiredCommand{
		OwnerID:        "recovery-owner",
		Now:            now,
		LeaseExpiresAt: now.Add(2 * time.Minute),
		Limit:          8,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	stolen, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	stolen.OwnerID = "another-recovery-owner"
	stolen.Version++
	repository.register(stolen)

	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(repository, recorder, nil, nil, now)
	runErr := runner.recoverTask(context.Background(), claimed[0])
	require.ErrorIs(t, runErr, interfaces.ErrEvaluationTaskOwnerConflict)
	assert.Empty(t, recorder.snapshot())
	assert.Equal(t, 0, repository.countCalls("PublishTerminal"))
}

func TestEvaluationRecoveryOperationBudgetsFitInsideRecoveryLease(t *testing.T) {
	total := evaluationRecoveryTenantTimeout + 3*evaluationRecoveryOperationTimeout
	margin := evaluationDefaultRunningLeaseDuration - total
	assert.GreaterOrEqual(
		t,
		margin,
		evaluationRecoveryLeaseSafetyMargin,
		"tenant, cleanup, and terminal budgets must keep a safety margin inside the recovery lease",
	)
}

func TestEvaluationTaskRecoveryBoundsOwnershipValidation(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	baseRepository := newFakeEvaluationTaskRepository()
	entity := newExpiredRecoveryTask(57, "bounded-recovery-validation", now)
	baseRepository.register(entity)
	repository := &evaluationRecoveryDeadlineRepository{
		EvaluationTaskRepository: baseRepository,
	}
	recorder := &evaluationRecoveryResourceRecorder{}
	runner := newTestEvaluationTaskRecoveryRunner(repository, recorder, nil, nil, now)

	require.NoError(t, runner.runOnce(context.Background()))
	assert.Equal(t, 3, repository.validationCalls)
}
