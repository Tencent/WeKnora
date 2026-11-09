package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A background job may load before a folder move and save much later. Only
// explicit folder operations own folder_path; unrelated saves must preserve it.
func TestKnowledgeFolderV2StaleSavePreservesMove(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			// The legacy repository fixture omits this post-131 column.
			require.NoError(t, db.Exec("ALTER TABLE knowledges ADD COLUMN custom_metadata TEXT").Error)
			repo := NewKnowledgeRepository(db)
			ctx := context.Background()
			id := insertKnowledgeInFolder(t, db, 1, "kb", "old/child", "readme.md")
			loaded, err := repo.GetKnowledgeByID(ctx, 1, id)
			require.NoError(t, err)
			require.Equal(t, "old/child", loaded.FolderPath)
			_, err = repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "old", "renamed")
			require.NoError(t, err)
			loaded.Title = "background title update"
			loaded.ErrorMessage = "background status update"
			if batch {
				err = repo.UpdateKnowledgeBatch(ctx, []*types.Knowledge{loaded})
			} else {
				err = repo.UpdateKnowledge(ctx, loaded)
			}
			require.NoError(t, err)
			current, err := repo.GetKnowledgeByID(ctx, 1, id)
			require.NoError(t, err)
			require.Equal(t, "renamed/child", current.FolderPath)
			require.Equal(t, loaded.Title, current.Title)
			require.Equal(t, loaded.ErrorMessage, current.ErrorMessage)
			// The dedicated explicit move continues to change folders.
			n, err := repo.UpdateKnowledgeFolderPath(
				types.WithKnowledgeFolderAnchor(ctx, ""), 1, "kb", []string{id}, "manual",
			)
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			require.Equal(t, "manual", folderPathOf(t, db, id))
		})
	}
}
