package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationResultReadsSharedPersistentTaskForContextTenant(t *testing.T) {
	const tenantID uint64 = 17
	repository := newFakeEvaluationTaskRepository()
	repository.register(newPersistentLifecycleEntity(tenantID, "persisted-task"))

	// These services model two processes. Neither owns an in-memory copy of the task.
	_ = &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  "first-instance",
	}
	second := &EvaluationService{
		evaluationTaskRepository: repository,
		ownerID:                  "second-instance",
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)

	detail, err := second.EvaluationResult(ctx, "persisted-task")
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "persisted-task", detail.Task.ID)
	assert.Equal(t, tenantID, detail.Task.TenantID)
	assert.Equal(t, "persisted-chat-model", detail.Params.ChatModelID)

	assert.Equal(t, []evaluationTaskRepositoryCall{{
		Method:   "GetTask",
		TenantID: tenantID,
		TaskID:   "persisted-task",
	}}, repository.callsSnapshot())
}

func TestEvaluationResultRejectsCorruptPersistedParams(t *testing.T) {
	const tenantID uint64 = 18
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(tenantID, "corrupt-task")
	entity.Params = types.JSON(`{"chat_model_id":`)
	repository.register(entity)
	service := &EvaluationService{
		evaluationTaskRepository: repository,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)

	detail, err := service.EvaluationResult(ctx, entity.ID)
	require.Error(t, err)
	assert.Nil(t, detail)
	assert.ErrorContains(t, err, "decode evaluation params")
	assert.Equal(t, []evaluationTaskRepositoryCall{{
		Method:   "GetTask",
		TenantID: tenantID,
		TaskID:   entity.ID,
	}}, repository.callsSnapshot())
}

func TestEvaluationCreateFailureCleansKnowledgeBaseWithoutStartingWorker(t *testing.T) {
	createErr := errors.New("persistent task unavailable")
	repository := newFakeEvaluationTaskRepository()
	repository.createErr = createErr
	workerEntered := make(chan struct{}, 1)
	knowledgeBaseDeleted := make(chan string, 1)
	service := newPersistentLifecycleService(
		repository,
		&persistentLifecycleDatasetStub{
			entered: workerEntered,
			err:     errors.New("worker must not start"),
		},
		knowledgeBaseDeleted,
		nil,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(19))

	detail, err := service.Evaluation(ctx, "dataset", "source-kb", "chat-model", "rerank-model")
	assert.ErrorIs(t, err, createErr)
	assert.Nil(t, detail)
	assert.Equal(t, 1, repository.countCalls("CreateTask"))
	assert.Equal(t, 0, repository.countCalls("TryStartTask"))

	select {
	case deletedID := <-knowledgeBaseDeleted:
		assert.Equal(t, "temporary-evaluation-kb", deletedID)
	case <-time.After(2 * time.Second):
		t.Error("temporary knowledge base was not cleaned after CreateTask failure")
	}
	select {
	case <-workerEntered:
		t.Error("evaluation worker entered after CreateTask failed")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEvaluationReturnsIsolatedPendingPersistentSnapshot(t *testing.T) {
	const tenantID uint64 = 20
	repository := newFakeEvaluationTaskRepository()
	startEntered := make(chan struct{}, 1)
	startRelease := make(chan struct{})
	repository.startEntered = startEntered
	repository.startRelease = startRelease
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(startRelease) })
	})

	workerEntered := make(chan struct{}, 1)
	observedChatModel := make(chan string, 1)
	service := newPersistentLifecycleService(
		repository,
		&persistentLifecycleDatasetStub{
			entered: workerEntered,
			dataset: []*types.QAPair{{
				QID:      0,
				Question: "question",
				PIDs:     []int{0},
				Passages: []string{"passage"},
				Answer:   "answer",
			}},
		},
		make(chan string, 1),
		observedChatModel,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)

	created, err := service.Evaluation(ctx, "dataset", "source-kb", "chat-model", "rerank-model")
	require.NoError(t, err)
	require.NotNil(t, created)
	require.NotNil(t, created.Task)
	assert.Equal(t, types.EvaluationStatuePending, created.Task.Status)
	taskID := created.Task.ID

	select {
	case <-startEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("background task did not attempt the persisted pending-to-running transition")
	}
	stored, err := repository.get(tenantID, taskID)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatuePending, stored.Status)
	assert.Equal(t, created.Task.StartTime.UTC(), stored.StartTime)
	storedDetail, err := evaluationEntityToDetail(stored)
	require.NoError(t, err)
	assert.Equal(t, "chat-model", storedDetail.Params.ChatModelID)

	created.Task.ID = "caller-mutated-task"
	created.Task.DatasetID = "caller-mutated-dataset"
	created.Params.ChatModelID = "caller-mutated-model"
	storedAfterMutation, err := repository.get(tenantID, taskID)
	require.NoError(t, err)
	storedDetailAfterMutation, err := evaluationEntityToDetail(storedAfterMutation)
	require.NoError(t, err)
	assert.Equal(t, taskID, storedAfterMutation.ID)
	assert.Equal(t, "dataset", storedAfterMutation.DatasetID)
	assert.Equal(t, "chat-model", storedDetailAfterMutation.Params.ChatModelID)

	releaseOnce.Do(func() { close(startRelease) })
	select {
	case <-workerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("background worker did not start after the persistent transition completed")
	}
	select {
	case modelID := <-observedChatModel:
		assert.Equal(t, "chat-model", modelID)
	case <-time.After(5 * time.Second):
		t.Fatal("background worker did not preserve the persisted chat model")
	}
}

func newPersistentLifecycleEntity(tenantID uint64, taskID string) *types.EvaluationTaskEntity {
	startedAt := time.Date(2026, time.August, 28, 9, 0, 0, 0, time.UTC)
	leaseExpiresAt := startedAt.Add(2 * time.Hour)
	return &types.EvaluationTaskEntity{
		ID:            taskID,
		TenantID:      tenantID,
		DatasetID:     "dataset",
		Status:        types.EvaluationStatuePending,
		StartTime:     startedAt,
		CleanupErrors: types.JSON(`[]`),
		Params: types.JSON(
			`{"chat_model_id":"persisted-chat-model","rerank_model_id":"persisted-rerank-model"}`,
		),
		TemporaryKnowledgeBaseID: "temporary-evaluation-kb",
		OwnerID:                  "persistent-owner",
		LeaseExpiresAt:           &leaseExpiresAt,
		HeartbeatAt:              startedAt,
		Version:                  1,
		CreatedAt:                startedAt,
		UpdatedAt:                startedAt,
	}
}

type persistentLifecycleKnowledgeBaseStub struct {
	interfaces.KnowledgeBaseService
	deleted chan<- string
}

func (s *persistentLifecycleKnowledgeBaseStub) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               "source-kb",
		EmbeddingModelID: "embedding-model",
		SummaryModelID:   "summary-model",
	}, nil
}

func (s *persistentLifecycleKnowledgeBaseStub) CreateKnowledgeBase(
	_ context.Context,
	knowledgeBase *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	created := *knowledgeBase
	created.ID = "temporary-evaluation-kb"
	return &created, nil
}

func (s *persistentLifecycleKnowledgeBaseStub) DeleteKnowledgeBase(_ context.Context, id string) error {
	if s.deleted != nil {
		s.deleted <- id
	}
	return nil
}

type persistentLifecycleDatasetStub struct {
	interfaces.DatasetService
	dataset []*types.QAPair
	err     error
	entered chan<- struct{}
}

func (s *persistentLifecycleDatasetStub) GetDatasetByID(
	context.Context,
	string,
) ([]*types.QAPair, error) {
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	return s.dataset, s.err
}

type persistentLifecycleKnowledgeStub struct {
	interfaces.KnowledgeService
}

func (s *persistentLifecycleKnowledgeStub) CreateKnowledgeFromPassageSync(
	context.Context,
	string,
	[]string,
	string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "temporary-knowledge"}, nil
}

func (s *persistentLifecycleKnowledgeStub) DeleteKnowledge(context.Context, string) error {
	return nil
}

type persistentLifecycleSessionStub struct {
	interfaces.SessionService
	observedChatModel chan<- string
}

func (s *persistentLifecycleSessionStub) KnowledgeQAByEvent(
	_ context.Context,
	chatManage *types.ChatManage,
	_ []types.EventType,
) error {
	if s.observedChatModel != nil {
		s.observedChatModel <- chatManage.ChatModelID
	}
	return nil
}

func newPersistentLifecycleService(
	repository interfaces.EvaluationTaskRepository,
	dataset interfaces.DatasetService,
	knowledgeBaseDeleted chan<- string,
	observedChatModel chan<- string,
) *EvaluationService {
	return &EvaluationService{
		config: &config.Config{
			Conversation: &config.ConversationConfig{
				Summary: &config.SummaryConfig{},
			},
		},
		dataset:                  dataset,
		knowledgeBaseService:     &persistentLifecycleKnowledgeBaseStub{deleted: knowledgeBaseDeleted},
		knowledgeService:         &persistentLifecycleKnowledgeStub{},
		sessionService:           &persistentLifecycleSessionStub{observedChatModel: observedChatModel},
		evaluationTaskRepository: repository,
		ownerID:                  "persistent-owner",
	}
}
