package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupFolderService(t *testing.T) (interfaces.KnowledgeFolderService, interfaces.KnowledgeFolderRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}, &types.Knowledge{}))
	repo := repository.NewKnowledgeFolderRepository(db)
	return NewKnowledgeFolderService(repo), repo, db
}

func seedDoc(t *testing.T, db *gorm.DB, kbID, folderID, fileName string) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, parse_status, file_name, folder_id)
		VALUES (?, 1, ?, 'file', 'completed', ?, ?)
	`, id, kbID, fileName, folderID).Error)
	return id
}

func TestKnowledgeFolderService_CreateValidation(t *testing.T) {
	svc, _, _ := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-create"

	folder, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "  报告  ")
	require.NoError(t, err)
	assert.Equal(t, "报告", folder.Name)
	assert.Equal(t, "报告", folder.Path)
	assert.Equal(t, 1, folder.Depth)

	// Duplicate sibling name is rejected.
	_, err = svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "报告")
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderConflict)

	// Separators and blank names are rejected.
	_, err = svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "a/b")
	assert.Error(t, err)
	_, err = svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "   ")
	assert.Error(t, err)

	// Depth cap: build a chain to the max, then one more level must fail.
	parent := types.KnowledgeFolderRootID
	for i := 1; i <= types.KnowledgeFolderMaxDepth; i++ {
		f, err := svc.CreateFolder(ctx, kbID, 1, parent, fmt.Sprintf("level-%d", i))
		require.NoError(t, err)
		assert.Equal(t, i, f.Depth)
		parent = f.ID
	}
	_, err = svc.CreateFolder(ctx, kbID, 1, parent, "too-deep")
	assert.Error(t, err)
}

func TestKnowledgeFolderService_FindOrCreateFolderPath(t *testing.T) {
	svc, repo, _ := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-path"

	// Creates the missing chain and normalizes degenerate segments.
	leaf, err := svc.FindOrCreateFolderPath(ctx, kbID, 1, types.KnowledgeFolderRootID,
		[]string{" reports ", "", ".", "2026"})
	require.NoError(t, err)
	folder, err := repo.GetByID(ctx, kbID, leaf)
	require.NoError(t, err)
	assert.Equal(t, "reports/2026", folder.Path)
	assert.Equal(t, 2, folder.Depth)

	// Re-resolving the same path reuses the existing chain.
	leafAgain, err := svc.FindOrCreateFolderPath(ctx, kbID, 1, types.KnowledgeFolderRootID,
		[]string{"reports", "2026"})
	require.NoError(t, err)
	assert.Equal(t, leaf, leafAgain)
	all, err := repo.ListAll(ctx, kbID)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// A wholly-degenerate path resolves to the base folder itself.
	base, err := svc.FindOrCreateFolderPath(ctx, kbID, 1, leaf, []string{"", "."})
	require.NoError(t, err)
	assert.Equal(t, leaf, base)
}

func TestKnowledgeFolderService_RenameOrMoveFolder(t *testing.T) {
	svc, repo, _ := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-move"

	a, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "a")
	require.NoError(t, err)
	b, err := svc.CreateFolder(ctx, kbID, 1, a.ID, "b")
	require.NoError(t, err)
	c, err := svc.CreateFolder(ctx, kbID, 1, b.ID, "c")
	require.NoError(t, err)
	sibling, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "sibling")
	require.NoError(t, err)

	// Rename the root of the subtree: every descendant path/depth follows.
	_, err = svc.RenameOrMoveFolder(ctx, kbID, a.ID, "alpha", "", false)
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, kbID, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "alpha/b/c", got.Path)
	assert.Equal(t, 3, got.Depth)

	// Cycle guards.
	_, err = svc.RenameOrMoveFolder(ctx, kbID, a.ID, "", a.ID, true)
	assert.Error(t, err, "cannot move into itself")
	_, err = svc.RenameOrMoveFolder(ctx, kbID, a.ID, "", c.ID, true)
	assert.Error(t, err, "cannot move into own descendant")

	// Sibling name conflict at the target parent.
	_, err = svc.RenameOrMoveFolder(ctx, kbID, sibling.ID, "alpha", "", false)
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderConflict)

	// Reparent subtree b under sibling: paths and depths recompute.
	_, err = svc.RenameOrMoveFolder(ctx, kbID, b.ID, "", sibling.ID, true)
	require.NoError(t, err)
	got, err = repo.GetByID(ctx, kbID, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "sibling/b/c", got.Path)
	assert.Equal(t, 3, got.Depth)
}

func TestKnowledgeFolderService_DeleteFolder(t *testing.T) {
	svc, repo, db := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-del"

	parent, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "parent")
	require.NoError(t, err)
	child, err := svc.CreateFolder(ctx, kbID, 1, parent.ID, "child")
	require.NoError(t, err)
	docID := seedDoc(t, db, kbID, parent.ID, "doc.pdf")

	// Default delete refuses non-empty folders.
	err = svc.DeleteFolder(ctx, kbID, parent.ID, false)
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderNotEmpty)

	// Promote relocates the document and the child folder to the parent level.
	require.NoError(t, svc.DeleteFolder(ctx, kbID, parent.ID, true))
	_, err = repo.GetByID(ctx, kbID, parent.ID)
	assert.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)
	promoted, err := repo.GetByID(ctx, kbID, child.ID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeFolderRootID, promoted.ParentID)
	assert.Equal(t, "child", promoted.Path)
	assert.Equal(t, 1, promoted.Depth)

	var folderID string
	require.NoError(t, db.Raw(`SELECT folder_id FROM knowledges WHERE id = ?`, docID).Scan(&folderID).Error)
	assert.Equal(t, types.KnowledgeFolderRootID, folderID)
}

func TestKnowledgeFolderService_OrganizeByPath(t *testing.T) {
	svc, repo, db := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-org"

	pathed := seedDoc(t, db, kbID, types.KnowledgeFolderRootID, "reports/2026/q1.pdf")
	plain := seedDoc(t, db, kbID, types.KnowledgeFolderRootID, "plain.pdf")
	degenerate := seedDoc(t, db, kbID, types.KnowledgeFolderRootID, "/leading.pdf")

	organized, created, err := svc.OrganizeByPath(ctx, kbID, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), organized)
	assert.Equal(t, int64(2), created, "reports + 2026")

	leaf, err := repo.GetChildByName(ctx, kbID, "", "reports")
	require.NoError(t, err)
	sub, err := repo.GetChildByName(ctx, kbID, leaf.ID, "2026")
	require.NoError(t, err)
	var folderID string
	require.NoError(t, db.Raw(`SELECT folder_id FROM knowledges WHERE id = ?`, pathed).Scan(&folderID).Error)
	assert.Equal(t, sub.ID, folderID)

	// The degenerate "/x.pdf" row and the plain row stay at the root, and a
	// second run is a strict no-op (idempotence, no infinite loop).
	organized, created, err = svc.OrganizeByPath(ctx, kbID, 1)
	require.NoError(t, err)
	assert.Zero(t, organized)
	assert.Zero(t, created)
	for _, id := range []string{plain, degenerate} {
		require.NoError(t, db.Raw(`SELECT folder_id FROM knowledges WHERE id = ?`, id).Scan(&folderID).Error)
		assert.Equal(t, types.KnowledgeFolderRootID, folderID)
	}
}

// TestKnowledgeFolderService_FolderScopeResolution locks the retrieval-scope
// contract: recursive resolution covers the whole subtree, and an empty
// folder yields an empty scope — never "no filter".
func TestKnowledgeFolderService_FolderScopeResolution(t *testing.T) {
	svc, _, db := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-scope"

	top, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "top")
	require.NoError(t, err)
	nested, err := svc.CreateFolder(ctx, kbID, 1, top.ID, "nested")
	require.NoError(t, err)
	empty, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "empty")
	require.NoError(t, err)

	inTop := seedDoc(t, db, kbID, top.ID, "top.pdf")
	inNested := seedDoc(t, db, kbID, nested.ID, "nested.pdf")
	seedDoc(t, db, kbID, types.KnowledgeFolderRootID, "root.pdf")

	// Recursive: subtree documents; non-recursive: direct documents only.
	ids, err := svc.ListKnowledgeIDsByFolderIDs(ctx, 1, kbID, []string{top.ID}, true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{inTop, inNested}, ids)
	ids, err = svc.ListKnowledgeIDsByFolderIDs(ctx, 1, kbID, []string{top.ID}, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{inTop}, ids)

	// Empty folder resolves to an empty scope.
	ids, err = svc.ListKnowledgeIDsByFolderIDs(ctx, 1, kbID, []string{empty.ID}, true)
	require.NoError(t, err)
	assert.Empty(t, ids)

	// A deleted (dead) folder id resolves to nothing rather than erroring.
	require.NoError(t, svc.DeleteFolder(ctx, kbID, empty.ID, false))
	ids, err = svc.ListKnowledgeIDsByFolderIDs(ctx, 1, kbID, []string{empty.ID}, true)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestKnowledgeFolderService_DeleteFoldersByKnowledgeBase(t *testing.T) {
	svc, repo, db := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-clear"

	f, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "f")
	require.NoError(t, err)
	docID := seedDoc(t, db, kbID, f.ID, "doc.pdf")

	require.NoError(t, svc.DeleteFoldersByKnowledgeBase(ctx, kbID))

	all, err := repo.ListAll(ctx, kbID)
	require.NoError(t, err)
	assert.Empty(t, all)
	var folderID string
	require.NoError(t, db.Raw(`SELECT folder_id FROM knowledges WHERE id = ?`, docID).Scan(&folderID).Error)
	assert.Equal(t, types.KnowledgeFolderRootID, folderID, "documents parked back at the root")
}
