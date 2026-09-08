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

func (r *wikiEnqueueFailureKnowledgeRepo) FinalizeSubtask(
	context.Context,
	string,
) (int, bool, error) {
	return 0, false, nil
}

func (r *wikiEnqueueFailureKnowledgeRepo) UpdateKnowledgeColumn(
	context.Context,
	string,
	string,
	interface{},
) error {
	return nil
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
	taskTypes       []string
	extractChunkIDs []string
	wikiErr         error
}

func (q *wikiEnqueueFailureTaskQueue) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	q.taskTypes = append(q.taskTypes, task.Type())
	if task.Type() == types.TypeWikiIngest {
		return nil, q.wikiErr
	}
	if task.Type() == types.TypeChunkExtract {
		var payload types.ExtractChunkPayload
		if err := json.Unmarshal(task.Payload(), &payload); err == nil {
			q.extractChunkIDs = append(q.extractChunkIDs, payload.ChunkID)
		}
	}
	return &asynq.TaskInfo{ID: "queued", Type: task.Type()}, nil
}

type wikiEnqueueFailurePendingRepo struct {
	interfaces.TaskPendingOpsRepository
	seedErr       error
	seedCalls     int
	seededOp      *types.TaskPendingOp
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

type wikiEnqueueFailureChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *wikiEnqueueFailureChunkRepo) ListChunksByKnowledgeIDAndTypes(
	_ context.Context,
	_ uint64,
	_ string,
	chunkTypes []types.ChunkType,
) ([]*types.Chunk, error) {
	allowed := make(map[types.ChunkType]struct{}, len(chunkTypes))
	for _, ct := range chunkTypes {
		allowed[ct] = struct{}{}
	}
	var out []*types.Chunk
	for _, c := range r.chunks {
		if _, ok := allowed[c.ChunkType]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func newWikiEnqueueTestService(
	knowledgeID string,
	pendingRepo *wikiEnqueueFailurePendingRepo,
	queue *wikiEnqueueFailureTaskQueue,
) (*KnowledgePostProcessService, *wikiEnqueueFailureKnowledgeRepo) {
	repo := &wikiEnqueueFailureKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID:              knowledgeID,
			TenantID:        7,
			KnowledgeBaseID: "kb-wiki",
			ParseStatus:     types.ParseStatusProcessing,
		},
	}
	pendingRepo.knowledgeRepo = repo
	return &KnowledgePostProcessService{
		knowledgeRepo: repo,
		kbService: &wikiEnqueueFailureKBService{kb: &types.KnowledgeBase{
			ID:       "kb-wiki",
			TenantID: 7,
			IndexingStrategy: types.IndexingStrategy{
				WikiEnabled: true,
			},
		}},
		chunkService: &wikiEnqueueFailureChunkService{chunks: []*types.Chunk{
			{
				ID:              "chunk-1",
				TenantID:        7,
				KnowledgeID:     knowledgeID,
				KnowledgeBaseID: "kb-wiki",
				ChunkType:       types.ChunkTypeText,
			},
		}},
		chunkRepo: &wikiEnqueueFailureChunkRepo{chunks: []*types.Chunk{
			{
				ID:              "chunk-1",
				TenantID:        7,
				KnowledgeID:     knowledgeID,
				KnowledgeBaseID: "kb-wiki",
				ChunkType:       types.ChunkTypeText,
				Content:         "plain text",
			},
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

func TestPostProcessRejectsMovedKnowledgeBeforeWikiOrSpanWrites(t *testing.T) {
	pending := &wikiEnqueueFailurePendingRepo{}
	queue := &wikiEnqueueFailureTaskQueue{}
	svc, repo := newWikiEnqueueTestService("moved-doc", pending, queue)
	repo.knowledge.KnowledgeBaseID = "other-kb"
	require.ErrorIs(t, svc.Handle(context.Background(), newWikiEnqueuePostProcessTask(t, "moved-doc")), asynq.SkipRetry)
	require.Zero(t, repo.expectedSubtasks)
	require.Equal(t, types.ParseStatusProcessing, repo.knowledge.ParseStatus)
}
