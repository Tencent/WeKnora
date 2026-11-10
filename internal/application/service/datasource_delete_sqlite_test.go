package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProcessSyncRetriesMetadataLookupErrors(t *testing.T) {
	for _, table := range []string{"data_sources", "sync_logs", "knowledge_bases"} {
		t.Run(table, func(t *testing.T) {
			fixture := newSQLiteDataSourceDeleteFixture(t)
			lookupErr := errors.New("temporary metadata read failure")
			kbService := &processSyncKBService{}
			if table == "knowledge_bases" {
				kbService.getErr = lookupErr
			}
			failLookup := true
			query := fixture.db.Callback().Query().Before("gorm:query")
			require.NoError(t, query.Register("fail_sync_lookup", func(tx *gorm.DB) {
				if failLookup && tx.Statement.Table == table {
					_ = tx.AddError(lookupErr)
				}
			}))
			svc := &DataSourceService{dsRepo: fixture.dsRepo, syncLogRepo: fixture.syncLogRepo, kbService: kbService}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: fixture.ds.ID,
				TenantID:     fixture.ds.TenantID,
				SyncLogID:    fixture.runningLog.ID,
			})
			require.NoError(t, err)
			err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
			assert.ErrorIs(t, err, lookupErr)
			assert.NotErrorIs(t, err, asynq.SkipRetry)

			failLookup = false
			log, err := fixture.syncLogRepo.FindByID(context.Background(), fixture.runningLog.ID)
			require.NoError(t, err)
			assert.Equal(t, types.SyncLogStatusRunning, log.Status)
			assert.Nil(t, log.FinishedAt)
			assert.Empty(t, log.ErrorMessage)
		})
	}
}

func TestProcessSyncAcknowledgesMissingMetadata(t *testing.T) {
	for _, missing := range []string{"data source", "sync log"} {
		t.Run(missing, func(t *testing.T) {
			fixture := newSQLiteDataSourceDeleteFixture(t)
			ctx := context.Background()
			logID := fixture.runningLog.ID
			if missing == "data source" {
				require.NoError(t, fixture.dsRepo.Delete(ctx, fixture.ds.ID))
			} else {
				logID = "missing-log"
			}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: fixture.ds.ID,
				TenantID:     fixture.ds.TenantID,
				SyncLogID:    logID,
			})
			require.NoError(t, err)
			svc := &DataSourceService{dsRepo: fixture.dsRepo, syncLogRepo: fixture.syncLogRepo}
			require.NoError(t, svc.ProcessSync(ctx, asynq.NewTask(types.TypeDataSourceSync, payload)))
			log, err := fixture.syncLogRepo.FindByID(ctx, fixture.runningLog.ID)
			require.NoError(t, err)
			if missing == "data source" {
				assert.Equal(t, types.SyncLogStatusCanceled, log.Status)
				assert.Equal(t, "data source has been deleted", log.ErrorMessage)
				assert.NotNil(t, log.FinishedAt)
			} else {
				assert.Equal(t, types.SyncLogStatusRunning, log.Status)
				assert.Nil(t, log.FinishedAt)
			}
		})
	}
}

type sqliteDataSourceDeleteFixture struct {
	db          *gorm.DB
	dsRepo      interfaces.DataSourceRepository
	syncLogRepo interfaces.SyncLogRepository
	scheduler   *datasource.Scheduler
	ds          *types.DataSource
	pendingLog  *types.SyncLog
	runningLog  *types.SyncLog
}

func newSQLiteDataSourceDeleteFixture(t *testing.T) *sqliteDataSourceDeleteFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "weknora.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.SyncLog{}))

	dsRepo := repository.NewDataSourceRepository(db)
	syncLogRepo := repository.NewSyncLogRepository(db)
	ds := &types.DataSource{
		ID:              "ds-sqlite-delete",
		TenantID:        1,
		KnowledgeBaseID: "kb-sqlite-delete",
		Name:            "SQLite delete",
		Type:            types.ConnectorTypeFeishu,
		Status:          types.DataSourceStatusActive,
		SyncSchedule:    "0 0 * * * *",
	}
	pendingLog := &types.SyncLog{
		ID:           "log-pending",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       "pending",
	}
	runningLog := &types.SyncLog{
		ID:           "log-running",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
	}
	require.NoError(t, dsRepo.Create(context.Background(), ds))
	require.NoError(t, syncLogRepo.Create(context.Background(), pendingLog))
	require.NoError(t, syncLogRepo.Create(context.Background(), runningLog))

	scheduler := datasource.NewScheduler(dsRepo, syncLogRepo, kbDeleteTaskEnqueuer{})
	require.NoError(t, scheduler.AddOrUpdate(ds))
	require.Equal(t, 1, scheduler.EntryCount())

	return &sqliteDataSourceDeleteFixture{
		db:          db,
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		scheduler:   scheduler,
		ds:          ds,
		pendingLog:  pendingLog,
		runningLog:  runningLog,
	}
}

func TestDataSourceServiceDeleteSQLiteCleansUpAfterSoftDelete(t *testing.T) {
	fixture := newSQLiteDataSourceDeleteFixture(t)
	svc := &DataSourceService{
		dsRepo:      fixture.dsRepo,
		syncLogRepo: fixture.syncLogRepo,
		scheduler:   fixture.scheduler,
	}

	require.NoError(t, svc.DeleteDataSource(context.Background(), fixture.ds.ID))

	_, err := fixture.dsRepo.FindByID(context.Background(), fixture.ds.ID)
	require.EqualError(t, err, "data source not found")
	assert.Equal(t, 0, fixture.scheduler.EntryCount())

	for _, logID := range []string{fixture.pendingLog.ID, fixture.runningLog.ID} {
		log, err := fixture.syncLogRepo.FindByID(context.Background(), logID)
		require.NoError(t, err)
		assert.Equal(t, types.SyncLogStatusCanceled, log.Status)
		require.NotNil(t, log.FinishedAt)
		assert.Equal(t, "data source deleted", log.ErrorMessage)
	}
}

func TestDataSourceServiceDeleteKeepsCleanupStateWhenSoftDeleteFails(t *testing.T) {
	fixture := newSQLiteDataSourceDeleteFixture(t)
	require.NoError(t, fixture.db.Exec(`
		CREATE TRIGGER fail_datasource_soft_delete
		BEFORE UPDATE OF deleted_at ON data_sources
		WHEN NEW.id = 'ds-sqlite-delete'
		BEGIN
			SELECT RAISE(FAIL, 'forced soft delete failure');
		END;
	`).Error)
	svc := &DataSourceService{
		dsRepo:      fixture.dsRepo,
		syncLogRepo: fixture.syncLogRepo,
		scheduler:   fixture.scheduler,
	}

	err := svc.DeleteDataSource(context.Background(), fixture.ds.ID)
	require.ErrorContains(t, err, "forced soft delete failure")

	found, err := fixture.dsRepo.FindByID(context.Background(), fixture.ds.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.ds.ID, found.ID)
	assert.Equal(t, 1, fixture.scheduler.EntryCount())

	pending, err := fixture.syncLogRepo.FindByID(context.Background(), fixture.pendingLog.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", pending.Status)
	running, err := fixture.syncLogRepo.FindByID(context.Background(), fixture.runningLog.ID)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusRunning, running.Status)
}

func TestDeleteKnowledgeBaseCleansUpSQLiteDataSources(t *testing.T) {
	fixture := newSQLiteDataSourceDeleteFixture(t)
	kbRepo := &kbDeleteKBRepo{fakeKBRepo: *newFakeKBRepo()}
	kbRepo.rows[fixture.ds.KnowledgeBaseID] = &types.KnowledgeBase{
		ID:       fixture.ds.KnowledgeBaseID,
		TenantID: fixture.ds.TenantID,
		Name:     "SQLite delete",
	}
	svc := &knowledgeBaseService{
		repo:        kbRepo,
		asynqClient: kbDeleteTaskEnqueuer{},
		dsRepo:      fixture.dsRepo,
		syncLogRepo: fixture.syncLogRepo,
		dsScheduler: fixture.scheduler,
	}

	err := svc.DeleteKnowledgeBase(
		ctxWithTenantStorage(fixture.ds.TenantID, "local"),
		fixture.ds.KnowledgeBaseID,
	)
	require.NoError(t, err)
	assert.Equal(t, fixture.ds.KnowledgeBaseID, kbRepo.deletedID)

	_, err = fixture.dsRepo.FindByID(context.Background(), fixture.ds.ID)
	require.EqualError(t, err, "data source not found")
	var deleted types.DataSource
	require.NoError(t, fixture.db.Unscoped().First(&deleted, "id = ?", fixture.ds.ID).Error)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.Equal(t, 0, fixture.scheduler.EntryCount())

	for _, logID := range []string{fixture.pendingLog.ID, fixture.runningLog.ID} {
		log, err := fixture.syncLogRepo.FindByID(context.Background(), logID)
		require.NoError(t, err)
		assert.Equal(t, types.SyncLogStatusCanceled, log.Status)
	}
}
