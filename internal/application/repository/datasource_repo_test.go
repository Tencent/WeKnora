package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
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

func TestDataSourceRepositoryConditionalSyncStatePreservesPauseResume(t *testing.T) {
	tests := []struct {
		name           string
		startStatus    string
		operatorStatus string
		outcomeStatus  string
		wantStatus     string
	}{
		{
			name:           "pause during active run",
			startStatus:    types.DataSourceStatusActive,
			operatorStatus: types.DataSourceStatusPaused,
			outcomeStatus:  types.DataSourceStatusError,
			wantStatus:     types.DataSourceStatusPaused,
		},
		{
			name:           "resume during paused run",
			startStatus:    types.DataSourceStatusPaused,
			operatorStatus: types.DataSourceStatusActive,
			outcomeStatus:  types.DataSourceStatusPaused,
			wantStatus:     types.DataSourceStatusActive,
		},
		{
			name:           "successful retry clears unchanged error status",
			startStatus:    types.DataSourceStatusError,
			operatorStatus: types.DataSourceStatusError,
			outcomeStatus:  types.DataSourceStatusActive,
			wantStatus:     types.DataSourceStatusActive,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupDataSourceRepoTestDB(t)
			repo := NewDataSourceRepository(db)
			conditionalRepo, ok := repo.(*DataSourceRepository)
			require.True(t, ok)
			ds := &types.DataSource{
				ID:              "ds-status-race",
				TenantID:        1,
				KnowledgeBaseID: "kb-status-race",
				Name:            "DingTalk",
				Type:            types.ConnectorTypeDingTalk,
				Status:          tt.startStatus,
			}
			require.NoError(t, repo.Create(context.Background(), ds))
			require.NoError(t, db.Model(&types.DataSource{}).
				Where("id = ?", ds.ID).
				Update("status", tt.operatorStatus).Error)

			ds.Status = tt.outcomeStatus
			ds.LastSyncResult = types.JSON(`{"total":1}`)
			require.NoError(t, conditionalRepo.UpdateSyncStateIfStatusUnchanged(
				context.Background(),
				ds,
				tt.startStatus,
			))

			var stored types.DataSource
			require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
			assert.Equal(t, tt.wantStatus, stored.Status)
			assert.JSONEq(t, `{"total":1}`, string(stored.LastSyncResult))
		})
	}
}

func TestDataSourceRepositoryUpdatePersistsIntentionalZeroValues(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	ds := &types.DataSource{
		ID:                   "ds-zero-values",
		TenantID:             1,
		KnowledgeBaseID:      "kb-1",
		Name:                 "DingTalk",
		Type:                 types.ConnectorTypeDingTalk,
		Status:               types.DataSourceStatusActive,
		SyncSchedule:         "0 0 */6 * * *",
		SyncMode:             types.SyncModeIncremental,
		ConflictStrategy:     types.ConflictStrategyOverwrite,
		SyncDeletions:        true,
		SyncLogRetentionDays: 30,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	ds.SyncSchedule = ""
	ds.SyncDeletions = false
	ds.ErrorMessage = ""
	require.NoError(t, repo.Update(context.Background(), ds))

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.Empty(t, stored.SyncSchedule)
	assert.False(t, stored.SyncDeletions)
	assert.Equal(t, 30, stored.SyncLogRetentionDays)
}

func TestDataSourceRepositoryStatusUpdateDoesNotRestoreStaleConfiguration(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	ds := &types.DataSource{
		ID:           "ds-status-only",
		TenantID:     1,
		Name:         "DingTalk",
		Type:         types.ConnectorTypeDingTalk,
		Status:       types.DataSourceStatusActive,
		Config:       types.JSON(`{"type":"dingtalk","resource_ids":["old"]}`),
		SyncSchedule: "0 0 */6 * * *",
	}
	require.NoError(t, repo.Create(context.Background(), ds))
	stalePauseSnapshot := *ds

	reconfigured := *ds
	reconfigured.Config = types.JSON(`{"type":"dingtalk","resource_ids":["new"]}`)
	reconfigured.SyncSchedule = "0 0 */2 * * *"
	require.NoError(t, repo.Update(context.Background(), &reconfigured))
	require.NoError(t, repo.UpdateStatus(
		context.Background(),
		stalePauseSnapshot.ID,
		types.DataSourceStatusPaused,
	))

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.JSONEq(t, string(reconfigured.Config), string(stored.Config))
	assert.Equal(t, reconfigured.SyncSchedule, stored.SyncSchedule)
	assert.Equal(t, types.DataSourceStatusPaused, stored.Status)
}

func TestDataSourceRepositoryConfigUpdatePreservesConcurrentPause(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	ds := &types.DataSource{
		ID:       "ds-config-preserves-pause",
		TenantID: 1,
		Name:     "DingTalk",
		Type:     types.ConnectorTypeDingTalk,
		Status:   types.DataSourceStatusActive,
		Config:   types.JSON(`{"type":"dingtalk","resource_ids":["old"]}`),
	}
	require.NoError(t, repo.Create(context.Background(), ds))
	staleCandidate := *ds
	staleCandidate.Config = types.JSON(`{"type":"dingtalk","resource_ids":["new"]}`)

	require.NoError(t, repo.UpdateStatus(context.Background(), ds.ID, types.DataSourceStatusPaused))
	updated, err := repo.UpdateIfNoRunningSync(context.Background(), &staleCandidate)
	require.NoError(t, err)
	require.True(t, updated)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.JSONEq(t, string(staleCandidate.Config), string(stored.Config))
	assert.Equal(t, types.DataSourceStatusPaused, stored.Status)
}

func TestDataSourceRepositoryStaleValidationCannotLabelNewConfig(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	oldConfig := types.JSON(`{"type":"dingtalk","credentials":{"client_id":"old"}}`)
	ds := &types.DataSource{
		ID:           "ds-validation-generation",
		TenantID:     1,
		Name:         "DingTalk",
		Type:         types.ConnectorTypeDingTalk,
		Status:       types.DataSourceStatusActive,
		ErrorMessage: "",
		Config:       oldConfig,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	reconfigured := *ds
	reconfigured.Config = types.JSON(`{"type":"dingtalk","credentials":{"client_id":"new"}}`)
	require.NoError(t, repo.Update(context.Background(), &reconfigured))
	updated, err := repo.UpdateValidationStateIfConfigUnchanged(
		context.Background(),
		ds.ID,
		oldConfig,
		types.DataSourceStatusActive,
		types.DataSourceStatusError,
		"old credentials rejected",
	)
	require.NoError(t, err)
	assert.False(t, updated)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.JSONEq(t, string(reconfigured.Config), string(stored.Config))
	assert.Equal(t, types.DataSourceStatusActive, stored.Status)
	assert.Empty(t, stored.ErrorMessage)
}

func TestDataSourceRepositoryStaleValidationCannotUndoPause(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	config := types.JSON(`{"type":"dingtalk","credentials":{"client_id":"same"}}`)
	ds := &types.DataSource{
		ID:       "ds-validation-pause",
		TenantID: 1,
		Name:     "DingTalk",
		Type:     types.ConnectorTypeDingTalk,
		Status:   types.DataSourceStatusActive,
		Config:   config,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	require.NoError(t, repo.UpdateStatus(
		context.Background(),
		ds.ID,
		types.DataSourceStatusPaused,
	))
	updated, err := repo.UpdateValidationStateIfConfigUnchanged(
		context.Background(),
		ds.ID,
		config,
		types.DataSourceStatusActive,
		types.DataSourceStatusError,
		"validation started before pause",
	)
	require.NoError(t, err)
	assert.False(t, updated)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.Equal(t, types.DataSourceStatusPaused, stored.Status)
	assert.Empty(t, stored.ErrorMessage)
}

func TestDataSourceRepositoryPausedValidationFailureStaysPaused(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	config := types.JSON(`{"type":"dingtalk","credentials":{"client_id":"same"}}`)
	ds := &types.DataSource{
		ID:       "ds-validation-already-paused",
		TenantID: 1,
		Name:     "DingTalk",
		Type:     types.ConnectorTypeDingTalk,
		Status:   types.DataSourceStatusPaused,
		Config:   config,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	updated, err := repo.UpdateValidationStateIfConfigUnchanged(
		context.Background(),
		ds.ID,
		config,
		types.DataSourceStatusPaused,
		types.DataSourceStatusPaused,
		"credentials rejected",
	)
	require.NoError(t, err)
	assert.True(t, updated)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.Equal(t, types.DataSourceStatusPaused, stored.Status)
	assert.Equal(t, "credentials rejected", stored.ErrorMessage)
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

func TestSyncLogRepositoryCreateRejectsOverlappingRun(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewSyncLogRepository(db)
	first := &types.SyncLog{
		ID:           "log-overlap-first",
		DataSourceID: "ds-overlap",
		TenantID:     1,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	require.NoError(t, repo.Create(context.Background(), first))

	second := &types.SyncLog{
		ID:           "log-overlap-second",
		DataSourceID: first.DataSourceID,
		TenantID:     first.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	require.ErrorIs(t, repo.Create(context.Background(), second), datasource.ErrSyncInProgress)

	var count int64
	require.NoError(t, db.Model(&types.SyncLog{}).
		Where("data_source_id = ?", first.DataSourceID).
		Where("status = ?", types.SyncLogStatusRunning).
		Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestDataSourceRepositoryUpdateIfNoRunningSync(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db).(*DataSourceRepository)
	syncLogs := NewSyncLogRepository(db)
	ds := &types.DataSource{
		ID:              "ds-fenced-update",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Old name",
		Type:            types.ConnectorTypeDingTalk,
		Status:          types.DataSourceStatusActive,
		Config:          types.JSON(`{"type":"dingtalk","resource_ids":["old"]}`),
		SyncSchedule:    "0 0 */6 * * *",
		SyncDeletions:   true,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	candidate := *ds
	candidate.Name = "First update"
	candidate.Config = types.JSON(`{"type":"dingtalk","resource_ids":["first"]}`)
	candidate.SyncSchedule = ""
	candidate.SyncDeletions = false
	updated, err := repo.UpdateIfNoRunningSync(context.Background(), &candidate)
	require.NoError(t, err)
	require.True(t, updated)

	require.NoError(t, syncLogs.Create(context.Background(), &types.SyncLog{
		ID:           "log-running-fence",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}))
	blockedCandidate := candidate
	blockedCandidate.Name = "Must not persist"
	blockedCandidate.Config = types.JSON(`{"type":"dingtalk","resource_ids":["blocked"]}`)
	updated, err = repo.UpdateIfNoRunningSync(context.Background(), &blockedCandidate)
	require.NoError(t, err)
	require.False(t, updated)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	assert.Equal(t, "First update", stored.Name)
	assert.JSONEq(t, string(candidate.Config), string(stored.Config))
	assert.Empty(t, stored.SyncSchedule)
	assert.False(t, stored.SyncDeletions)
}
