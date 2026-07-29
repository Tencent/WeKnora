package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type kbTaskCancelCall struct {
	kbID          string
	knowledgeIDs  []string
	dataSourceIDs []string
}

type recordingKBTaskInspector struct {
	repo                 *kbDeleteKBRepo
	calls                []kbTaskCancelCall
	cancelErr            error
	sawSoftDeletedRecord bool
}

func (r *recordingKBTaskInspector) CancelTasksForKnowledge(
	context.Context,
	string,
) (int, int, error) {
	return 0, 0, nil
}

func (r *recordingKBTaskInspector) HasQueuedTasksForKnowledge(context.Context, string) (bool, error) {
	return false, nil
}

func (r *recordingKBTaskInspector) QueueStats(context.Context) ([]types.QueueStat, bool, error) {
	return nil, true, nil
}

func (r *recordingKBTaskInspector) WorkerServerStats(context.Context) ([]types.WorkerServerStat, bool, error) {
	return nil, true, nil
}

func (r *recordingKBTaskInspector) CancelTasksForKnowledgeBase(
	_ context.Context,
	kbID string,
	knowledgeIDs []string,
	dataSourceIDs []string,
) (int, int, error) {
	r.calls = append(r.calls, kbTaskCancelCall{
		kbID:          kbID,
		knowledgeIDs:  append([]string(nil), knowledgeIDs...),
		dataSourceIDs: append([]string(nil), dataSourceIDs...),
	})
	if r.repo != nil && r.repo.deletedID == kbID {
		r.sawSoftDeletedRecord = true
	}
	return 0, 0, r.cancelErr
}

var (
	_ interfaces.TaskInspector              = (*recordingKBTaskInspector)(nil)
	_ interfaces.KnowledgeBaseTaskCanceller = (*recordingKBTaskInspector)(nil)
)

type recordingKBDeleteEnqueuer struct {
	calls int
	task  *asynq.Task
}

type recordingKBPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	scopeIDs  []string
	deleteErr error
}

func (r *recordingKBPendingRepo) DeleteByScope(_ context.Context, scope, scopeID string) error {
	if scope == types.TaskScopeKnowledgeBase {
		r.scopeIDs = append(r.scopeIDs, scopeID)
	}
	return r.deleteErr
}

func (r *recordingKBDeleteEnqueuer) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	r.calls++
	r.task = task
	return &asynq.TaskInfo{ID: "kb-delete-task"}, nil
}

func TestDeleteKnowledgeBaseForwardsDataSourceTaskScope(t *testing.T) {
	const kbID = "kb-with-datasource"
	kbRepo := &kbDeleteKBRepo{fakeKBRepo: *newFakeKBRepo()}
	kbRepo.rows[kbID] = &types.KnowledgeBase{ID: kbID, TenantID: 1, Name: "test"}
	inspector := &recordingKBTaskInspector{repo: kbRepo}
	enqueuer := &recordingKBDeleteEnqueuer{}
	dsRepo := newKBDeleteDSRepo(kbID, &types.DataSource{ID: "datasource-1", KnowledgeBaseID: kbID})
	svc := &knowledgeBaseService{
		repo:          kbRepo,
		asynqClient:   enqueuer,
		taskInspector: inspector,
		dsRepo:        dsRepo,
	}

	err := svc.DeleteKnowledgeBase(ctxWithTenantStorage(1, "local"), kbID)

	require.NoError(t, err)
	require.Len(t, inspector.calls, 2)
	assert.Empty(t, inspector.calls[0].dataSourceIDs)
	assert.Equal(t, []string{"datasource-1"}, inspector.calls[1].dataSourceIDs)
	require.NotNil(t, enqueuer.task)
	var payload types.KBDeletePayload
	require.NoError(t, json.Unmarshal(enqueuer.task.Payload(), &payload))
	assert.Equal(t, []string{"datasource-1"}, payload.DataSourceIDs)
}

func TestDeleteKnowledgeBaseCancelsQueuedTasksBestEffort(t *testing.T) {
	tests := []struct {
		name       string
		cancelErr  error
		pendingErr error
	}{
		{name: "success"},
		{name: "inspector failure", cancelErr: errors.New("redis unavailable")},
		{name: "durable queue failure", pendingErr: errors.New("database unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const kbID = "kb-task-cleanup"
			kbRepo := &kbDeleteKBRepo{fakeKBRepo: *newFakeKBRepo()}
			kbRepo.rows[kbID] = &types.KnowledgeBase{ID: kbID, TenantID: 1, Name: "test"}
			inspector := &recordingKBTaskInspector{repo: kbRepo, cancelErr: tt.cancelErr}
			pendingRepo := &recordingKBPendingRepo{deleteErr: tt.pendingErr}
			enqueuer := &recordingKBDeleteEnqueuer{}
			svc := &knowledgeBaseService{
				repo:            kbRepo,
				asynqClient:     enqueuer,
				taskInspector:   inspector,
				taskPendingRepo: pendingRepo,
			}

			err := svc.DeleteKnowledgeBase(ctxWithTenantStorage(1, "local"), kbID)

			require.NoError(t, err)
			require.Len(t, inspector.calls, 1)
			assert.Equal(t, kbID, inspector.calls[0].kbID)
			assert.Empty(t, inspector.calls[0].knowledgeIDs)
			assert.True(t, inspector.sawSoftDeletedRecord)
			assert.Equal(t, []string{kbID}, pendingRepo.scopeIDs)
			assert.Equal(t, 1, enqueuer.calls)
		})
	}
}

type emptyKBKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (emptyKBKnowledgeRepo) ListKnowledgeByKnowledgeBaseID(
	context.Context,
	uint64,
	string,
) ([]*types.Knowledge, error) {
	return nil, nil
}

func TestProcessKBDeleteRepeatsQueueCleanup(t *testing.T) {
	inspector := &recordingKBTaskInspector{}
	pendingRepo := &recordingKBPendingRepo{}
	svc := &knowledgeBaseService{
		kgRepo:          emptyKBKnowledgeRepo{},
		taskInspector:   inspector,
		taskPendingRepo: pendingRepo,
	}
	payload, err := json.Marshal(types.KBDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-race"})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.NoError(t, err)
	require.Len(t, inspector.calls, 2)
	for _, call := range inspector.calls {
		assert.Equal(t, "kb-race", call.kbID)
		assert.Empty(t, call.knowledgeIDs)
	}
	assert.Equal(t, []string{"kb-race", "kb-race"}, pendingRepo.scopeIDs)
}

type populatedKBKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	items []*types.Knowledge
}

func (r populatedKBKnowledgeRepo) ListKnowledgeByKnowledgeBaseID(
	context.Context,
	uint64,
	string,
) ([]*types.Knowledge, error) {
	return r.items, nil
}

func (populatedKBKnowledgeRepo) DeleteKnowledgeList(context.Context, uint64, []string) error {
	return nil
}

type kbCleanupChunkRepo struct {
	interfaces.ChunkRepository
}

func (kbCleanupChunkRepo) ListImageInfoByKnowledgeIDs(
	context.Context,
	uint64,
	[]string,
) ([]interfaces.ChunkImageInfo, error) {
	return nil, nil
}

func (kbCleanupChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	return nil
}

type kbCleanupModelService struct {
	interfaces.ModelService
}

func (kbCleanupModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return kbCleanupEmbedder{}, nil
}

type kbCleanupEmbedder struct{}

func (kbCleanupEmbedder) Embed(context.Context, string) ([]float32, error) { return nil, nil }
func (kbCleanupEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (kbCleanupEmbedder) GetModelName() string { return "test" }
func (kbCleanupEmbedder) GetDimensions() int   { return 1 }
func (kbCleanupEmbedder) GetModelID() string   { return "test" }
func (kbCleanupEmbedder) BatchEmbedWithPool(
	context.Context,
	embedding.Embedder,
	[]string,
) ([][]float32, error) {
	return nil, nil
}

func TestProcessKBDeleteCollectsKnowledgeIDsForEveryScrub(t *testing.T) {
	inspector := &recordingKBTaskInspector{}
	svc := &knowledgeBaseService{
		kgRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
			{ID: "knowledge-1", KnowledgeBaseID: "kb-1", EmbeddingModelID: "model-1"},
			{ID: "knowledge-2", KnowledgeBaseID: "kb-1", EmbeddingModelID: "model-1"},
		}},
		chunkRepo:     kbCleanupChunkRepo{},
		modelService:  kbCleanupModelService{},
		taskInspector: inspector,
	}
	payload, err := json.Marshal(types.KBDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-1"})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.NoError(t, err)
	require.Len(t, inspector.calls, 2)
	for _, call := range inspector.calls {
		assert.Equal(t, []string{"knowledge-1", "knowledge-2"}, call.knowledgeIDs)
	}
}

func TestCancelTasksForKnowledgeBaseForwardsKnowledgeIDs(t *testing.T) {
	inspector := &recordingKBTaskInspector{}
	svc := &knowledgeBaseService{taskInspector: inspector}

	svc.cancelTasksForKnowledgeBase(
		context.Background(),
		"kb-1",
		[]string{"knowledge-1", "knowledge-2"},
		[]string{"datasource-1"},
	)

	require.Len(t, inspector.calls, 1)
	assert.Equal(t, "kb-1", inspector.calls[0].kbID)
	assert.Equal(t, []string{"knowledge-1", "knowledge-2"}, inspector.calls[0].knowledgeIDs)
	assert.Equal(t, []string{"datasource-1"}, inspector.calls[0].dataSourceIDs)
}
