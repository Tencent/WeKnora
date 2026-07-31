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
	// custom_metadata is NOT NULL on the Knowledge model, so a raw INSERT has to
	// supply it explicitly (the Go layer defaults it via a hook, not the schema).
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, parse_status, file_name, folder_id, custom_metadata)
		VALUES (?, 1, ?, 'file', 'completed', ?, ?, '{}')
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

// TestKnowledgeFolderService_SearchFoldersInScopes locks the service-level
// contract the chat "@" picker depends on: empty scopes resolve to an empty
// result (never "search everything"), and the repository's page/total/has_more
// triple passes through untouched.
func TestKnowledgeFolderService_SearchFoldersInScopes(t *testing.T) {
	svc, _, db := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-scope-search"

	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}))
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, 1, 'Docs', 'document')
	`, kbID).Error)

	reports, err := svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "reports")
	require.NoError(t, err)
	nested, err := svc.CreateFolder(ctx, kbID, 1, reports.ID, "2026")
	require.NoError(t, err)
	_, err = svc.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "empty")
	require.NoError(t, err)
	seedDoc(t, db, kbID, reports.ID, "a.pdf")
	seedDoc(t, db, kbID, nested.ID, "q1.pdf")

	scopes := []types.KnowledgeSearchScope{{TenantID: 1, KBID: kbID}}

	rows, hasMore, total, err := svc.SearchFoldersInScopes(ctx, scopes, "", 0, 20)
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Equal(t, int64(2), total, "the empty folder is not a usable scope")
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"reports", "reports/2026"}, []string{rows[0].Path, rows[1].Path})
	assert.Equal(t, "Docs", rows[0].KnowledgeBaseName)
	assert.Equal(t, int64(1), rows[0].KnowledgeCount)

	// has_more surfaces the remainder so the picker can page.
	rows, hasMore, total, err = svc.SearchFoldersInScopes(ctx, scopes, "", 0, 1)
	require.NoError(t, err)
	assert.True(t, hasMore)
	assert.Equal(t, int64(2), total)
	require.Len(t, rows, 1)

	// No scopes short-circuits to an empty result without touching the store.
	rows, hasMore, total, err = svc.SearchFoldersInScopes(ctx, nil, "", 0, 20)
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Zero(t, total)
	assert.Empty(t, rows)
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

// vanishingRowRepository hard-deletes one folder right after ListAll has
// handed the tree to the service, reproducing a folder deleted concurrently
// inside the service's read-then-write window. The subsequent batch write
// finds no row for it and fails mid-subtree.
type vanishingRowRepository struct {
	interfaces.KnowledgeFolderRepository
	db     *gorm.DB
	vanish string
	fired  bool
}

func (r *vanishingRowRepository) ListAll(
	ctx context.Context, kbID string,
) ([]*types.KnowledgeFolder, error) {
	folders, err := r.KnowledgeFolderRepository.ListAll(ctx, kbID)
	if err != nil || r.fired {
		return folders, err
	}
	r.fired = true
	if err := r.db.Unscoped().
		Where("id = ?", r.vanish).
		Delete(&types.KnowledgeFolder{}).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

// TestKnowledgeFolderService_RenameOrMoveFolderRollsBackOnFailure locks in that
// the service hands the whole recomputed subtree to the repository in ONE
// transaction. Reverting to a row-by-row Update loop would still pass the
// repository's own atomicity test, but would fail here: a mid-subtree failure
// must leave every ancestor path untouched, not half-rewritten.
func TestKnowledgeFolderService_RenameOrMoveFolderRollsBackOnFailure(t *testing.T) {
	_, repo, db := setupFolderService(t)
	ctx := context.Background()
	kbID := "kb-move-rollback"

	seed := NewKnowledgeFolderService(repo)
	a, err := seed.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "a")
	require.NoError(t, err)
	b, err := seed.CreateFolder(ctx, kbID, 1, a.ID, "b")
	require.NoError(t, err)
	c, err := seed.CreateFolder(ctx, kbID, 1, b.ID, "c")
	require.NoError(t, err)
	dst, err := seed.CreateFolder(ctx, kbID, 1, types.KnowledgeFolderRootID, "dst")
	require.NoError(t, err)

	// The deepest descendant disappears between the service's ListAll and its
	// batch write, so the write hits a missing row after a/b were already
	// recomputed in memory.
	racing := &vanishingRowRepository{KnowledgeFolderRepository: repo, db: db, vanish: c.ID}
	svc := NewKnowledgeFolderService(racing)

	_, err = svc.RenameOrMoveFolder(ctx, kbID, a.ID, "", dst.ID, true)
	require.ErrorIs(t, err, repository.ErrKnowledgeFolderNotFound)

	// The two surviving ancestors keep their pre-move path and depth: the
	// batch rolled back rather than leaving a/b relocated under dst while
	// their descendant still claims the old path.
	gotA, err := repo.GetByID(ctx, kbID, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", gotA.Path)
	assert.Equal(t, types.KnowledgeFolderRootID, gotA.ParentID)
	assert.Equal(t, 1, gotA.Depth)

	gotB, err := repo.GetByID(ctx, kbID, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "a/b", gotB.Path)
	assert.Equal(t, 2, gotB.Depth)

	// With the failing row gone from the tree, the same move now succeeds end
	// to end — the rollback left no residue that blocks a retry.
	moved, err := seed.RenameOrMoveFolder(ctx, kbID, a.ID, "", dst.ID, true)
	require.NoError(t, err)
	assert.Equal(t, "dst/a", moved.Path)
	assert.Equal(t, 2, moved.Depth)

	gotB, err = repo.GetByID(ctx, kbID, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "dst/a/b", gotB.Path)
	assert.Equal(t, 3, gotB.Depth)
}
