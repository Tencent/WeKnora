package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// folderPathOf reads a row's stored folder_path directly so the assertions do
// not depend on the list filters.
func folderPathOf(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var path string
	require.NoError(t, db.Raw(`SELECT folder_path FROM knowledges WHERE id = ?`, id).Scan(&path).Error)
	return path
}

func TestExplicitKnowledgeFolder(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", "Projects/Specs"))
	}
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 2, "other", "foreign"))
	counts, err := repo.ListKnowledgeFolderCounts(ctx, 1, "kb")
	require.NoError(t, err)
	require.Len(t, counts, 1)
	require.Equal(t, int64(0), counts[0].Count)
	id := insertKnowledgeInFolder(t, db, 1, "kb", "Projects/Specs", "spec.md")
	moved, err := repo.RenameKnowledgeFolderPath(ctx, 1, "kb", "Projects", "Archive")
	require.NoError(t, err)
	require.EqualValues(t, 1, moved)
	require.Equal(t, "Archive/Specs", folderPathOf(t, db, id))
	_, err = repo.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{id}, "")
	require.NoError(t, err)
	counts, err = repo.ListKnowledgeFolderCounts(ctx, 1, "kb")
	require.NoError(t, err)
	tree := types.BuildKnowledgeFolderTree(counts)
	require.Equal(t, int64(1), tree.TotalDocumentCount)
	require.Equal(t, "Archive/Specs", tree.Folders[0].Children[0].Path)
	require.Zero(t, tree.Folders[0].TotalCount)
}

func TestDeleteEmptyKnowledgeFolder(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	for _, path := range []string{"A_1", "A_1/empty", "AB1/other", "a_1/other"} {
		require.NoError(t, repo.CreateKnowledgeFolder(ctx, 1, "kb", path))
	}
	try := func(path string, want bool) {
		t.Helper()
		got, err := repo.DeleteEmptyKnowledgeFolder(ctx, 1, "kb", path)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	try("A_1", false)
	try("A_1/empty", true)
	doc := insertKnowledgeInFolder(t, db, 1, "kb", "A_1/sub", "file.txt")
	try("A_1", false)
	_, err := repo.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{doc}, "A_1")
	require.NoError(t, err)
	try("A_1", false)
	got, err := repo.DeleteEmptyKnowledgeFolder(ctx, 2, "kb", "A_1")
	require.NoError(t, err)
	require.False(t, got)
	_, err = repo.UpdateKnowledgeFolderPath(ctx, 1, "kb", []string{doc}, "AB1/other")
	require.NoError(t, err)
	try("A_1", true)
	require.Equal(t, "AB1/other", folderPathOf(t, db, doc))
}

func TestUpdateKnowledgeFolderPath(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()
	otherKBID := uuid.New().String()

	loose := insertKnowledgeInFolder(t, db, tenantID, kbID, "", "readme.md")
	filed := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "intro.md")
	foreign := insertKnowledgeInFolder(t, db, tenantID, otherKBID, "docs", "other.md")

	affected, err := repo.UpdateKnowledgeFolderPath(ctx, tenantID, kbID, []string{loose, filed}, "archive/2026")
	require.NoError(t, err)
	assert.Equal(t, int64(2), affected)
	assert.Equal(t, "archive/2026", folderPathOf(t, db, loose))
	assert.Equal(t, "archive/2026", folderPathOf(t, db, filed))

	// Moving back out of every folder is expressed as the empty path.
	affected, err = repo.UpdateKnowledgeFolderPath(ctx, tenantID, kbID, []string{filed}, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)
	assert.Equal(t, "", folderPathOf(t, db, filed))

	// A row in another knowledge base is never touched, even when its ID is passed.
	affected, err = repo.UpdateKnowledgeFolderPath(ctx, tenantID, kbID, []string{foreign}, "leaked")
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
	assert.Equal(t, "docs", folderPathOf(t, db, foreign))

	affected, err = repo.UpdateKnowledgeFolderPath(ctx, tenantID, kbID, nil, "archive")
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
}

func TestRenameKnowledgeFolderPath(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	root := insertKnowledgeInFolder(t, db, tenantID, kbID, "", "readme.md")
	docs := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "intro.md")
	spec := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs/spec", "design.md")
	deep := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs/spec/v2", "draft.md")
	sibling := insertKnowledgeInFolder(t, db, tenantID, kbID, "docsets", "unrelated.md")

	// The folder and its whole subtree move; siblings whose name merely shares a
	// prefix ("docsets") must not be dragged along.
	affected, err := repo.RenameKnowledgeFolderPath(ctx, tenantID, kbID, "docs", "handbook")
	require.NoError(t, err)
	assert.Equal(t, int64(3), affected)
	assert.Equal(t, "handbook", folderPathOf(t, db, docs))
	assert.Equal(t, "handbook/spec", folderPathOf(t, db, spec))
	assert.Equal(t, "handbook/spec/v2", folderPathOf(t, db, deep))
	assert.Equal(t, "docsets", folderPathOf(t, db, sibling))
	assert.Equal(t, "", folderPathOf(t, db, root))

	// Renaming a nested folder onto an existing one merges them.
	_, err = repo.RenameKnowledgeFolderPath(ctx, tenantID, kbID, "handbook/spec", "docsets")
	require.NoError(t, err)
	assert.Equal(t, "docsets", folderPathOf(t, db, spec))
	assert.Equal(t, "docsets/v2", folderPathOf(t, db, deep))
	assert.Equal(t, "docsets", folderPathOf(t, db, sibling))

	// An unknown folder is a no-op rather than an error.
	affected, err = repo.RenameKnowledgeFolderPath(ctx, tenantID, kbID, "missing", "whatever")
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)

	_, err = repo.RenameKnowledgeFolderPath(ctx, tenantID, kbID, "", "whatever")
	assert.Error(t, err, "renaming the root itself is meaningless and must be rejected")
}

func TestRenameKnowledgeFolderPathUnderscoreInPath(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	parent := insertKnowledgeInFolder(t, db, tenantID, kbID, "my_docs", "intro.md")
	child := insertKnowledgeInFolder(t, db, tenantID, kbID, "my_docs/spec", "design.md")

	affected, err := repo.RenameKnowledgeFolderPath(ctx, tenantID, kbID, "my_docs", "handbook")
	require.NoError(t, err)
	assert.Equal(t, int64(2), affected)
	assert.Equal(t, "handbook", folderPathOf(t, db, parent))
	assert.Equal(t, "handbook/spec", folderPathOf(t, db, child))
}
