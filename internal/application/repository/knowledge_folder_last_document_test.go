package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestExplicitFolderSurvivesLastDocumentMoveWhileDerivedFolderDisappears(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "explicit"))
	explicitDoc := insertKnowledgeInFolder(t, db, 1, "kb", "explicit", "explicit.md")
	derivedDoc := insertKnowledgeInFolder(t, db, 1, "kb", "derived", "derived.md")

	// A fresh repository reads the same tree used when the client refreshes.
	readTree := func() *types.KnowledgeFolderTree {
		t.Helper()
		counts, err := NewKnowledgeRepository(db).ListKnowledgeFolderCounts(ctx, 1, "kb")
		require.NoError(t, err)
		return types.BuildKnowledgeFolderTree(counts)
	}
	before := readTree()
	require.EqualValues(t, 2, before.TotalDocumentCount)
	require.Zero(t, before.RootDocumentCount)
	require.Len(t, before.Folders, 2)
	for _, folder := range before.Folders {
		require.EqualValues(t, 1, folder.DocumentCount)
		require.EqualValues(t, 1, folder.TotalCount)
	}

	// Move the only document out of each source into the KB root.
	affected, err := repo.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{explicitDoc, derivedDoc}, "")
	require.NoError(t, err)
	require.EqualValues(t, 2, affected)
	require.Empty(t, folderPathOf(t, db, explicitDoc))
	require.Empty(t, folderPathOf(t, db, derivedDoc))

	after := readTree()
	require.EqualValues(t, 2, after.TotalDocumentCount, "moving must retain both documents")
	require.EqualValues(t, 2, after.RootDocumentCount)
	require.Len(t, after.Folders, 1, "the empty derived folder keeps its previous disappearance behavior")
	require.Equal(t, "explicit", after.Folders[0].Path)
	require.Zero(t, after.Folders[0].DocumentCount)
	require.Zero(t, after.Folders[0].TotalCount)
	require.Empty(t, after.Folders[0].Children)

	var markerPaths []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", 1, "kb").Pluck("path", &markerPaths).Error)
	require.Equal(t, []string{"explicit"}, markerPaths, "moving must not invent a marker for the derived folder")
}
