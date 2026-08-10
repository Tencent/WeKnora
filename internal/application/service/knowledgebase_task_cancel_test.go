package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
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

type recordingKBWikiRepo struct {
	interfaces.WikiPageRepository
	kbIDs []string
	err   error
}

func (r *recordingKBWikiRepo) DeleteByKnowledgeBaseID(_ context.Context, kbID string) error {
	r.kbIDs = append(r.kbIDs, kbID)
	return r.err
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

func TestProcessKBDeleteRetriesWhenDurableCleanupFails(t *testing.T) {
	pendingErr := errors.New("database unavailable")
	pendingRepo := &recordingKBPendingRepo{deleteErr: pendingErr}
	svc := &knowledgeBaseService{
		kgRepo:          emptyKBKnowledgeRepo{},
		taskPendingRepo: pendingRepo,
	}
	payload, err := json.Marshal(types.KBDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-pending"})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, pendingErr)
	assert.Equal(t, []string{"kb-pending", "kb-pending"}, pendingRepo.scopeIDs)
}

func TestProcessKBDeleteCleansWikiDataWithoutKnowledgeRows(t *testing.T) {
	wikiRepo := &recordingKBWikiRepo{}
	svc := &knowledgeBaseService{
		kgRepo:   emptyKBKnowledgeRepo{},
		wikiRepo: wikiRepo,
	}
	payload, err := json.Marshal(types.KBDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-wiki"})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.NoError(t, err)
	assert.Equal(t, []string{"kb-wiki"}, wikiRepo.kbIDs)
}

func TestProcessKBDeleteRetriesWhenWikiCleanupFails(t *testing.T) {
	wikiErr := errors.New("wiki database unavailable")
	wikiRepo := &recordingKBWikiRepo{err: wikiErr}
	svc := &knowledgeBaseService{
		kgRepo:   emptyKBKnowledgeRepo{},
		wikiRepo: wikiRepo,
	}
	payload, err := json.Marshal(types.KBDeletePayload{TenantID: 1, KnowledgeBaseID: "kb-wiki"})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, wikiErr)
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

type failingKBDeleteEngine struct {
	interfaces.RetrieveEngineService
	err         error
	deleteCalls int
}

func (e *failingKBDeleteEngine) EngineType() types.RetrieverEngineType {
	return types.QdrantRetrieverEngineType
}

func (e *failingKBDeleteEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *failingKBDeleteEngine) DeleteByKnowledgeIDList(
	context.Context, []string, int, string,
) error {
	e.deleteCalls++
	return e.err
}

type trackingKBChunkRepo struct {
	kbCleanupChunkRepo
	deleteCalls int
}

func (r *trackingKBChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	r.deleteCalls++
	return nil
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

func TestProcessKBDeleteVectorFailureKeepsKnowledgeRows(t *testing.T) {
	vectorErr := errors.New("qdrant unavailable")
	engine := &failingKBDeleteEngine{err: vectorErr}
	registry := retriever.NewRetrieveEngineRegistry(nil, nil)
	require.NoError(t, registry.Register(engine))
	repo := &kbDeleteTrackingKnowledgeRepo{populatedKBKnowledgeRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
		{ID: "knowledge-1", KnowledgeBaseID: "kb-1", EmbeddingModelID: "model-1", Type: "file"},
	}}}
	chunkRepo := &trackingKBChunkRepo{}
	svc := &knowledgeBaseService{
		kgRepo:         repo,
		chunkRepo:      chunkRepo,
		modelService:   kbCleanupModelService{},
		retrieveEngine: registry,
	}
	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		EffectiveEngines: []types.RetrieverEngineParams{{
			RetrieverEngineType: types.QdrantRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
		}},
	})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, vectorErr)
	assert.Equal(t, 1, engine.deleteCalls)
	assert.Equal(t, 0, chunkRepo.deleteCalls)
	assert.Equal(t, 0, repo.deleteCalls, "knowledge rows must remain available for retry")
}

// kbDeleteDeferredRegistry reports a retryable engine-resolution failure from
// the rebuild path, matching what GetOrLoadByStoreID does when the caller
// goes away or the store engine cannot be produced yet.
type kbDeleteDeferredRegistry struct {
	err error
}

func (kbDeleteDeferredRegistry) Register(interfaces.RetrieveEngineService) error { return nil }
func (kbDeleteDeferredRegistry) GetRetrieveEngineService(types.RetrieverEngineType) (
	interfaces.RetrieveEngineService, error,
) {
	return nil, nil
}
func (kbDeleteDeferredRegistry) GetAllRetrieveEngineServices() []interfaces.RetrieveEngineService {
	return nil
}
func (kbDeleteDeferredRegistry) GetByStoreID(string) (interfaces.RetrieveEngineService, error) {
	return nil, errors.New("store not in registry")
}
func (r kbDeleteDeferredRegistry) GetOrLoadByStoreID(
	context.Context, uint64, string,
) (interfaces.RetrieveEngineService, error) {
	return nil, r.err
}

type kbDeleteOwnership struct {
	owned map[string]uint64
}

func (o *kbDeleteOwnership) StoreOwnedBy(_ context.Context, storeID string, tenantID uint64) (bool, error) {
	owner, ok := o.owned[storeID]
	return ok && owner == tenantID, nil
}

type kbDeleteTrackingKnowledgeRepo struct {
	populatedKBKnowledgeRepo
	deleteCalls int
}

func (r *kbDeleteTrackingKnowledgeRepo) DeleteKnowledgeList(context.Context, uint64, []string) error {
	r.deleteCalls++
	return nil
}

func TestProcessKBDeleteEngineResolutionFailureRetries(t *testing.T) {
	const storeID = "00000000-0000-0000-0000-0000000000dd"
	storeIDPtr := storeID
	repo := &kbDeleteTrackingKnowledgeRepo{populatedKBKnowledgeRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
		{ID: "knowledge-1", KnowledgeBaseID: "kb-1", EmbeddingModelID: "model-1"},
	}}}
	svc := &knowledgeBaseService{
		kgRepo:         repo,
		chunkRepo:      kbCleanupChunkRepo{},
		modelService:   kbCleanupModelService{},
		retrieveEngine: kbDeleteDeferredRegistry{err: context.Canceled},
		ownership:      &kbDeleteOwnership{owned: map[string]uint64{storeID: 1}},
	}
	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		VectorStoreID:   &storeIDPtr,
	})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, repo.deleteCalls, "knowledge rows must not be deleted when engine resolution is deferred")
}

func TestProcessKBDeleteUnavailableStoreRetries(t *testing.T) {
	const storeID = "00000000-0000-0000-0000-0000000000ee"
	storeIDPtr := storeID
	repo := &kbDeleteTrackingKnowledgeRepo{populatedKBKnowledgeRepo: populatedKBKnowledgeRepo{items: []*types.Knowledge{
		{ID: "knowledge-1", KnowledgeBaseID: "kb-1", EmbeddingModelID: "model-1"},
	}}}
	svc := &knowledgeBaseService{
		kgRepo:         repo,
		chunkRepo:      kbCleanupChunkRepo{},
		modelService:   kbCleanupModelService{},
		retrieveEngine: kbDeleteDeferredRegistry{err: retriever.ErrVectorStoreUnavailable},
		ownership:      &kbDeleteOwnership{owned: map[string]uint64{storeID: 1}},
	}
	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		VectorStoreID:   &storeIDPtr,
	})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payload))

	require.ErrorIs(t, err, retriever.ErrVectorStoreUnavailable)
	assert.Equal(t, 0, repo.deleteCalls, "knowledge rows must not be deleted when engine resolution is deferred")
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
