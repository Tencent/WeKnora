package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type dataTableAttemptTrackerStub struct {
	SpanTracker
	latest       []int
	latestErr    error
	latestCalls  int
	openAttempt  int
	finalizeCall int
}

func (s *dataTableAttemptTrackerStub) LatestAttemptWithError(context.Context, string) (int, error) {
	s.latestCalls++
	if s.latestErr != nil {
		return 0, s.latestErr
	}
	if len(s.latest) == 0 {
		return 0, nil
	}
	index := s.latestCalls - 1
	if index >= len(s.latest) {
		index = len(s.latest) - 1
	}
	return s.latest[index], nil
}

func (s *dataTableAttemptTrackerStub) OpenAttempt(
	context.Context, string, string,
) (*Span, int, error) {
	return &Span{KnowledgeID: "knowledge-1", Attempt: s.openAttempt}, s.openAttempt, nil
}

func (s *dataTableAttemptTrackerStub) FinalizeAttempt(
	context.Context, string, int, string, types.JSONMap, string, string,
) {
	s.finalizeCall++
}

type dataTableKnowledgeRepoStub struct {
	interfaces.KnowledgeRepository
}

type dataTableAttemptRepoStub struct {
	interfaces.KnowledgeRepository
	updated     bool
	updateErr   error
	updateCalls int
}

func (s *dataTableAttemptRepoStub) ClaimKnowledgeAttemptProcessing(
	context.Context, string, int,
) (bool, error) {
	return s.updated, s.updateErr
}

func (s *dataTableAttemptRepoStub) UpdateKnowledgeColumnsForAttempt(
	context.Context, string, int, map[string]interface{},
) (bool, error) {
	s.updateCalls++
	return s.updated, s.updateErr
}

type dataTableKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	repo     interfaces.KnowledgeRepository
	getCalls int
}

func (s *dataTableKnowledgeServiceStub) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *dataTableKnowledgeServiceStub) GetKnowledgeByID(
	context.Context, string,
) (*types.Knowledge, error) {
	s.getCalls++
	return nil, errors.New("unexpected knowledge lookup")
}

type dataTableChunkServiceStub struct {
	interfaces.ChunkService
	existing         []*types.Chunk
	listCalls        int
	createCalls      int
	updateChunkCalls int
	updateCalls      int
	deleteCalls      int
	created          []*types.Chunk
}

func (s *dataTableChunkServiceStub) ListChunksByKnowledgeID(
	context.Context, string,
) ([]*types.Chunk, error) {
	s.listCalls++
	return s.existing, nil
}

func (s *dataTableChunkServiceStub) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	s.createCalls++
	s.created = append(s.created, chunks...)
	return nil
}

func (s *dataTableChunkServiceStub) UpdateChunk(context.Context, *types.Chunk) error {
	s.updateChunkCalls++
	return nil
}

func (s *dataTableChunkServiceStub) UpdateChunks(context.Context, []*types.Chunk) error {
	s.updateCalls++
	return nil
}

func (s *dataTableChunkServiceStub) DeleteChunks(context.Context, []string) error {
	s.deleteCalls++
	return nil
}

type dataTableTaskEnqueuerStub struct {
	task  *asynq.Task
	err   error
	calls int
}

func (s *dataTableTaskEnqueuerStub) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	s.calls++
	s.task = task
	if s.err != nil {
		return nil, s.err
	}
	return &asynq.TaskInfo{ID: "datatable-task", Queue: types.QueueSummary}, nil
}

func TestNewDataTableSummaryTaskCarriesAttempt(t *testing.T) {
	queue := &dataTableTaskEnqueuerStub{}
	require.NoError(t, NewDataTableSummaryTask(
		context.Background(), queue, 11, "knowledge-1", "summary-model", "embedding-model", 7,
	))
	require.Equal(t, 1, queue.calls)
	require.Equal(t, types.TypeDataTableSummary, queue.task.Type())

	var payload DataTableSummaryPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	require.Equal(t, 7, payload.Attempt)
	require.Equal(t, uint64(11), payload.TenantID)
	require.Equal(t, "knowledge-1", payload.KnowledgeID)

	zeroAttemptQueue := &dataTableTaskEnqueuerStub{}
	require.Error(t, NewDataTableSummaryTask(
		context.Background(), zeroAttemptQueue, 11, "knowledge-1", "summary-model", "embedding-model", 0,
	))
	require.Zero(t, zeroAttemptQueue.calls)
}

func TestNewDataTableSummaryTaskPropagatesEnqueueError(t *testing.T) {
	wantErr := errors.New("queue unavailable")
	queue := &dataTableTaskEnqueuerStub{err: wantErr}
	err := NewDataTableSummaryTask(
		context.Background(), queue, 11, "knowledge-1", "summary-model", "embedding-model", 7,
	)
	require.ErrorIs(t, err, wantErr)
}

func TestDataTableSummaryHandleChecksAttemptBeforeWork(t *testing.T) {
	tests := []struct {
		name      string
		tracker   *dataTableAttemptTrackerStub
		repo      interfaces.KnowledgeRepository
		wantError error
		wantStale bool
	}{
		{
			name:      "stale task is dropped",
			tracker:   &dataTableAttemptTrackerStub{latest: []int{8}},
			repo:      &dataTableKnowledgeRepoStub{},
			wantStale: true,
		},
		{
			name:      "lookup error fails closed",
			tracker:   &dataTableAttemptTrackerStub{latestErr: errors.New("attempt lookup unavailable")},
			repo:      &dataTableKnowledgeRepoStub{},
			wantError: errors.New("attempt lookup unavailable"),
		},
		{
			name:      "missing attempt aware repository fails closed",
			tracker:   &dataTableAttemptTrackerStub{latest: []int{7}},
			repo:      &dataTableKnowledgeRepoStub{},
			wantError: errors.New("attempt-aware updates"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			knowledgeService := &dataTableKnowledgeServiceStub{repo: test.repo}
			service := &DataTableSummaryService{
				knowledgeService: knowledgeService,
				spanTracker:      test.tracker,
			}
			payload, err := json.Marshal(DataTableSummaryPayload{
				TenantID: 11, KnowledgeID: "knowledge-1", Attempt: 7,
			})
			require.NoError(t, err)
			err = service.Handle(context.Background(), asynq.NewTask(types.TypeDataTableSummary, payload))
			if test.wantStale {
				require.NoError(t, err)
			} else if test.tracker.latestErr != nil {
				require.ErrorIs(t, err, test.tracker.latestErr)
			} else {
				require.ErrorContains(t, err, test.wantError.Error())
			}
			require.Zero(t, knowledgeService.getCalls)
		})
	}
}

func TestDataTableSummaryBuildChunksUsesAttemptScopedDeterministicIDs(t *testing.T) {
	service := &DataTableSummaryService{}
	resources := &extractionResources{knowledge: &types.Knowledge{
		ID: "knowledge-1", TenantID: 11, KnowledgeBaseID: "kb-1",
	}}
	first := service.buildChunks(resources, 7, "table summary", "column summary")
	retry := service.buildChunks(resources, 7, "new table summary", "new column summary")
	nextAttempt := service.buildChunks(resources, 8, "table summary", "column summary")

	require.Len(t, first, 2)
	require.Equal(t, first[0].ID, retry[0].ID)
	require.Equal(t, first[1].ID, retry[1].ID)
	require.NotEqual(t, first[0].ID, first[1].ID)
	require.NotEqual(t, first[0].ID, nextAttempt[0].ID)
	require.NotEqual(t, first[1].ID, nextAttempt[1].ID)
	require.Equal(t, first[1].ID, first[0].NextChunkID)
	require.Equal(t, first[0].ID, first[1].ParentChunkID)
	require.Equal(t, first[0].ID, first[1].PreChunkID)
}

func TestDataTableSummaryIndexStopsWhenAttemptChangesAfterChunkWrite(t *testing.T) {
	tracker := &dataTableAttemptTrackerStub{latest: []int{7, 8}}
	chunkService := &dataTableChunkServiceStub{}
	service := &DataTableSummaryService{spanTracker: tracker, chunkService: chunkService}
	resources := &extractionResources{knowledge: &types.Knowledge{
		ID: "knowledge-1", TenantID: 11, KnowledgeBaseID: "kb-1",
	}}
	chunks := service.buildChunks(resources, 7, "table summary", "column summary")

	err := service.indexToVectorDB(context.Background(), "knowledge-1", 7, chunks, nil, nil)
	require.ErrorIs(t, err, errDataTableSummaryStale)
	require.Equal(t, 1, chunkService.createCalls)
	require.Zero(t, chunkService.updateCalls)
	require.Equal(t, 2, tracker.latestCalls)
}

func TestDataTableSummaryCleanupFailsClosedAfterOwnershipChanges(t *testing.T) {
	t.Run("stale lookup does not mutate", func(t *testing.T) {
		repo := &dataTableAttemptRepoStub{updated: true}
		chunkService := &dataTableChunkServiceStub{}
		service := &DataTableSummaryService{
			spanTracker:      &dataTableAttemptTrackerStub{latest: []int{8}},
			knowledgeService: &dataTableKnowledgeServiceStub{repo: repo},
			chunkService:     chunkService,
		}
		err := service.cleanupOnFailure(
			context.Background(), 7,
			&extractionResources{knowledge: &types.Knowledge{ID: "knowledge-1"}},
			[]*types.Chunk{{ID: "summary-1"}}, errors.New("index failed"),
		)
		require.NoError(t, err)
		require.Zero(t, repo.updateCalls)
		require.Zero(t, chunkService.updateCalls)
		require.Zero(t, chunkService.deleteCalls)
	})

	t.Run("atomic attempt update refusal does not mutate resources", func(t *testing.T) {
		repo := &dataTableAttemptRepoStub{updated: false}
		chunkService := &dataTableChunkServiceStub{}
		service := &DataTableSummaryService{
			spanTracker:      &dataTableAttemptTrackerStub{latest: []int{7}},
			knowledgeService: &dataTableKnowledgeServiceStub{repo: repo},
			chunkService:     chunkService,
		}
		err := service.cleanupOnFailure(
			context.Background(), 7,
			&extractionResources{knowledge: &types.Knowledge{ID: "knowledge-1"}},
			[]*types.Chunk{{ID: "summary-1"}}, errors.New("index failed"),
		)
		require.NoError(t, err)
		require.Equal(t, 1, repo.updateCalls)
		require.Zero(t, chunkService.updateCalls)
		require.Zero(t, chunkService.deleteCalls)
	})

	t.Run("missing updater returns an error before resource mutation", func(t *testing.T) {
		chunkService := &dataTableChunkServiceStub{}
		service := &DataTableSummaryService{
			spanTracker: &dataTableAttemptTrackerStub{latest: []int{7}},
			knowledgeService: &dataTableKnowledgeServiceStub{
				repo: &dataTableKnowledgeRepoStub{},
			},
			chunkService: chunkService,
		}
		err := service.cleanupOnFailure(
			context.Background(), 7,
			&extractionResources{knowledge: &types.Knowledge{ID: "knowledge-1"}},
			[]*types.Chunk{{ID: "summary-1"}}, errors.New("index failed"),
		)
		require.ErrorContains(t, err, "attempt-aware updates")
		require.Zero(t, chunkService.updateCalls)
		require.Zero(t, chunkService.deleteCalls)
	})
}

type dataTableProducerTaskEnqueuer struct {
	tasks   []*asynq.Task
	failErr error
}

func (s *dataTableProducerTaskEnqueuer) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	s.tasks = append(s.tasks, task)
	if task.Type() == types.TypeDataTableSummary && s.failErr != nil {
		return nil, s.failErr
	}
	return &asynq.TaskInfo{ID: "task", Queue: "default"}, nil
}

func (r *createKnowledgeFileRepoStub) ClaimKnowledgeAttemptProcessing(
	context.Context, string, int,
) (bool, error) {
	return true, nil
}

func (r *createKnowledgeFileRepoStub) UpdateKnowledgeColumnsForAttempt(
	context.Context, string, int, map[string]interface{},
) (bool, error) {
	return true, nil
}

func TestCreateKnowledgeFromFileDoesNotSilenceDataTableSummaryEnqueueError(t *testing.T) {
	wantErr := errors.New("summary queue unavailable")
	repo := &createKnowledgeFileRepoStub{}
	queue := &dataTableProducerTaskEnqueuer{failErr: wantErr}
	tracker := &dataTableAttemptTrackerStub{openAttempt: 7}
	service := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID: "kb-1", SummaryModelID: "summary-model", EmbeddingModelID: "embedding-model",
		}},
		fileSvc:     &createKnowledgeFileServiceStub{},
		task:        queue,
		spanTracker: tracker,
	}

	knowledge, err := service.CreateKnowledgeFromFile(
		newCreateKnowledgeFileContext(), "kb-1", newMultipartFileHeader(t, "table.csv", "a,b\n1,2\n"),
		nil, nil, "", nil, "", nil,
	)
	require.NotNil(t, knowledge)
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.Len(t, queue.tasks, 2)

	var documentPayload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &documentPayload))
	var tablePayload DataTableSummaryPayload
	require.NoError(t, json.Unmarshal(queue.tasks[1].Payload(), &tablePayload))
	require.Equal(t, 7, documentPayload.Attempt)
	require.Equal(t, documentPayload.Attempt, tablePayload.Attempt)
}
