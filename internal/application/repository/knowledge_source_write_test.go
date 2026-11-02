package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeSourceCASPreservesCurrentFolder(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "kb", "old", "a.md")
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET file_path = 'old/file.md', file_hash = 'old-hash' WHERE id = ?", id,
	).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
	before, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	_, err = repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "old", "renamed")
	require.NoError(t, err)
	committed, err := repo.UpdateKnowledgeSourceIfCurrent(ctx, before, map[string]interface{}{
		"file_path": "new/file.md", "file_hash": "new-hash", "title": "replaced",
	})
	require.NoError(t, err)
	require.Equal(t, "renamed", committed.FolderPath)
	require.Equal(t, "new/file.md", committed.FilePath)
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "later"))
	_, err = repo.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{id}, "later")
	require.NoError(t, err)
	restored, err := repo.UpdateKnowledgeSourceIfCurrent(ctx, committed, map[string]interface{}{
		"file_path": before.FilePath, "file_hash": before.FileHash, "title": "restored",
	})
	require.NoError(t, err)
	require.Equal(t, "later", restored.FolderPath)
	require.Equal(t, before.FilePath, restored.FilePath)
}

func TestKnowledgeSourceCASRejectsStaleScopeAndSource(t *testing.T) {
	for _, change := range []string{"source", "kb", "deleting", "deleted", "moving"} {
		t.Run(change, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			repo := NewKnowledgeRepository(db)
			ctx := context.Background()
			id := insertKnowledgeInFolder(t, db, 1, "kb", "old", "a.md")
			require.NoError(t, db.Exec(
				"UPDATE knowledges SET file_path = 'old/file.md', file_hash = 'old-hash' WHERE id = ?", id,
			).Error)
			require.NoError(t, db.Model(&types.Knowledge{}).
				Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
			before, err := repo.GetKnowledgeByID(ctx, 1, id)
			require.NoError(t, err)
			switch change {
			case "source":
				require.NoError(t, db.Exec(
					"UPDATE knowledges SET file_path = 'later/file.md', file_hash = 'later-hash' WHERE id = ?", id,
				).Error)
			case "kb":
				require.NoError(t, db.Exec("UPDATE knowledges SET knowledge_base_id = 'other' WHERE id = ?", id).Error)
			case "deleting":
				require.NoError(t, db.Exec("UPDATE knowledges SET parse_status = 'deleting' WHERE id = ?", id).Error)
			case "deleted":
				require.NoError(t, repo.DeleteKnowledge(ctx, 1, id))
			case "moving":
				require.NoError(t, db.Exec("UPDATE knowledges SET metadata = ? WHERE id = ?",
					`{"_knowledge_transfer":{"operation":"move","phase":"moving"}}`, id).Error)
			}
			_, err = repo.UpdateKnowledgeSourceIfCurrent(ctx, before, map[string]interface{}{"title": "must not write"})
			require.Error(t, err)
			var title string
			require.NoError(t, db.Raw("SELECT title FROM knowledges WHERE id = ?", id).Scan(&title).Error)
			require.Equal(t, "a.md", title)
		})
	}
}

func TestKnowledgeSourceCASCannotWriteLocationOrIdentity(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	before := &types.Knowledge{ID: "id", TenantID: 1, KnowledgeBaseID: "kb"}
	for _, field := range []string{"folder_path", "FolderPath", "tenant_id", "knowledge_base_id", "id"} {
		_, err := repo.UpdateKnowledgeSourceIfCurrent(
			context.Background(), before, map[string]interface{}{field: "invalid"},
		)
		require.ErrorContains(t, err, "cannot update column")
	}
}

func TestKnowledgeSourceCASAllowsLegacyNullHash(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "kb", "old", "a.md")
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET file_path = 'old/file.md', file_hash = NULL WHERE id = ?", id,
	).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
	before, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	require.Empty(t, before.FileHash)
	current, err := repo.UpdateKnowledgeSourceIfCurrent(ctx, before, map[string]interface{}{
		"file_path": "new/file.md", "file_hash": "new-hash",
	})
	require.NoError(t, err)
	require.Equal(t, "new/file.md", current.FilePath)
}

func TestReparseClaimRejectsTransferredSnapshotBeforeCleanup(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "source", "docs", "a.md")
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET file_path = 'file.md', file_hash = 'hash' WHERE id = ?", id,
	).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
	before, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	after := *before
	after.KnowledgeBaseID = "target"
	require.NoError(t, repo.UpdateKnowledgeForTransfer(ctx, before, &after))
	_, err = repo.ClaimKnowledgeForReparse(ctx, before, false)
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
	current, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	require.Equal(t, "target", current.KnowledgeBaseID)
	require.Equal(t, types.ParseStatusCompleted, current.ParseStatus)
}

func TestReparseClaimSerializesTransferAndCannotReenter(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "source", "docs", "a.md")
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET file_path = 'file.md', file_hash = 'hash' WHERE id = ?", id,
	).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
	before, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	claim, err := repo.ClaimKnowledgeForReparse(ctx, before, false)
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusProcessing, claim.ParseStatus)
	transfer := *before
	transfer.KnowledgeBaseID = "target"
	require.Error(t, repo.UpdateKnowledgeForTransfer(ctx, before, &transfer),
		"stale completed transfer cannot win after cleanup claim")
	_, err = repo.ClaimKnowledgeForReparse(ctx, claim, true)
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged, "fresh processing snapshot cannot start a second cleanup")
	failed, err := repo.FailKnowledgeReparseClaim(ctx, claim, "cleanup failed")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusFailed, failed.ParseStatus)
	secondClaim, err := repo.ClaimKnowledgeForReparse(ctx, failed, false)
	require.NoError(t, err)
	_, err = repo.FailKnowledgeReparseClaim(ctx, claim, "stale error")
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
	_, err = repo.UpdateKnowledgeSourceIfCurrent(ctx, failed, map[string]interface{}{
		"parse_status": types.ParseStatusFailed, "file_path": "old-backup.md",
	})
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged, "old replacement compensation cannot overwrite a later claim")
	current, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	require.Equal(t, secondClaim.UpdatedAt, current.UpdatedAt)
	require.Equal(t, types.ParseStatusProcessing, current.ParseStatus)
}

func TestReparseClaimPendingRequiresReplacementAdmission(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "source", "docs", "a.md")
	require.NoError(t, db.Exec("UPDATE knowledges SET parse_status = 'pending' WHERE id = ?", id).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
	before, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	_, err = repo.ClaimKnowledgeForReparse(ctx, before, false)
	require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
	_, err = repo.ClaimKnowledgeForReparse(ctx, before, true)
	require.NoError(t, err)
}

func TestReparseClaimFailureSurvivesFolderRename(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	id := insertKnowledgeInFolder(t, db, 1, "source", "docs", "a.md")
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error)
	before, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	claim, err := repo.ClaimKnowledgeForReparse(ctx, before, false)
	require.NoError(t, err)
	_, err = repo.RenameKnowledgeFolderPath(ctx, 1, "source", "docs", "renamed")
	require.NoError(t, err)
	failed, err := repo.FailKnowledgeReparseClaim(ctx, claim, "cleanup failed")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusFailed, failed.ParseStatus)
	require.Equal(t, "renamed", failed.FolderPath)
	before.Title = "stale worker update"
	require.ErrorIs(t, repo.UpdateKnowledge(ctx, before), ErrKnowledgeFolderChanged,
		"a stale generic save must not erase a new reparse claim")
}
