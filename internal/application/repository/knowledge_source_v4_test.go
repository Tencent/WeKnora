package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSourceV4ManualDraftClaimRequiresManualAndCurrentVersion(t *testing.T) {
	for _, manual := range []bool{true, false} {
		db := setupKnowledgeTestDB(t)
		repo := NewKnowledgeRepository(db)
		ctx := context.Background()
		id := insertKnowledgeInFolder(t, db, 1, "kb", "", "doc.md")
		kind := "file"
		if manual {
			kind = types.KnowledgeTypeManual
		}
		require.NoError(t, db.Exec(
			"UPDATE knowledges SET type = ?, parse_status = ?, updated_at = ? WHERE id = ?",
			kind, types.ManualKnowledgeStatusDraft, time.Now().UTC(), id,
		).Error)
		before, err := repo.GetKnowledgeByID(ctx, 1, id)
		require.NoError(t, err)
		claim, err := repo.ClaimKnowledgeForReparse(ctx, before, false)
		if !manual {
			require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, types.ParseStatusProcessing, claim.ParseStatus)
		_, err = repo.ClaimKnowledgeForReparse(ctx, before, false)
		require.ErrorIs(t, err, ErrKnowledgeFolderChanged, "a successful draft claim cannot be replayed")
	}
}

func TestSourceV4CompensationFollowsClaimOwnershipAcrossRename(t *testing.T) {
	for _, change := range []string{"rename", "cancel", "new_attempt", "new_source", "legacy_timestamp"} {
		t.Run(change, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			repo := NewKnowledgeRepository(db)
			ctx := context.Background()
			id := insertKnowledgeInFolder(t, db, 1, "kb", "old/sub", "a.md")
			require.NoError(t, db.Exec(
				"UPDATE knowledges SET file_path = ?, file_hash = ?, updated_at = ? WHERE id = ?",
				"replacement-A", "hash-A", time.Now().UTC(), id,
			).Error)
			before, err := repo.GetKnowledgeByID(ctx, 1, id)
			require.NoError(t, err)
			var failed *types.Knowledge
			if change == "legacy_timestamp" {
				require.NoError(t, db.Exec("UPDATE knowledges SET parse_status = ? WHERE id = ?",
					types.ParseStatusFailed, id).Error)
				failed, err = repo.GetKnowledgeByID(ctx, 1, id)
				require.NoError(t, err)
			} else {
				claim, err := repo.ClaimKnowledgeForReparse(ctx, before, false)
				require.NoError(t, err)
				failed, err = repo.FailKnowledgeReparseClaim(ctx, claim, "queue failed")
				require.NoError(t, err)
			}
			_, err = repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "old", "new")
			require.NoError(t, err)
			switch change {
			case "cancel":
				require.NoError(t, db.Exec("UPDATE knowledges SET parse_status = ? WHERE id = ?",
					types.ParseStatusCancelled, id).Error)
			case "new_attempt":
				fresh, err := repo.GetKnowledgeByID(ctx, 1, id)
				require.NoError(t, err)
				claim, err := repo.ClaimKnowledgeForReparse(ctx, fresh, false)
				require.NoError(t, err)
				_, err = repo.FailKnowledgeReparseClaim(ctx, claim, "later failed")
				require.NoError(t, err)
			case "new_source":
				require.NoError(t, db.Exec("UPDATE knowledges SET file_path = ?, file_hash = ? WHERE id = ?",
					"replacement-B", "hash-B", id).Error)
			}
			restored, err := repo.UpdateKnowledgeSourceIfCurrent(ctx, failed, map[string]interface{}{
				"file_path": "original", "file_hash": "original-hash", "parse_status": types.ParseStatusFailed,
			})
			if change == "rename" {
				require.NoError(t, err)
				require.Equal(t, "original", restored.FilePath)
				require.Equal(t, "new/sub", restored.FolderPath)
			} else {
				require.ErrorIs(t, err, ErrKnowledgeFolderChanged)
			}
		})
	}
}
