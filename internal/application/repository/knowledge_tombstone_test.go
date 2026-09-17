package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindTombstonedByDataSourceExternalID verifies the tombstone lookup used
// by sync to keep manually deleted items deleted. Only rows that are (a)
// soft-deleted and (b) owned by the exact (tenant, knowledge base, data
// source, external_id) match. A tombstone is persistent (no retention window)
// and the most recent deletion wins.
func TestFindTombstonedByDataSourceExternalID(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 52
	kbID := uuid.New().String()
	dsID := uuid.New().String()

	insertRow := func(tid uint64, kb, ds, extID, deletedAt string) string {
		id := uuid.New().String()
		metadata := fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, ds, extID)
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges
			  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
			VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?, ?)
		`, id, tid, kb, extID, metadata, deletedAt).Error)
		return id
	}

	now := time.Now().UTC()
	// Tombstone deleted recently, must match.
	recentID := insertRow(tenantID, kbID, dsID, "file:gone", now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
	// Tombstone deleted long ago, still matches (persistent).
	_ = insertRow(tenantID, kbID, dsID, "file:old", now.Add(-400*24*time.Hour).Format("2006-01-02 15:04:05"))
	// Same external_id under a different data source, must NOT match.
	_ = insertRow(tenantID, kbID, uuid.New().String(), "file:gone",
		now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
	// Same external_id in a different KB, must NOT match.
	_ = insertRow(tenantID, uuid.New().String(), dsID, "file:gone",
		now.Add(-24*time.Hour).Format("2006-01-02 15:04:05"))
	// Live (non-deleted) row, must NOT match.
	liveID := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata)
		VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?)
	`, liveID, tenantID, kbID, "file:live",
		fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, dsID, "file:live")).Error)

	tomb, err := repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, "file:gone")
	require.NoError(t, err)
	require.NotNil(t, tomb)
	assert.Equal(t, recentID, tomb.ID)

	tomb, err = repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, "file:old")
	require.NoError(t, err)
	require.NotNil(t, tomb, "a tombstone is persistent, an old deletion still suppresses re-sync")

	tomb, err = repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, "file:live")
	require.NoError(t, err)
	assert.Nil(t, tomb, "live rows are not tombstones")

	tomb, err = repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, "file:never-existed")
	require.NoError(t, err)
	assert.Nil(t, tomb)
}

// TestFindTombstonedByDataSourceExternalID_MostRecentWins verifies that when a
// row was deleted repeatedly, the latest deletion is the one honored.
func TestFindTombstonedByDataSourceExternalID_MostRecentWins(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 53
	kbID := uuid.New().String()
	dsID := uuid.New().String()
	metadata := fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, dsID, "file:repeated")

	older := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
		VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?, ?)
	`, older, tenantID, kbID, "file:repeated", metadata,
		time.Now().UTC().Add(-10*24*time.Hour).Format("2006-01-02 15:04:05")).Error)

	newer := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
		VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?, ?)
	`, newer, tenantID, kbID, "file:repeated", metadata,
		time.Now().UTC().Add(-2*time.Hour).Format("2006-01-02 15:04:05")).Error)

	tomb, err := repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, "file:repeated")
	require.NoError(t, err)
	require.NotNil(t, tomb)
	assert.Equal(t, newer, tomb.ID, "the most recent deletion must win")
}

// TestHardDeleteKnowledgeRemovesRowAndTombstone verifies that hard deletion
// physically removes the row: neither the live lookup nor the tombstone
// lookup sees it afterwards.
func TestHardDeleteKnowledgeRemovesRowAndTombstone(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 54
	kbID := uuid.New().String()
	dsID := uuid.New().String()
	extID := "file:hard-deleted"

	id := uuid.New().String()
	metadata := fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, dsID, extID)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
		VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?, ?)
	`, id, tenantID, kbID, extID, metadata,
		time.Now().UTC().Add(-24*time.Hour).Format("2006-01-02 15:04:05")).Error)

	tomb, err := repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, extID)
	require.NoError(t, err)
	require.NotNil(t, tomb)

	require.NoError(t, repo.HardDeleteKnowledge(ctx, tenantID, id))

	tomb, err = repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, extID)
	require.NoError(t, err)
	assert.Nil(t, tomb, "a hard-deleted row must not remain a tombstone")
}

// TestHardDeleteKnowledgeList verifies the batch hard delete on the subtree
// sweep path.
func TestHardDeleteKnowledgeList(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 55
	kbID := uuid.New().String()
	dsID := uuid.New().String()

	ids := make([]string, 0, 2)
	for _, ext := range []string{"doc:p#file:a", "doc:p#file:b"} {
		id := uuid.New().String()
		ids = append(ids, id)
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges
			  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
			VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?, ?)
		`, id, tenantID, kbID, ext, fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, dsID, ext),
			time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")).Error)
	}

	require.NoError(t, repo.HardDeleteKnowledgeList(ctx, tenantID, ids))
	for _, ext := range []string{"doc:p#file:a", "doc:p#file:b"} {
		tomb, err := repo.FindTombstonedByDataSourceExternalID(ctx, tenantID, kbID, dsID, ext)
		require.NoError(t, err)
		assert.Nil(t, tomb, "batch hard delete must clear tombstones for every id")
	}
}
