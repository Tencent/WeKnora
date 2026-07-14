package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type kbDeleteDSRepo struct {
	mu        sync.Mutex
	byKB      map[string][]*types.DataSource
	deleted   map[string]bool
	deleteIDs []string
}

func newKBDeleteDSRepo(kbID string, ds ...*types.DataSource) *kbDeleteDSRepo {
	r := &kbDeleteDSRepo{
		byKB:    map[string][]*types.DataSource{kbID: ds},
		deleted: map[string]bool{},
	}
	return r
}

func (r *kbDeleteDSRepo) Create(_ context.Context, _ *types.DataSource) error { return nil }
func (r *kbDeleteDSRepo) FindByID(_ context.Context, id string) (*types.DataSource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleted[id] {
		return nil, errors.New("data source not found")
	}
	for _, list := range r.byKB {
		for _, ds := range list {
			if ds.ID == id {
				return ds, nil
			}
		}
	}
	return nil, errors.New("data source not found")
}
func (r *kbDeleteDSRepo) FindByKnowledgeBase(_ context.Context, kbID string) ([]*types.DataSource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var active []*types.DataSource
	for _, ds := range r.byKB[kbID] {
		if !r.deleted[ds.ID] {
			active = append(active, ds)
		}
	}
	return active, nil
}
func (r *kbDeleteDSRepo) Update(_ context.Context, _ *types.DataSource) error { return nil }
func (r *kbDeleteDSRepo) UpdateSyncState(_ context.Context, _ *types.DataSource) error {
	return nil
}
func (r *kbDeleteDSRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted[id] = true
	r.deleteIDs = append(r.deleteIDs, id)
	return nil
}
func (r *kbDeleteDSRepo) FindActive(_ context.Context) ([]*types.DataSource, error) {
	return nil, nil
}

var _ interfaces.DataSourceRepository = (*kbDeleteDSRepo)(nil)

type kbDeleteSyncLogRepo struct {
	mu       sync.Mutex
	canceled []string
}

func (r *kbDeleteSyncLogRepo) Create(_ context.Context, _ *types.SyncLog) error { return nil }
func (r *kbDeleteSyncLogRepo) FindByID(_ context.Context, _ string) (*types.SyncLog, error) {
	return nil, errors.New("not found")
}
func (r *kbDeleteSyncLogRepo) FindByDataSource(_ context.Context, _ string, _, _ int) ([]*types.SyncLog, error) {
	return nil, nil
}
func (r *kbDeleteSyncLogRepo) FindLatest(_ context.Context, _ string) (*types.SyncLog, error) {
	return nil, nil
}
func (r *kbDeleteSyncLogRepo) HasRunningSync(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (r *kbDeleteSyncLogRepo) Update(_ context.Context, _ *types.SyncLog) error { return nil }
func (r *kbDeleteSyncLogRepo) UpdateResult(_ context.Context, _ *types.SyncLog) error {
	return nil
}
func (r *kbDeleteSyncLogRepo) CancelPendingByDataSource(_ context.Context, dsID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.canceled = append(r.canceled, dsID)
	return nil
}
func (r *kbDeleteSyncLogRepo) CleanupOldLogs(_ context.Context, _ int) error { return nil }

var _ interfaces.SyncLogRepository = (*kbDeleteSyncLogRepo)(nil)

type kbDeleteKBRepo struct {
	fakeKBRepo
	deletedID string
}

func (r *kbDeleteKBRepo) DeleteKnowledgeBase(_ context.Context, id string) error {
	r.deletedID = id
	delete(r.rows, id)
	return nil
}

type kbDeleteTaskEnqueuer struct{}

func (kbDeleteTaskEnqueuer) Enqueue(_ *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "kb-delete-task"}, nil
}

func TestDeleteDataSourcesForKnowledgeBase(t *testing.T) {
	const kbID = "kb-1"
	dsRepo := newKBDeleteDSRepo(kbID,
		&types.DataSource{ID: "ds-1", KnowledgeBaseID: kbID, Status: types.DataSourceStatusActive, SyncSchedule: "0 0 * * * *"},
		&types.DataSource{ID: "ds-2", KnowledgeBaseID: kbID, Status: types.DataSourceStatusActive},
	)
	syncLogRepo := &kbDeleteSyncLogRepo{}
	kbRepo := &kbDeleteKBRepo{fakeKBRepo: *newFakeKBRepo()}
	kbRepo.rows[kbID] = &types.KnowledgeBase{ID: kbID, TenantID: 1, Name: "test"}

	scheduler := datasource.NewScheduler(dsRepo, syncLogRepo, kbDeleteTaskEnqueuer{})
	require.NoError(t, scheduler.AddOrUpdate(dsRepo.byKB[kbID][0]))

	svc := &knowledgeBaseService{
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		dsScheduler: scheduler,
	}

	svc.deleteDataSourcesForKnowledgeBase(ctxWithTenant(1), kbID)

	assert.ElementsMatch(t, []string{"ds-1", "ds-2"}, dsRepo.deleteIDs)
	assert.ElementsMatch(t, []string{"ds-1", "ds-2"}, syncLogRepo.canceled)
	assert.Equal(t, 0, scheduler.EntryCount())
}

func TestDeleteKnowledgeBaseCleansUpDataSources(t *testing.T) {
	const kbID = "kb-1"
	dsRepo := newKBDeleteDSRepo(kbID,
		&types.DataSource{ID: "ds-1", KnowledgeBaseID: kbID, Status: types.DataSourceStatusActive, SyncSchedule: "0 0 * * * *"},
	)
	syncLogRepo := &kbDeleteSyncLogRepo{}
	kbRepo := &kbDeleteKBRepo{fakeKBRepo: *newFakeKBRepo()}
	kbRepo.rows[kbID] = &types.KnowledgeBase{ID: kbID, TenantID: 1, Name: "test"}

	scheduler := datasource.NewScheduler(dsRepo, syncLogRepo, kbDeleteTaskEnqueuer{})
	require.NoError(t, scheduler.AddOrUpdate(dsRepo.byKB[kbID][0]))

	svc := &knowledgeBaseService{
		repo:        kbRepo,
		shareRepo:   nil,
		asynqClient: kbDeleteTaskEnqueuer{},
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		dsScheduler: scheduler,
	}

	ctx := ctxWithTenantStorage(1, "local")
	err := svc.DeleteKnowledgeBase(ctx, kbID)
	require.NoError(t, err)

	assert.Equal(t, kbID, kbRepo.deletedID)
	assert.Equal(t, []string{"ds-1"}, dsRepo.deleteIDs)
	assert.Equal(t, []string{"ds-1"}, syncLogRepo.canceled)
	assert.Equal(t, 0, scheduler.EntryCount())
}

func TestDeleteDataSourcesForKnowledgeBaseContinuesOnDeleteError(t *testing.T) {
	const kbID = "kb-2"
	dsRepo := &deleteErrDSRepo{
		kbDeleteDSRepo: *newKBDeleteDSRepo(kbID, &types.DataSource{ID: "ds-bad", KnowledgeBaseID: kbID}),
		deleteErr:      errors.New("db unavailable"),
	}

	svc := &knowledgeBaseService{
		dsRepo:      dsRepo,
		syncLogRepo: &kbDeleteSyncLogRepo{},
	}

	svc.deleteDataSourcesForKnowledgeBase(context.Background(), kbID)
	assert.Empty(t, dsRepo.deleteIDs)
}

func TestDeleteKnowledgeBaseContinuesWhenDataSourceCleanupFails(t *testing.T) {
	const kbID = "kb-2"
	dsRepo := &deleteErrDSRepo{
		kbDeleteDSRepo: *newKBDeleteDSRepo(kbID, &types.DataSource{ID: "ds-bad", KnowledgeBaseID: kbID}),
		deleteErr:      errors.New("db unavailable"),
	}

	kbRepo := &kbDeleteKBRepo{fakeKBRepo: *newFakeKBRepo()}
	kbRepo.rows[kbID] = &types.KnowledgeBase{ID: kbID, TenantID: 1, Name: "test"}

	svc := &knowledgeBaseService{
		repo:        kbRepo,
		asynqClient: kbDeleteTaskEnqueuer{},
		dsRepo:      dsRepo,
		syncLogRepo: &kbDeleteSyncLogRepo{},
	}

	err := svc.DeleteKnowledgeBase(ctxWithTenantStorage(1, "local"), kbID)
	require.NoError(t, err)
	assert.Equal(t, kbID, kbRepo.deletedID)
}

// deleteErrDSRepo injects a delete failure for testing best-effort cleanup.
type deleteErrDSRepo struct {
	kbDeleteDSRepo
	deleteErr error
}

func (r *deleteErrDSRepo) Delete(_ context.Context, id string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return r.kbDeleteDSRepo.Delete(context.Background(), id)
}
