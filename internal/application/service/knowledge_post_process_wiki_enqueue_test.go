package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiEnqueueFailureKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge        *types.Knowledge
	expectedSubtasks int
}

func (r *wikiEnqueueFailureKnowledgeRepo) GetKnowledgeByIDOnly(
	context.Context,
	string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *wikiEnqueueFailureKnowledgeRepo) SetFinalizing(
	_ context.Context,
	_ string,
	expectedSubtasks int,
) (bool, error) {
	r.expectedSubtasks = expectedSubtasks
	r.knowledge.ParseStatus = types.ParseStatusFinalizing
	return true, nil
}

func (r *wikiEnqueueFailureKnowledgeRepo) UpdateKnowledgeColumn(
	context.Context,
	string,
	string,
	interface{},
) error {
	return nil
}

func (r *wikiEnqueueFailureKnowledgeRepo) FinalizeSubtask(context.Context, string) (int, bool, error) {
	return 0, true, nil
}

type wikiEnqueueFailureKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *wikiEnqueueFailureKBService) GetKnowledgeBaseByIDOnly(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type wikiEnqueueFailureChunkService struct {
	interfaces.ChunkService
	chunks []*types.Chunk
}

func (s *wikiEnqueueFailureChunkService) ListChunksByKnowledgeID(
	context.Context,
	string,
) ([]*types.Chunk, error) {
	return s.chunks, nil
}

type wikiEnqueueFailureTaskQueue struct {
	interfaces.TaskEnqueuer
	taskTypes []string
	wikiErr   error
}

func (q *wikiEnqueueFailureTaskQueue) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	q.taskTypes = append(q.taskTypes, task.Type())
	if task.Type() == types.TypeWikiIngest {
		return nil, q.wikiErr
	}
	return &asynq.TaskInfo{ID: "queued", Type: task.Type()}, nil
}

type wikiEnqueueFailurePendingRepo struct {
	interfaces.TaskPendingOpsRepository
	seedErr       error
	seedCalls     int
	seededOp      *types.TaskPendingOp
	enqueueCalls  int
	enqueuedOp    *types.TaskPendingOp
	knowledgeRepo *wikiEnqueueFailureKnowledgeRepo
}

func (r *wikiEnqueueFailurePendingRepo) SeedKnowledgeFinalizingWithPendingOp(
	_ context.Context,
	_ string,
	expectedSubtasks int,
	op *types.TaskPendingOp,
) (bool, error) {
	r.seedCalls++
	if r.seedErr != nil {
		return false, r.seedErr
	}
	r.seededOp = op
	r.knowledgeRepo.expectedSubtasks = expectedSubtasks
	r.knowledgeRepo.knowledge.ParseStatus = types.ParseStatusFinalizing
	return true, nil
}

func (r *wikiEnqueueFailurePendingRepo) Enqueue(_ context.Context, op *types.TaskPendingOp) error {
	r.enqueueCalls++
	r.enqueuedOp = op
	return nil
}

type wikiGenerationPostProcessRepo struct {
	interfaces.KnowledgeGenerationRepository
	generation *types.KnowledgeGeneration
}

func (r wikiGenerationPostProcessRepo) Get(context.Context, uint64, string) (*types.KnowledgeGeneration, error) {
	return r.generation, nil
}

func (r wikiGenerationPostProcessRepo) LatestAttempt(context.Context, uint64, string) (int, error) {
	return r.generation.Attempt, nil
}

func (r wikiGenerationPostProcessRepo) ActivateIfCurrent(context.Context, string, int) (bool, error) {
	return true, nil
}

func (r wikiGenerationPostProcessRepo) MarkRetired(context.Context, string) error {
	return nil
}

type wikiGenerationChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r wikiGenerationChunkRepo) ListGenerationChunks(context.Context, uint64, string, string) ([]*types.Chunk, error) {
	return r.chunks, nil
}

func newWikiEnqueueTestService(
	knowledgeID string,
	pendingRepo *wikiEnqueueFailurePendingRepo,
	queue *wikiEnqueueFailureTaskQueue,
) (*KnowledgePostProcessService, *wikiEnqueueFailureKnowledgeRepo) {
	repo := &wikiEnqueueFailureKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID:          knowledgeID,
			ParseStatus: types.ParseStatusProcessing,
		},
	}
	pendingRepo.knowledgeRepo = repo
	return &KnowledgePostProcessService{
		knowledgeRepo: repo,
		kbService: &wikiEnqueueFailureKBService{kb: &types.KnowledgeBase{
			ID: "kb-wiki",
			IndexingStrategy: types.IndexingStrategy{
				WikiEnabled: true,
			},
		}},
		chunkService: &wikiEnqueueFailureChunkService{chunks: []*types.Chunk{
			{ID: "chunk-1", ChunkType: types.ChunkTypeText},
		}},
		taskEnqueuer: queue,
		pendingRepo:  pendingRepo,
	}, repo
}

func newWikiEnqueuePostProcessTask(t *testing.T, knowledgeID string) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID:        7,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: "kb-wiki",
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKnowledgePostProcess, payload)
}

func TestKnowledgePostProcessDefersGenerationWikiTriggerUntilActivation(t *testing.T) {
	const knowledgeID = "knowledge-generation-wiki"
	const generationID = "generation-wiki"
	pendingRepo := &wikiEnqueueFailurePendingRepo{}
	queue := &wikiEnqueueFailureTaskQueue{}
	service, repo := newWikiEnqueueTestService(knowledgeID, pendingRepo, queue)
	service.generationRepo = wikiGenerationPostProcessRepo{generation: &types.KnowledgeGeneration{
		ID:          generationID,
		TenantID:    7,
		KnowledgeID: knowledgeID,
		Attempt:     2,
		State:       types.KnowledgeGenerationStateBuilding,
	}}
	service.chunkRepo = wikiGenerationChunkRepo{chunks: []*types.Chunk{
		{ID: "chunk-generation", ChunkType: types.ChunkTypeText, GenerationID: generationID},
	}}
	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID:        7,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: "kb-wiki",
		Attempt:         2,
		GenerationID:    generationID,
	})
	require.NoError(t, err)

	err = service.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, payload))

	require.NoError(t, err)
	assert.Equal(t, types.ParseStatusFinalizing, repo.knowledge.ParseStatus)
	assert.Equal(t, 1, repo.expectedSubtasks, "only summary owns a finalizing slot before generation activation")
	assert.Equal(t, []string{types.TypeSummaryGeneration}, queue.taskTypes)
	require.Equal(t, 1, pendingRepo.enqueueCalls)
	require.NotNil(t, pendingRepo.enqueuedOp)
	assert.Equal(t, knowledgeID, pendingRepo.enqueuedOp.DedupKey)

	var op WikiPendingOp
	require.NoError(t, json.Unmarshal(pendingRepo.enqueuedOp.Payload, &op))
	assert.Equal(t, knowledgeID, op.KnowledgeID)
	assert.Equal(t, generationID, op.GenerationID)
	assert.Equal(t, 2, op.Attempt)
}

func TestKnowledgePostProcessDoesNotTriggerWikiAfterGenerationActivationWhenWikiDisabled(t *testing.T) {
	const knowledgeID = "knowledge-generation-no-wiki"
	const generationID = "generation-no-wiki"
	t.Setenv("NEO4J_ENABLE", "false")
	pendingRepo := &wikiEnqueueFailurePendingRepo{}
	queue := &wikiEnqueueFailureTaskQueue{}
	service, _ := newWikiEnqueueTestService(knowledgeID, pendingRepo, queue)
	service.kbService = &wikiEnqueueFailureKBService{kb: &types.KnowledgeBase{
		ID:               "kb-no-wiki",
		SummaryModelID:   "chat-1",
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true},
	}}
	service.generationRepo = wikiGenerationPostProcessRepo{generation: &types.KnowledgeGeneration{
		ID:          generationID,
		TenantID:    7,
		KnowledgeID: knowledgeID,
		Attempt:     2,
		State:       types.KnowledgeGenerationStateReady,
	}}
	service.chunkRepo = wikiGenerationChunkRepo{chunks: []*types.Chunk{
		{ID: "chunk-generation", ChunkType: types.ChunkTypeText, GenerationID: generationID},
	}}
	payload, err := json.Marshal(types.KnowledgePostProcessPayload{
		TenantID:        7,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: "kb-no-wiki",
		Attempt:         2,
		GenerationID:    generationID,
	})
	require.NoError(t, err)

	err = service.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, payload))

	require.NoError(t, err)
	assert.Equal(t, []string{types.TypeSummaryGeneration}, queue.taskTypes)
	assert.Zero(t, pendingRepo.enqueueCalls)
}

func TestKnowledgePostProcessAtomicallySeedsWikiSlot(t *testing.T) {
	const knowledgeID = "knowledge-wiki-enqueue-failure"
	tests := []struct {
		name       string
		pendingErr error
		wantTasks  []string
		wantStatus string
		wantErr    bool
	}{
		{
			name:       "pending op persistence fails",
			pendingErr: errors.New("postgres unavailable"),
			wantStatus: types.ParseStatusProcessing,
			wantErr:    true,
		},
		{
			name:       "pending op and trigger succeed",
			wantTasks:  []string{types.TypeSummaryGeneration, types.TypeWikiIngest},
			wantStatus: types.ParseStatusFinalizing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pendingRepo := &wikiEnqueueFailurePendingRepo{seedErr: test.pendingErr}
			queue := &wikiEnqueueFailureTaskQueue{}
			service, repo := newWikiEnqueueTestService(knowledgeID, pendingRepo, queue)

			err := service.Handle(context.Background(), newWikiEnqueuePostProcessTask(t, knowledgeID))

			if test.wantErr {
				require.ErrorIs(t, err, test.pendingErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 2, repo.expectedSubtasks, "summary and wiki each seed one slot")
				require.NotNil(t, pendingRepo.seededOp)
				assert.Equal(t, knowledgeID, pendingRepo.seededOp.DedupKey)
			}
			assert.Equal(t, test.wantStatus, repo.knowledge.ParseStatus)
			assert.Equal(t, test.wantTasks, queue.taskTypes)
			assert.Equal(t, 1, pendingRepo.seedCalls)
		})
	}
}

func TestKnowledgePostProcessRetriesWikiTriggerWithoutDoubleAccounting(t *testing.T) {
	const knowledgeID = "knowledge-wiki-trigger-retry"
	wikiErr := errors.New("redis unavailable")
	pendingRepo := &wikiEnqueueFailurePendingRepo{}
	queue := &wikiEnqueueFailureTaskQueue{wikiErr: wikiErr}
	service, repo := newWikiEnqueueTestService(knowledgeID, pendingRepo, queue)
	task := newWikiEnqueuePostProcessTask(t, knowledgeID)

	err := service.Handle(context.Background(), task)

	require.ErrorIs(t, err, wikiErr)
	assert.Equal(t, 2, repo.expectedSubtasks, "summary and wiki each seed one slot")
	assert.Equal(t, 1, pendingRepo.seedCalls)
	assert.Equal(t, []string{types.TypeSummaryGeneration, types.TypeWikiIngest}, queue.taskTypes)

	queue.wikiErr = nil
	err = service.Handle(context.Background(), task)

	require.NoError(t, err)
	assert.Equal(t, 1, pendingRepo.seedCalls, "retry must not append another pending op")
	assert.Equal(t,
		[]string{types.TypeSummaryGeneration, types.TypeWikiIngest, types.TypeWikiIngest},
		queue.taskTypes,
	)
}
