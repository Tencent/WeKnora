package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFolderWriteV3SnapshotRejectsRenamedOrRecreatedAnchor(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		db := setupKnowledgeTestDB(t)
		require.NoError(t, db.Exec("ALTER TABLE knowledges ADD COLUMN custom_metadata TEXT").Error)
		r := NewKnowledgeRepository(db)
		ctx := context.Background()
		require.NoError(t, r.CreateKnowledgeFolder(ctx, 1, "kb", "selected"))
		guard, err := r.SnapshotKnowledgeFolderPath(
			types.WithKnowledgeFolderAnchor(ctx, "selected"), 1, "kb", "selected/import/new",
		)
		require.NoError(t, err)
		_, err = r.RenameKnowledgeFolderPath(ctx, 1, "kb", "selected", "renamed")
		require.NoError(t, err)
		if recreate {
			require.NoError(t, r.CreateKnowledgeFolder(ctx, 1, "kb", "selected"))
		}
		k := &types.Knowledge{
			ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", Type: "file", FolderPath: guard.Path,
		}
		require.ErrorIs(t, r.CreateKnowledgeWithFolderSnapshot(ctx, k, guard), ErrKnowledgeFolderChanged)
		var count int64
		require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", k.ID).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestFolderWriteV3LegacyAncestorAndRootImport(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec("ALTER TABLE knowledges ADD COLUMN custom_metadata TEXT").Error)
	r := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "kb", "old/derived", "doc.md")
	guard, err := r.SnapshotKnowledgeFolderPath(ctx, 1, "kb", "old/new/levels")
	require.NoError(t, err)
	require.Equal(t, "old", guard.Anchor)
	_, err = r.UpdateKnowledgeFolderPath(types.WithKnowledgeFolderAnchor(ctx, ""), 1, "kb", []string{id}, "moved")
	require.NoError(t, err)
	k := &types.Knowledge{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", Type: "file", FolderPath: guard.Path,
	}
	require.ErrorIs(t, r.CreateKnowledgeWithFolderSnapshot(ctx, k, guard), ErrKnowledgeFolderChanged)
	guard, err = r.SnapshotKnowledgeFolderPath(types.WithKnowledgeFolderAnchor(ctx, ""), 1, "kb", "new/import/levels")
	require.NoError(t, err)
	k.FolderPath = guard.Path
	require.NoError(t, r.CreateKnowledgeWithFolderSnapshot(ctx, k, guard))
	require.Equal(t, k.FolderPath, folderPathOf(t, db, k.ID))
	_, err = r.SnapshotKnowledgeFolderPath(types.WithKnowledgeFolderAnchor(ctx, "missing"), 1, "kb", "missing/sub")
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
	_, err = r.SnapshotKnowledgeFolderPath(types.WithKnowledgeFolderAnchor(ctx, "new"), 1, "kb", "outside")
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalidPath)
}

func TestFolderWriteV3MoveRejectsStaleBatchWithoutPartialWrites(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	r := NewKnowledgeRepository(db)
	ctx := context.Background()
	a := insertKnowledgeInFolder(t, db, 1, "kb", "old", "a.md")
	b := insertKnowledgeInFolder(t, db, 1, "other", "old", "b.md")
	require.NoError(t, r.CreateKnowledgeFolder(ctx, 1, "kb", "target"))
	n, err := r.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{a, b}, "target")
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
	require.Zero(t, n)
	require.Equal(t, "old", folderPathOf(t, db, a))
	require.Equal(t, "old", folderPathOf(t, db, b))
	_, err = r.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{a}, "missing")
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
	n, err = r.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{a, a}, "target")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func TestFolderWriteV3GenericSaveRejectsChangedSourceAndBinding(t *testing.T) {
	for _, batch := range []bool{false, true} {
		db := setupKnowledgeTestDB(t)
		r := NewKnowledgeRepository(db)
		ctx := context.Background()
		id := insertKnowledgeInFolder(t, db, 1, "kb", "old", "old.pdf")
		require.NoError(t, db.Exec("UPDATE knowledges SET file_path = ? WHERE id = ?", "old.pdf", id).Error)
		loaded, err := r.GetKnowledgeByID(ctx, 1, id)
		require.NoError(t, err)
		require.NoError(t, db.Exec(
			"UPDATE knowledges SET file_path = ?, file_name = ?, knowledge_base_id = ? WHERE id = ?",
			"new.md", "new.md", "other", id,
		).Error)
		loaded.Title = "stale"
		if batch {
			err = r.UpdateKnowledgeBatch(ctx, []*types.Knowledge{loaded})
		} else {
			err = r.UpdateKnowledge(ctx, loaded)
		}
		require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
		current, err := r.GetKnowledgeByID(ctx, 1, id)
		require.NoError(t, err)
		require.Equal(t, "new.md", current.FilePath)
		require.Equal(t, "new.md", current.FileName)
		require.Equal(t, "other", current.KnowledgeBaseID)
		require.NotEqual(t, "stale", current.Title)
		// Legitimate same-source edits (manual names or recomputed hashes) still persist.
		current.FileHash = "recomputed"
		current.Title = "current"
		current.FileName = "renamed.md"
		require.NoError(t, r.UpdateKnowledge(ctx, current))
		reloaded, err := r.GetKnowledgeByID(ctx, 1, id)
		require.NoError(t, err)
		require.Equal(t, "recomputed", reloaded.FileHash)
		require.Equal(t, "renamed.md", reloaded.FileName)
		// A disappeared row is not recreated through a Save upsert fallback.
		require.NoError(t, db.Exec("DELETE FROM knowledges WHERE id = ?", id).Error)
		require.ErrorIs(t, r.UpdateKnowledge(ctx, current), ErrKnowledgeFolderChanged)
	}
}

func TestFolderWriteV3SaveCannotEraseActiveMoveAndBatchRollsBack(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	r := NewKnowledgeRepository(db)
	ctx := context.Background()
	a := insertKnowledgeInFolder(t, db, 1, "kb", "old", "a.md")
	b := insertKnowledgeInFolder(t, db, 1, "kb", "old", "b.md")
	first, err := r.GetKnowledgeByID(ctx, 1, a)
	require.NoError(t, err)
	second, err := r.GetKnowledgeByID(ctx, 1, b)
	require.NoError(t, err)
	first.Title, second.Title = "stale-a", "stale-b"
	moving := `{"_knowledge_transfer":{"operation":"move","phase":"moving"}}`
	require.NoError(t, db.Exec("UPDATE knowledges SET metadata = ? WHERE id = ?", moving, b).Error)
	require.Error(t, r.UpdateKnowledgeBatch(ctx, []*types.Knowledge{first, second}))
	current, err := r.GetKnowledgeByID(ctx, 1, a)
	require.NoError(t, err)
	require.Equal(t, "a.md", current.Title, "a failed batch must roll back its other rows")
	require.NoError(t, r.CreateKnowledgeFolder(ctx, 1, "kb", "target"))
	_, err = r.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{a, b}, "target")
	require.Error(t, err)
	_, err = r.RenameKnowledgeFolderPath(ctx, 1, "kb", "old", "renamed")
	require.Error(t, err)
	require.Equal(t, "old", folderPathOf(t, db, a))
	require.Equal(t, "old", folderPathOf(t, db, b))
}
