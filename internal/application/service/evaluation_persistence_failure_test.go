package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluationPersistenceKnowledgeStub struct {
	*evaluationCleanupKnowledgeStub

	mu               sync.Mutex
	createCalls      int
	knowledgeBaseIDs []string
}

func (s *evaluationPersistenceKnowledgeStub) CreateKnowledgeFromPassageSync(
	ctx context.Context,
	knowledgeBaseID string,
	passages []string,
	fileName string,
) (*types.Knowledge, error) {
	s.mu.Lock()
	s.createCalls++
	s.knowledgeBaseIDs = append(s.knowledgeBaseIDs, knowledgeBaseID)
	s.mu.Unlock()
	return s.evaluationCleanupKnowledgeStub.CreateKnowledgeFromPassageSync(
		ctx,
		knowledgeBaseID,
		passages,
		fileName,
	)
}

func (s *evaluationPersistenceKnowledgeStub) createdInKnowledgeBases() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.knowledgeBaseIDs...)
}

func TestRunEvaluationLoadFailureKeepsRecoverablePendingTaskAndResources(t *testing.T) {
	loadErr := errors.New("load task unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.getErr = loadErr
	entity := newPersistentLifecycleEntity(30, "load-before-start-failure")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		"caller-kb",
	)
	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, loadErr)
	assert.Empty(t, recorder.snapshot(), "Pending recovery resources must remain intact")

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatuePending, stored.Status)
	assert.Equal(t, entity.TemporaryKnowledgeBaseID, stored.TemporaryKnowledgeBaseID)
	assert.NotNil(t, stored.LeaseExpiresAt)
}

func TestRunEvaluationStartConflictKeepsRecoverablePendingTaskAndResources(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.startErr = interfaces.ErrEvaluationTaskVersionConflict
	entity := newPersistentLifecycleEntity(30, "start-conflict")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		"caller-kb",
	)
	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, interfaces.ErrEvaluationTaskVersionConflict)
	assert.Empty(t, recorder.snapshot(), "A failed ownership transition must not delete recovery resources")

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatuePending, stored.Status)
	assert.Equal(t, entity.TemporaryKnowledgeBaseID, stored.TemporaryKnowledgeBaseID)
	assert.NotNil(t, stored.LeaseExpiresAt)
}

func TestRunEvaluationUsesPersistedTemporaryKnowledgeBaseID(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(30, "persisted-knowledge-base")
	entity.TemporaryKnowledgeBaseID = "persisted-evaluation-kb"
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		"caller-kb",
	)
	require.NoError(t, runErr)
	assert.Equal(t, []string{"persisted-evaluation-kb"}, knowledge.createdInKnowledgeBases())
	assert.Equal(t, []string{
		"knowledge:evaluation-knowledge",
		"knowledge-base:persisted-evaluation-kb",
	}, recorder.snapshot())
}

func TestRunEvaluationMarksTimedOutWhenInitialTotalPersistenceHitsTaskDeadline(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	repository.progressErr = context.DeadlineExceeded
	entity := newPersistentLifecycleEntity(30, "initial-total-timeout")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	ctx, cancel := context.WithCancelCause(types.WithExecutionTenant(context.Background(), detail.Task.TenantID))
	cancel(errEvaluationTaskTimeout)
	runErr := service.runEvaluation(ctx, detail, entity.TemporaryKnowledgeBaseID)
	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, context.DeadlineExceeded)

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueTimedOut, stored.Status)
	assert.Equal(t, context.DeadlineExceeded.Error(), stored.ErrMsg)
}

func TestEvalDatasetRejectsEmptyPersistedTemporaryKnowledgeBaseID(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(30, "missing-persisted-knowledge-base")
	entity.TemporaryKnowledgeBaseID = ""
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.EvalDataset(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		"caller-kb",
	)
	require.Error(t, runErr)
	assert.ErrorContains(t, runErr, "persisted temporary knowledge base ID is required")
	assert.Equal(t, 0, knowledge.createCallCount())
}

func (s *evaluationPersistenceKnowledgeStub) createCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCalls
}

func TestRunEvaluationPublishesFailedWhenInitialTotalPersistenceFails(t *testing.T) {
	publishErr := errors.New("publish total unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.progressErr = publishErr
	entity := newPersistentLifecycleEntity(31, "total-persistence-failure")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		entity.TemporaryKnowledgeBaseID,
	)
	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, publishErr)
	assert.Equal(t, 0, knowledge.createCallCount(), "Knowledge must not be created before total is durable")
	assert.Equal(t, []string{
		"knowledge-base:" + entity.TemporaryKnowledgeBaseID,
	}, recorder.snapshot())
	assert.Equal(t, 1, repository.countCalls("PublishProgress"))
	assert.Equal(t, 0, repository.countCalls("RecordTemporaryKnowledge"))
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueFailed, stored.Status)
	assert.Contains(t, stored.ErrMsg, "publish evaluation total")
	assert.NotNil(t, stored.EndTime)
	assert.Nil(t, stored.LeaseExpiresAt)
}

func TestRunEvaluationCleansKnowledgeBeforeBaseWhenKnowledgePersistenceFails(t *testing.T) {
	recordErr := errors.New("record temporary knowledge unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.knowledgeErr = recordErr
	entity := newPersistentLifecycleEntity(32, "knowledge-persistence-failure")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		entity.TemporaryKnowledgeBaseID,
	)
	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, recordErr)
	assert.Equal(t, 1, knowledge.createCallCount())
	assert.Equal(t, []string{
		"knowledge:evaluation-knowledge",
		"knowledge-base:" + entity.TemporaryKnowledgeBaseID,
	}, recorder.snapshot())
	assert.Equal(t, 1, repository.countCalls("PublishProgress"))
	assert.Equal(t, 1, repository.countCalls("RecordTemporaryKnowledge"))
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueFailed, stored.Status)
	assert.Empty(t, stored.TemporaryKnowledgeID)
	assert.Contains(t, stored.ErrMsg, "record temporary evaluation knowledge")
	assert.NotNil(t, stored.EndTime)
	assert.Nil(t, stored.LeaseExpiresAt)
}

func TestRunEvaluationJoinsWorkerAndTerminalPublicationErrors(t *testing.T) {
	workerErr := errors.New("worker business failure")
	publicationErr := errors.New("terminal publication unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.terminalErr = publicationErr
	entity := newPersistentLifecycleEntity(33, "terminal-persistence-failure")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, workerErr)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		entity.TemporaryKnowledgeBaseID,
	)
	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, workerErr)
	assert.ErrorIs(t, runErr, publicationErr)
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))
	assert.Equal(t, []string{
		"knowledge:evaluation-knowledge",
		"knowledge-base:" + entity.TemporaryKnowledgeBaseID,
	}, recorder.snapshot())

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueRunning, stored.Status)
	assert.Equal(t, "evaluation-knowledge", stored.TemporaryKnowledgeID)
	assert.Nil(t, stored.EndTime)
	assert.NotNil(t, stored.LeaseExpiresAt)
}

func TestRunEvaluationSuccessRetainsRecoveryStateAndClearsLease(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(34, "persistent-success")
	repository.register(entity)

	recorder := newEvaluationCleanupRecorder()
	knowledge := newEvaluationPersistenceKnowledgeStub(recorder)
	service := newEvaluationPersistenceFailureService(repository, recorder, knowledge, nil)
	detail, err := evaluationEntityToDetail(entity)
	require.NoError(t, err)

	runErr := service.runEvaluation(
		types.WithExecutionTenant(context.Background(), detail.Task.TenantID),
		detail,
		entity.TemporaryKnowledgeBaseID,
	)
	require.NoError(t, runErr)
	assert.Equal(t, []string{
		"knowledge:evaluation-knowledge",
		"knowledge-base:" + entity.TemporaryKnowledgeBaseID,
	}, recorder.snapshot())

	stored, err := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueSuccess, stored.Status)
	assert.Equal(t, "evaluation-knowledge", stored.TemporaryKnowledgeID)
	assert.Equal(t, 1, stored.Total)
	assert.Equal(t, 1, stored.Finished)
	assert.NotEmpty(t, stored.Metric)
	metric, err := decodeEvaluationMetric(stored.Metric)
	require.NoError(t, err)
	assert.NotNil(t, metric)
	assert.NotNil(t, stored.EndTime)
	assert.Nil(t, stored.LeaseExpiresAt)
	assert.Greater(t, stored.Version, entity.Version)
	assert.Equal(t, 3, repository.countCalls("PublishProgress"))
	assert.Equal(t, 1, repository.countCalls("RecordTemporaryKnowledge"))
	assert.Equal(t, 1, repository.countCalls("PublishTerminal"))
}

func newEvaluationPersistenceFailureService(
	repository interfaces.EvaluationTaskRepository,
	recorder *evaluationCleanupRecorder,
	knowledge *evaluationPersistenceKnowledgeStub,
	workerErr error,
) *EvaluationService {
	service := newPersistentLifecycleService(
		repository,
		&persistentLifecycleDatasetStub{dataset: []*types.QAPair{{
			QID:      0,
			Question: "question",
			PIDs:     []int{0},
			Passages: []string{"passage"},
			Answer:   "answer",
		}}},
		nil,
		nil,
	)
	service.knowledgeBaseService = &evaluationCleanupKnowledgeBaseStub{recorder: recorder}
	service.knowledgeService = knowledge
	service.sessionService = &evaluationCleanupSessionStub{err: workerErr}
	return service
}

func newEvaluationPersistenceKnowledgeStub(
	recorder *evaluationCleanupRecorder,
) *evaluationPersistenceKnowledgeStub {
	return &evaluationPersistenceKnowledgeStub{
		evaluationCleanupKnowledgeStub: &evaluationCleanupKnowledgeStub{recorder: recorder},
	}
}
