package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReplaceKnowledgePreservesFolderAndParticipatesInLaterRename(t *testing.T) {
	db, repo, before, after := replacementFixture(t)
	ctx := context.Background()
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}))
	require.NoError(t, db.Exec(
		`CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME)`,
	).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id, tenant_id) VALUES ('kb-1', 7)`).Error)
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "kb-1", "local"))
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "kb-1", "local/empty"))
	require.NoError(t, repo.UpdateKnowledgeColumn(ctx, before.ID, "folder_path", "local"))
	before, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	after.FolderPath = "upstream/renamed"
	require.NoError(t, repo.ReplaceKnowledge(ctx, before, after, nil))
	stored, err := repo.GetKnowledgeByID(ctx, 7, after.ID)
	require.NoError(t, err)
	require.Equal(t, "local", stored.FolderPath)

	moved, err := repo.RenameKnowledgeFolderPath(ctx, 7, "kb-1", "local", "moved")
	require.NoError(t, err)
	require.EqualValues(t, 1, moved)
	stored, err = repo.GetKnowledgeByID(ctx, 7, after.ID)
	require.NoError(t, err)
	require.Equal(t, "moved", stored.FolderPath)
	var paths []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Order("path").Pluck("path", &paths).Error)
	require.Equal(t, []string{"moved", "moved/empty"}, paths)
}

func TestReplaceKnowledgeRejectsPreRenameSnapshotWithoutRetiringDocument(t *testing.T) {
	db, repo, before, after := replacementFixture(t)
	ctx := context.Background()
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}))
	require.NoError(t, db.Exec(
		`CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME)`,
	).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id, tenant_id) VALUES ('kb-1', 7)`).Error)
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "kb-1", "local"))
	require.NoError(t, repo.UpdateKnowledgeColumn(ctx, before.ID, "folder_path", "local"))
	before, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	moved, err := repo.RenameKnowledgeFolderPath(ctx, 7, "kb-1", "local", "moved")
	require.NoError(t, err)
	require.EqualValues(t, 1, moved)
	require.ErrorContains(t, repo.ReplaceKnowledge(ctx, before, after, nil), "knowledge changed during replacement")
	current, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, "moved", current.FolderPath)
	require.Equal(t, before.FilePath, current.FilePath)
	var count int64
	require.NoError(t, db.Unscoped().Model(&types.Knowledge{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, repo.ReplaceKnowledge(ctx, current, after, nil))
	current, err = repo.GetKnowledgeByID(ctx, 7, after.ID)
	require.NoError(t, err)
	require.Equal(t, "moved", current.FolderPath)
}
