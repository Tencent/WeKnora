package repository

import (
	"context"
	"encoding/json"
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

func TestDataSourceRepositoryInvalidateCursorItemRemovesFeishuNode(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	cursor := types.JSON(`{
		"last_sync_time":"2026-06-10T00:00:00Z",
		"connector_cursor":{
			"space_node_times":{
				"space-1:root-a":{"node-a":"100","node-b":"200"},
				"space-1:root-b":{"node-a":"300"}
			}
		}
	}`)
	ds := &types.DataSource{
		ID:              "ds-feishu",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Feishu",
		Type:            types.ConnectorTypeFeishu,
		Status:          types.DataSourceStatusActive,
		LastSyncCursor:  cursor,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	require.NoError(t, repo.InvalidateCursorItem(context.Background(), 1, ds.ID, "node-a", "space-1:root-a"))

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(stored.LastSyncCursor, &decoded))
	connectorCursor := decoded["connector_cursor"].(map[string]interface{})
	spaceNodeTimes := connectorCursor["space_node_times"].(map[string]interface{})
	rootA := spaceNodeTimes["space-1:root-a"].(map[string]interface{})
	rootB := spaceNodeTimes["space-1:root-b"].(map[string]interface{})
	assert.NotContains(t, rootA, "node-a")
	assert.Equal(t, "200", rootA["node-b"])
	assert.Equal(t, "300", rootB["node-a"])
}

func TestDataSourceRepositoryInvalidateCursorItemRemovesGenericExternalID(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	cursor := types.JSON(`{
		"last_sync_time":"2026-06-10T00:00:00Z",
		"connector_cursor":{
			"page_edit_times":{
				"page-a":"2026-06-09T00:00:00Z",
				"page-b":"2026-06-09T01:00:00Z"
			}
		}
	}`)
	ds := &types.DataSource{
		ID:              "ds-notion",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Notion",
		Type:            types.ConnectorTypeNotion,
		Status:          types.DataSourceStatusActive,
		LastSyncCursor:  cursor,
	}
	require.NoError(t, repo.Create(context.Background(), ds))

	require.NoError(t, repo.InvalidateCursorItem(context.Background(), 1, ds.ID, "page-a", ""))

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(stored.LastSyncCursor, &decoded))
	connectorCursor := decoded["connector_cursor"].(map[string]interface{})
	pageEditTimes := connectorCursor["page_edit_times"].(map[string]interface{})
	assert.NotContains(t, pageEditTimes, "page-a")
	assert.Contains(t, pageEditTimes, "page-b")
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
