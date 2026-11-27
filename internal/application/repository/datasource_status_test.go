package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// UpdateStatus is the pause/resume write path. It must not touch the columns a
// sync worker updates while fetching: a full-row write from the caller's older
// snapshot would roll the cursor and completion state back.
func TestUpdateStatusLeavesSyncProgressIntact(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}))

	lastSync := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	ds := &types.DataSource{
		ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Name: "sync", Type: "rss", Status: types.DataSourceStatusActive,
		LastSyncAt:     &lastSync,
		LastSyncCursor: types.JSON(`{"offset":7}`),
		LastSyncResult: types.JSON(`{"created":3}`),
		ErrorMessage:   "previous warning",
	}
	require.NoError(t, db.Create(ds).Error)
	repo := NewDataSourceRepository(db)

	require.NoError(t, repo.UpdateStatus(context.Background(), "ds-1", types.DataSourceStatusPaused))

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", "ds-1").Error)
	require.Equal(t, types.DataSourceStatusPaused, stored.Status)
	require.NotNil(t, stored.LastSyncAt)
	require.True(t, stored.LastSyncAt.Equal(lastSync), "last_sync_at must not be rolled back")
	require.JSONEq(t, `{"offset":7}`, string(stored.LastSyncCursor))
	require.JSONEq(t, `{"created":3}`, string(stored.LastSyncResult))
	require.Equal(t, "previous warning", stored.ErrorMessage)
}
