package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOutlineFileIdentityDeduplication(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	kb, id := uuid.NewString(), uuid.NewString()
	require.NoError(t, db.Exec(`INSERT INTO knowledges
		(id, tenant_id, knowledge_base_id, type, title, parse_status, file_hash, file_type, metadata)
		VALUES (?, 1, ?, 'file', 'same', 'completed', 'hash', 'md', ?)`,
		id, kb, `{"datasource_id":"ds-a","external_id":"a"}`).Error)
	for _, tc := range []struct {
		ds, external string
		duplicate    bool
	}{
		{"ds-a", "a", true}, {"ds-a", "b", false}, {"ds-b", "a", false}, {"", "", true},
	} {
		t.Run(fmt.Sprintf("%s/%s", tc.ds, tc.external), func(t *testing.T) {
			found, _, err := repo.CheckKnowledgeExists(ctx, 1, kb, &types.KnowledgeCheckParams{
				Type: "file", FileHash: "hash", FileType: "md", DataSourceID: tc.ds, ExternalID: tc.external,
			})
			require.NoError(t, err)
			require.Equal(t, tc.duplicate, found)
		})
	}
	_, _, err := repo.CheckKnowledgeExists(ctx, 1, kb, &types.KnowledgeCheckParams{DataSourceID: "ds-a"})
	require.Error(t, err)
}

func TestOutlinePauseDoesNotLoseCheckpoint(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	repo := NewDataSourceRepository(db)
	ctx := context.Background()
	ds := &types.DataSource{ID: "outline-pause", TenantID: 1, KnowledgeBaseID: "kb", Name: "Outline",
		Type: types.ConnectorTypeOutline, Status: types.DataSourceStatusActive}
	require.NoError(t, repo.Create(ctx, ds))
	require.NoError(t, repo.(*DataSourceRepository).Pause(ctx, ds.ID))
	ds.LastSyncCursor = types.JSON(`{"checkpoint":1}`)
	require.NoError(t, repo.UpdateSyncState(ctx, ds))
	stored, err := repo.FindByID(ctx, ds.ID)
	require.NoError(t, err)
	require.Equal(t, types.DataSourceStatusPaused, stored.Status)
	require.JSONEq(t, `{"checkpoint":1}`, stored.LastSyncCursor.ToString())
}

func TestOutlineSQLiteExternalIDIndexMigration(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	up, err := os.ReadFile("../../../migrations/sqlite/000014_knowledge_external_id_index.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up)).Error)
	var rows []struct{ Detail string }
	require.NoError(t, db.Raw(`EXPLAIN QUERY PLAN SELECT id FROM knowledges
		WHERE knowledge_base_id = ? AND metadata->>'external_id' = ? AND deleted_at IS NULL`, "kb", "a").Scan(&rows).Error)
	found := false
	for _, row := range rows {
		found = found || strings.Contains(row.Detail, "idx_knowledges_kb_metadata_external_id")
	}
	require.True(t, found, "identity query should use the shipped expression index")
	down, err := os.ReadFile("../../../migrations/sqlite/000014_knowledge_external_id_index.down.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(down)).Error)
}
