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
	require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.SyncLog{}, &types.DingTalkExportTask{}))
	return db
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

func TestDingTalkExportTaskRepositoryUpsertsAndFindsPendingTask(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDingTalkExportTaskRepository(db)
	ctx := context.Background()

	task := &types.DingTalkExportTask{
		TaskID:           "task-1",
		DataSourceID:     "ds-1",
		SyncLogID:        "log-1",
		TenantID:         7,
		ExternalID:       "ws1:doc1",
		SourceResourceID: "ws1:root",
		WorkspaceID:      "ws1",
		NodeID:           "doc1",
		DentryUUID:       "doc1",
		Title:            "Architecture",
		FileName:         "Architecture.md",
		SourceURL:        "https://dingtalk.example/wiki/doc1",
		Status:           types.DingTalkExportTaskStatusPending,
	}
	require.NoError(t, repo.UpsertPending(ctx, task))

	stored, err := repo.FindByTaskID(ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.DingTalkExportTaskStatusPending, stored.Status)
	assert.Equal(t, "Architecture.md", stored.FileName)
	assert.Equal(t, uint64(7), stored.TenantID)

	task.Title = "Architecture v2"
	task.FileName = "Architecture v2.md"
	require.NoError(t, repo.UpsertPending(ctx, task))

	stored, err = repo.FindByTaskID(ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, "Architecture v2", stored.Title)
	assert.Equal(t, "Architecture v2.md", stored.FileName)
}

func TestDingTalkExportTaskRepositoryMarksTerminalStates(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDingTalkExportTaskRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.UpsertPending(ctx, &types.DingTalkExportTask{
		TaskID:       "task-ok",
		DataSourceID: "ds-1",
		TenantID:     7,
		ExternalID:   "ws1:doc1",
		DentryUUID:   "doc1",
		Title:        "Architecture",
		FileName:     "Architecture.md",
		Status:       types.DingTalkExportTaskStatusPending,
	}))

	require.NoError(t, repo.MarkSucceeded(ctx, "task-ok", "evt-1", "https://example.com/export.md"))
	require.NoError(t, repo.MarkSucceeded(ctx, "task-ok", "evt-1", "https://example.com/export.md"))

	okTask, err := repo.FindByTaskID(ctx, "task-ok")
	require.NoError(t, err)
	assert.Equal(t, types.DingTalkExportTaskStatusSucceeded, okTask.Status)
	assert.Equal(t, "evt-1", okTask.EventID)
	assert.Equal(t, "https://example.com/export.md", okTask.ExportURL)
	require.NotNil(t, okTask.FinishedAt)

	require.NoError(t, repo.UpsertPending(ctx, &types.DingTalkExportTask{
		TaskID:       "task-fail",
		DataSourceID: "ds-1",
		TenantID:     7,
		ExternalID:   "ws1:doc2",
		DentryUUID:   "doc2",
		Title:        "Failure",
		FileName:     "Failure.md",
		Status:       types.DingTalkExportTaskStatusPending,
	}))
	require.NoError(t, repo.MarkFailed(ctx, "task-fail", "evt-2", "52622003", "task initial cp not found"))

	failedTask, err := repo.FindByTaskID(ctx, "task-fail")
	require.NoError(t, err)
	assert.Equal(t, types.DingTalkExportTaskStatusFailed, failedTask.Status)
	assert.Equal(t, "52622003", failedTask.ErrorCode)
	assert.Equal(t, "task initial cp not found", failedTask.ErrorMessage)
	require.NotNil(t, failedTask.FinishedAt)
}
