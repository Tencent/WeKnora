//go:build mysqlintegration

package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMySQLDeleteWikiFolder(t *testing.T) {
	db := testutil.MySQL(t)
	r := &wikiPageRepository{db: db}
	ctx := context.Background()
	for _, id := range []string{"empty", "parent", "pages"} {
		require.NoError(t, db.Create(&types.WikiFolder{
			ID: id, TenantID: 1, KnowledgeBaseID: "kb", Name: id, Path: id,
		}).Error)
	}
	require.NoError(t, db.Create(&types.WikiFolder{
		ID: "child", TenantID: 1, KnowledgeBaseID: "kb", ParentID: "parent", Name: "child", Path: "parent/child",
	}).Error)
	require.NoError(t, r.Create(ctx, &types.WikiPage{
		ID: "page", TenantID: 1, KnowledgeBaseID: "kb", FolderID: "pages",
		Slug: "page", Title: "Page", PageType: types.WikiPageTypeSummary, Status: "published",
	}))
	t.Run("empty and scoped", func(t *testing.T) {
		require.ErrorIs(t, r.DeleteFolder(ctx, "other-kb", "empty"), ErrWikiFolderNotFound)
		require.NoError(t, r.DeleteFolder(ctx, "kb", "empty"))
		var folder types.WikiFolder
		require.NoError(t, db.Unscoped().First(&folder, "id = ?", "empty").Error)
		require.True(t, folder.DeletedAt.Valid)
		require.ErrorIs(t, r.DeleteFolder(ctx, "kb", "empty"), ErrWikiFolderNotFound)
	})
	t.Run("child prevents deletion", func(t *testing.T) {
		require.ErrorIs(t, r.DeleteFolder(ctx, "kb", "parent"), ErrWikiFolderNotEmpty)
		require.NoError(t, r.DeleteFolder(ctx, "kb", "child"))
		require.NoError(t, r.DeleteFolder(ctx, "kb", "parent"))
	})
	t.Run("page prevents deletion", func(t *testing.T) {
		require.ErrorIs(t, r.DeleteFolder(ctx, "kb", "pages"), ErrWikiFolderNotEmpty)
		require.NoError(t, db.Where("id = ?", "page").Delete(&types.WikiPage{}).Error)
		require.NoError(t, r.DeleteFolder(ctx, "kb", "pages"))
	})
}
