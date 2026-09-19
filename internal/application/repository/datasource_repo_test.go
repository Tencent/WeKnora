package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupDataSourceRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.SyncLog{}))
	return db
}

func TestSyncLogRepositoryDispatchStateTransitionsAreConditional(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewSyncLogRepository(db).(*SyncLogRepository)
	ctx := context.Background()

	pending := &types.SyncLog{
		ID:                "outbox-pending",
		DataSourceID:      "ds-1",
		TenantID:          1,
		Status:            types.SyncLogStatusPending,
		TaskID:            "dssync:ds-1:outbox-pending",
		DispatchAttempts:  2,
		LastDispatchError: "old error",
	}
	terminal := &types.SyncLog{
		ID:           "outbox-terminal",
		DataSourceID: "ds-1",
		TenantID:     1,
		Status:       types.SyncLogStatusCanceled,
		TaskID:       "dssync:ds-1:outbox-terminal",
	}
	require.NoError(t, repo.Create(ctx, pending))
	require.NoError(t, repo.Create(ctx, terminal))

	dispatchedAt := time.Now().UTC()
	require.NoError(t, repo.MarkDispatched(ctx, pending.ID, dispatchedAt))

	// A late queue failure from another dispatcher must not make the already
	// delivered row eligible for another attempt.
	retryAt := dispatchedAt.Add(time.Minute)
	require.NoError(t, repo.MarkDispatchFailure(ctx, pending.ID, 99, retryAt, "late failure"))

	var storedPending, storedTerminal types.SyncLog
	require.NoError(t, db.First(&storedPending, "id = ?", pending.ID).Error)
	require.NoError(t, db.First(&storedTerminal, "id = ?", terminal.ID).Error)
	assert.NotNil(t, storedPending.DispatchedAt)
	assert.Equal(t, 2, storedPending.DispatchAttempts)
	assert.Empty(t, storedPending.LastDispatchError)
	assert.Nil(t, storedPending.NextDispatchAt)

	// A terminal row can still need the delivery marker when the worker raced
	// ahead of the producer. Marking delivery does not change its status.
	require.NoError(t, repo.MarkDispatched(ctx, terminal.ID, dispatchedAt))
	var reloadedTerminal types.SyncLog
	require.NoError(t, db.First(&reloadedTerminal, "id = ?", terminal.ID).Error)
	assert.NotNil(t, reloadedTerminal.DispatchedAt)
}

func TestDataSourceRepositoryUpdateSyncStateClearsErrorMessage(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	now := time.Now().UTC()
	result := types.JSON(`{"total":0}`)

	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Feishu",
		Type:            types.ConnectorTypeFeishu,
		Status:          types.DataSourceStatusError,
		ErrorMessage:    "previous failure",
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	ds.Status = types.DataSourceStatusActive
	ds.ErrorMessage = ""
	ds.LastSyncAt = &now
	ds.LastSyncResult = result
	require.NoError(t, repo.UpdateSyncState(context.Background(), ds))

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.Equal(t, types.DataSourceStatusActive, stored.Status)
	assert.Empty(t, stored.ErrorMessage)
	assert.Equal(t, result.ToString(), stored.LastSyncResult.ToString())
	require.NotNil(t, stored.LastSyncAt)
}

func TestDataSourceRepositoryUpdatePersistsDisabledSyncDeletions(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	ctx := context.Background()

	ds := &types.DataSource{
		ID:              "ds-sync-deletions",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Feishu",
		Type:            types.ConnectorTypeFeishu,
		SyncDeletions:   true,
	}
	require.NoError(t, repo.Create(ctx, ds))

	ds.SyncDeletions = false
	require.NoError(t, repo.Update(ctx, ds))
	assert.False(t, ds.SyncDeletions)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.False(t, stored.SyncDeletions)

	ds.SyncDeletions = true
	require.NoError(t, repo.Update(ctx, ds))
	assert.True(t, ds.SyncDeletions)
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.True(t, stored.SyncDeletions)
}

func TestDataSourceRepositoryCreatePersistsDisabledSyncDeletions(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	ctx := context.Background()

	ds := &types.DataSource{
		ID:              "ds-create-sync-deletions",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Feishu",
		Type:            types.ConnectorTypeFeishu,
		SyncDeletions:   false,
	}
	require.NoError(t, repo.Create(ctx, ds))
	assert.False(t, ds.SyncDeletions, "Create must not leave the in-memory field hydrated to the GORM default")

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.False(t, stored.SyncDeletions)

	// A later Updates() on the same pointer must not persist the hydrated default.
	ds.Name = "Renamed"
	require.NoError(t, repo.Update(ctx, ds))
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.False(t, stored.SyncDeletions)
	assert.Equal(t, "Renamed", stored.Name)
}

func TestDataSourceRepositoryCreatePersistsEnabledSyncDeletions(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	ctx := context.Background()

	ds := &types.DataSource{
		ID:              "ds-create-sync-deletions-enabled",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Feishu",
		Type:            types.ConnectorTypeFeishu,
		SyncDeletions:   true,
	}
	require.NoError(t, repo.Create(ctx, ds))
	assert.True(t, ds.SyncDeletions)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.True(t, stored.SyncDeletions)
}

func TestDataSourceRepositoryDeleteSoftDeletesOnSQLite(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	ctx := context.Background()

	target := &types.DataSource{
		ID:              "ds-delete-target",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Delete target",
		Type:            types.ConnectorTypeFeishu,
	}
	other := &types.DataSource{
		ID:              "ds-delete-other",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Other data source",
		Type:            types.ConnectorTypeFeishu,
	}
	require.NoError(t, repo.Create(ctx, target))
	require.NoError(t, repo.Create(ctx, other))

	require.NoError(t, repo.Delete(ctx, target.ID))

	var deleted types.DataSource
	require.NoError(t, db.Unscoped().First(&deleted, "id = ?", target.ID).Error)
	assert.True(t, deleted.DeletedAt.Valid)

	found, err := repo.FindByID(ctx, target.ID)
	assert.Error(t, err)
	assert.Nil(t, found)

	untouched, err := repo.FindByID(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, other.ID, untouched.ID)
}

func TestDataSourceRepositoryFinalizeSyncCommitsBothTables(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	ctx := context.Background()

	ds := &types.DataSource{
		ID: "ds-finalize", TenantID: 1, KnowledgeBaseID: "kb-1",
		Name: "Feishu", Type: types.ConnectorTypeFeishu,
	}
	log := &types.SyncLog{
		ID: "log-finalize", DataSourceID: ds.ID, TenantID: 1,
		Status: types.SyncLogStatusRunning,
	}
	require.NoError(t, repo.Create(ctx, ds))
	require.NoError(t, NewSyncLogRepository(db).Create(ctx, log))

	finished := time.Now().UTC()
	ds.Status = types.DataSourceStatusActive
	ds.LastSyncCursor = types.JSON(`{"connector_cursor":{"page":"2"}}`)
	log.Status = types.SyncLogStatusSuccess
	log.FinishedAt = &finished
	log.ItemsCreated = 3
	require.NoError(t, repo.FinalizeSync(ctx, ds, log))

	var storedDS types.DataSource
	var storedLog types.SyncLog
	require.NoError(t, db.First(&storedDS, "id = ?", ds.ID).Error)
	require.NoError(t, db.First(&storedLog, "id = ?", log.ID).Error)
	assert.Equal(t, types.DataSourceStatusActive, storedDS.Status)
	assert.Equal(t, `{"connector_cursor":{"page":"2"}}`, string(storedDS.LastSyncCursor))
	assert.Equal(t, types.SyncLogStatusSuccess, storedLog.Status)
	assert.Equal(t, 3, storedLog.ItemsCreated)
	require.NotNil(t, storedLog.FinishedAt)
}

func TestDataSourceRepositoryFinalizeSyncRejectsNilInputs(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	ctx := context.Background()

	ds := &types.DataSource{ID: "ds-finalize-nil", TenantID: 1, KnowledgeBaseID: "kb-1"}
	require.NoError(t, repo.Create(ctx, ds))

	assert.Error(t, repo.FinalizeSync(ctx, nil, &types.SyncLog{ID: "l"}))
	assert.Error(t, repo.FinalizeSync(ctx, ds, nil))
	assert.Error(t, repo.FinalizeSync(ctx, &types.DataSource{}, &types.SyncLog{ID: "l"}))
	assert.Error(t, repo.FinalizeSync(ctx, ds, &types.SyncLog{}))
}

func TestSyncLogRepositoryUpdateResultClearsErrorMessage(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewSyncLogRepository(db)
	finishedAt := time.Now().UTC()
	result := types.JSON(`{"total":0}`)

	log := &types.SyncLog{
		ID:           "log-1",
		DataSourceID: "ds-1",
		TenantID:     1,
		Status:       types.SyncLogStatusFailed,
		ErrorMessage: "previous failure",
		ItemsTotal:   1,
		ItemsFailed:  1,
	}
	require.NoError(t, repo.Create(context.Background(), log))

	log.Status = types.SyncLogStatusSuccess
	log.ErrorMessage = ""
	log.FinishedAt = &finishedAt
	log.ItemsTotal = 0
	log.ItemsFailed = 0
	log.Result = result
	require.NoError(t, repo.UpdateResult(context.Background(), log))

	var stored types.SyncLog
	require.NoError(t, db.First(&stored, "id = ?", log.ID).Error)
	assert.Equal(t, types.SyncLogStatusSuccess, stored.Status)
	assert.Empty(t, stored.ErrorMessage)
	assert.Zero(t, stored.ItemsTotal)
	assert.Zero(t, stored.ItemsFailed)
	assert.Equal(t, result.ToString(), stored.Result.ToString())
	require.NotNil(t, stored.FinishedAt)
}
