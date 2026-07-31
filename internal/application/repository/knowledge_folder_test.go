package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// knowledgeFoldersTestDDL mirrors migrations/versioned/000079_knowledge_folders.up.sql
// for SQLite. The knowledges table (including folder_id) comes from the shared
// knowledgesTestDDL in knowledge_finalize_test.go.
const knowledgeFoldersTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INTEGER NOT NULL DEFAULT 0,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);
`

func setupKnowledgeFolderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(knowledgeFoldersTestDDL).Error)
	return db
}

func makeKnowledgeFolder(id, kbID, parentID, name, path string, depth int) *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID: id, TenantID: 1, KnowledgeBaseID: kbID,
		ParentID: parentID, Name: name, Path: path, Depth: depth,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

// insertKnowledgeInFolder seeds a document row placed in the given folder.
func insertKnowledgeInFolder(t *testing.T, db *gorm.DB, tenantID uint64, kbID, folderID, fileName string) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, file_name, folder_id)
		VALUES (?, ?, ?, 'file', 'folder-test', 'manual', 'completed', ?, ?)
	`, id, tenantID, kbID, fileName, folderID).Error)
	return id
}

// TestKnowledgeFolder_CRUDAndChildListing covers create, typed not-found
// errors, sibling lookup by name, and the enriched child listing (document
// counts + has_children) that powers lazy tree loading.
func TestKnowledgeFolder_CRUDAndChildListing(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	ctx := context.Background()
	kbID := "kb-f"

	docs := makeKnowledgeFolder("f-docs", kbID, types.KnowledgeFolderRootID, "docs", "docs", 1)
	reports := makeKnowledgeFolder("f-reports", kbID, types.KnowledgeFolderRootID, "报告", "报告", 1)
	q1 := makeKnowledgeFolder("f-q1", kbID, "f-reports", "Q1", "报告/Q1", 2)
	for _, f := range []*types.KnowledgeFolder{docs, reports, q1} {
		require.NoError(t, repo.Create(ctx, f))
	}

	got, err := repo.GetByID(ctx, kbID, "f-docs")
	require.NoError(t, err)
	assert.Equal(t, "docs", got.Name)

	_, err = repo.GetByID(ctx, kbID, "missing")
	assert.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	// A folder id from another KB must not resolve.
	_, err = repo.GetByID(ctx, "kb-other", "f-docs")
	assert.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	child, err := repo.GetChildByName(ctx, kbID, "f-reports", "Q1")
	require.NoError(t, err)
	assert.Equal(t, "f-q1", child.ID)
	_, err = repo.GetChildByName(ctx, kbID, "f-reports", "Q2")
	assert.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	// Two docs directly in reports, one in its child Q1: the direct count for
	// reports must be 2 (not 3 — subtree expansion is the service's job), and
	// has_children must flag reports but not docs.
	insertKnowledgeInFolder(t, db, 1, kbID, "f-reports", "a.pdf")
	insertKnowledgeInFolder(t, db, 1, kbID, "f-reports", "b.pdf")
	insertKnowledgeInFolder(t, db, 1, kbID, "f-q1", "c.pdf")

	roots, err := repo.ListChildren(ctx, kbID, types.KnowledgeFolderRootID)
	require.NoError(t, err)
	require.Len(t, roots, 2)
	byName := map[string]*types.KnowledgeFolderNode{}
	for _, n := range roots {
		byName[n.Name] = n
	}
	require.Contains(t, byName, "docs")
	require.Contains(t, byName, "报告")
	assert.Equal(t, int64(0), byName["docs"].KnowledgeCount)
	assert.False(t, byName["docs"].HasChildren)
	assert.Equal(t, int64(2), byName["报告"].KnowledgeCount)
	assert.True(t, byName["报告"].HasChildren)

	all, err := repo.ListAll(ctx, kbID)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

// TestKnowledgeFolder_DeleteIsAtomicOnEmptiness locks the race-safe delete
// contract: the emptiness check lives inside the delete statement itself, so
// a folder holding a live document or child folder survives, and only truly
// empty folders go away. A soft-deleted document no longer blocks deletion.
func TestKnowledgeFolder_DeleteIsAtomicOnEmptiness(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	ctx := context.Background()
	kbID := "kb-del"

	parent := makeKnowledgeFolder("f-parent", kbID, types.KnowledgeFolderRootID, "parent", "parent", 1)
	child := makeKnowledgeFolder("f-child", kbID, "f-parent", "child", "parent/child", 2)
	require.NoError(t, repo.Create(ctx, parent))
	require.NoError(t, repo.Create(ctx, child))
	docID := insertKnowledgeInFolder(t, db, 1, kbID, "f-child", "doc.pdf")

	// Parent has a live child folder.
	assert.ErrorIs(t, repo.Delete(ctx, kbID, "f-parent"), ErrKnowledgeFolderNotEmpty)
	// Child has a live document.
	assert.ErrorIs(t, repo.Delete(ctx, kbID, "f-child"), ErrKnowledgeFolderNotEmpty)

	// Soft-delete the document: the child becomes deletable.
	require.NoError(t, db.Exec(`UPDATE knowledges SET deleted_at = ? WHERE id = ?`, time.Now(), docID).Error)
	require.NoError(t, repo.Delete(ctx, kbID, "f-child"))
	// And now the parent too.
	require.NoError(t, repo.Delete(ctx, kbID, "f-parent"))

	// Deleting again reports not-found, not not-empty.
	assert.ErrorIs(t, repo.Delete(ctx, kbID, "f-parent"), ErrKnowledgeFolderNotFound)

	// After soft delete the name is free for reuse.
	again := makeKnowledgeFolder("f-parent-2", kbID, types.KnowledgeFolderRootID, "parent", "parent", 1)
	require.NoError(t, repo.Create(ctx, again))
}

// TestKnowledgeFolder_DocumentPlacementQueries covers the document-side
// helpers: counting and listing documents in folders (tenant-scoped), batch
// re-filing, folder-to-folder moves, and the organize-by-path work queue.
func TestKnowledgeFolder_DocumentPlacementQueries(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	ctx := context.Background()
	kbID := "kb-place"

	a := makeKnowledgeFolder("f-a", kbID, types.KnowledgeFolderRootID, "a", "a", 1)
	b := makeKnowledgeFolder("f-b", kbID, types.KnowledgeFolderRootID, "b", "b", 1)
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))

	d1 := insertKnowledgeInFolder(t, db, 1, kbID, "f-a", "one.pdf")
	d2 := insertKnowledgeInFolder(t, db, 1, kbID, "f-a", "two.pdf")
	d3 := insertKnowledgeInFolder(t, db, 2, kbID, "f-a", "other-tenant.pdf")

	total, err := repo.CountKnowledgeInFolders(ctx, kbID, []string{"f-a", "f-b"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)

	// Listing is tenant-scoped: tenant 1 must not see tenant 2's document.
	ids, err := repo.ListKnowledgeIDsInFolders(ctx, 1, kbID, []string{"f-a"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{d1, d2}, ids)

	// Empty folder list means empty scope, never "no filter".
	ids, err = repo.ListKnowledgeIDsInFolders(ctx, 1, kbID, nil)
	require.NoError(t, err)
	assert.Empty(t, ids)

	// Batch re-file d1 into f-b; d2 stays.
	moved, err := repo.BatchUpdateKnowledgeFolder(ctx, kbID, []string{d1}, "f-b")
	require.NoError(t, err)
	assert.Equal(t, int64(1), moved)
	ids, err = repo.ListKnowledgeIDsInFolders(ctx, 1, kbID, []string{"f-b"})
	require.NoError(t, err)
	assert.Equal(t, []string{d1}, ids)

	// A knowledge id from another KB is silently ignored by the KB guard.
	moved, err = repo.BatchUpdateKnowledgeFolder(ctx, "kb-other", []string{d2}, "f-b")
	require.NoError(t, err)
	assert.Equal(t, int64(0), moved)

	// Promote-style move: everything in f-a (d2 + tenant-2 doc) goes to root.
	moved, err = repo.MoveKnowledgeBetweenFolders(ctx, kbID, "f-a", types.KnowledgeFolderRootID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), moved)
	assert.Contains(t, listFolderOf(t, db, d3), types.KnowledgeFolderRootID)
}

func listFolderOf(t *testing.T, db *gorm.DB, knowledgeID string) string {
	t.Helper()
	var folderID string
	require.NoError(t, db.Raw(`SELECT folder_id FROM knowledges WHERE id = ?`, knowledgeID).Scan(&folderID).Error)
	return folderID
}

// TestKnowledgeFolder_ListPathedRootKnowledge locks the organize-by-path work
// queue predicate: only live, root-level documents whose file_name carries a
// "/" are returned, and filing a document removes it from the queue — the
// property that makes OrganizeByPath idempotent.
func TestKnowledgeFolder_ListPathedRootKnowledge(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	ctx := context.Background()
	kbID := "kb-org"

	pathed := insertKnowledgeInFolder(t, db, 1, kbID, types.KnowledgeFolderRootID, "reports/2026/q1.pdf")
	insertKnowledgeInFolder(t, db, 1, kbID, types.KnowledgeFolderRootID, "plain.pdf")
	alreadyFiled := insertKnowledgeInFolder(t, db, 1, kbID, "f-somewhere", "reports/2026/q2.pdf")

	rows, err := repo.ListPathedRootKnowledge(ctx, kbID, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, pathed, rows[0].ID)
	assert.Equal(t, "reports/2026/q1.pdf", rows[0].FileName)

	// Filing the row empties the queue (idempotence of a second organize run).
	_, err = repo.BatchUpdateKnowledgeFolder(ctx, kbID, []string{pathed}, "f-somewhere")
	require.NoError(t, err)
	rows, err = repo.ListPathedRootKnowledge(ctx, kbID, 100)
	require.NoError(t, err)
	assert.Empty(t, rows)
	_ = alreadyFiled
}

// TestKnowledgeFolder_SearchFoldersInScopes covers the cross-KB "@"-picker
// query: scope isolation, the non-empty-folder rule, name/path matching, a
// total that spans every scope, offset/limit paging with has_more, and the
// per-row document count and KB label the picker renders.
func TestKnowledgeFolder_SearchFoldersInScopes(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	// The search joins knowledge_bases for the folder's KB label; reuse the
	// shared DDL from knowledgebase_sqlite_test.go.
	require.NoError(t, db.Exec(knowledgeBasesTestDDL).Error)
	repo := NewKnowledgeFolderRepository(db)
	ctx := context.Background()

	const kbMine, kbShared, kbHidden = "kb-mine", "kb-shared", "kb-hidden"
	for _, kb := range []struct {
		id       string
		tenantID uint64
		name     string
	}{
		{kbMine, 1, "My KB"},
		{kbShared, 2, "Shared KB"},
		{kbHidden, 3, "Hidden KB"},
	} {
		// embedding_model_id / summary_model_id are NOT NULL without defaults.
		require.NoError(t, db.Exec(`
			INSERT INTO knowledge_bases (id, tenant_id, name, type, embedding_model_id, summary_model_id)
			VALUES (?, ?, ?, 'document', 'embed', 'summary')
		`, kb.id, kb.tenantID, kb.name).Error)
	}

	mk := func(id, kbID string, tenantID uint64, parentID, name, path string, depth int) {
		f := makeKnowledgeFolder(id, kbID, parentID, name, path, depth)
		f.TenantID = tenantID
		require.NoError(t, repo.Create(ctx, f))
	}
	mk("f-reports", kbMine, 1, types.KnowledgeFolderRootID, "reports", "reports", 1)
	mk("f-2026", kbMine, 1, "f-reports", "2026", "reports/2026", 2)
	mk("f-empty", kbMine, 1, types.KnowledgeFolderRootID, "empty", "empty", 1)
	mk("f-shared", kbShared, 2, types.KnowledgeFolderRootID, "shared-docs", "shared-docs", 1)
	mk("f-hidden", kbHidden, 3, types.KnowledgeFolderRootID, "reports", "reports", 1)

	insertKnowledgeInFolder(t, db, 1, kbMine, "f-reports", "a.pdf")
	insertKnowledgeInFolder(t, db, 1, kbMine, "f-reports", "b.pdf")
	insertKnowledgeInFolder(t, db, 1, kbMine, "f-2026", "q1.pdf")
	insertKnowledgeInFolder(t, db, 2, kbShared, "f-shared", "s.pdf")
	insertKnowledgeInFolder(t, db, 3, kbHidden, "f-hidden", "h.pdf")

	scopes := []types.KnowledgeSearchScope{
		{TenantID: 1, KBID: kbMine},
		{TenantID: 2, KBID: kbShared},
	}

	// Browse: every non-empty folder of the in-scope KBs, ordered by path. The
	// empty folder and the out-of-scope KB's folder are both absent, and total
	// spans both scopes rather than reporting a single KB's count.
	rows, hasMore, total, err := repo.SearchFoldersInScopes(ctx, scopes, "", 0, 20)
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Equal(t, int64(3), total)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"reports", "reports/2026", "shared-docs"},
		[]string{rows[0].Path, rows[1].Path, rows[2].Path})

	// Counts are per-folder (direct placement, not subtree) and each row
	// carries its KB's name.
	byID := map[string]*types.KnowledgeFolderSearchResult{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	assert.Equal(t, int64(2), byID["f-reports"].KnowledgeCount)
	assert.Equal(t, "My KB", byID["f-reports"].KnowledgeBaseName)
	assert.Equal(t, int64(1), byID["f-2026"].KnowledgeCount)
	assert.Equal(t, "Shared KB", byID["f-shared"].KnowledgeBaseName)
	assert.NotContains(t, byID, "f-empty", "an empty folder is not a usable scope")
	assert.NotContains(t, byID, "f-hidden", "out-of-scope KB must not leak")

	// Keyword matches the materialized path as well as the name, so searching
	// "reports" also surfaces the nested child.
	rows, _, total, err = repo.SearchFoldersInScopes(ctx, scopes, "reports", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"reports", "reports/2026"}, []string{rows[0].Path, rows[1].Path})

	// Paging: limit slices the ordered set, has_more flags the remainder, and
	// total stays the full match count so the picker's header is reachable.
	rows, hasMore, total, err = repo.SearchFoldersInScopes(ctx, scopes, "", 0, 2)
	require.NoError(t, err)
	assert.True(t, hasMore)
	assert.Equal(t, int64(3), total)
	require.Len(t, rows, 2)
	rows, hasMore, total, err = repo.SearchFoldersInScopes(ctx, scopes, "", 2, 2)
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Equal(t, int64(3), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "shared-docs", rows[0].Path)

	// No scopes means an empty result, never "search everything".
	rows, hasMore, total, err = repo.SearchFoldersInScopes(ctx, nil, "", 0, 20)
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Zero(t, total)
	assert.Empty(t, rows)

	// LIKE metacharacters are escaped rather than treated as wildcards.
	rows, _, total, err = repo.SearchFoldersInScopes(ctx, scopes, "re%rts", 0, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, rows)

	// Soft-deleting the only document empties the folder, which drops it from
	// the picker — the JOIN is on live documents.
	require.NoError(t, db.Exec(
		`UPDATE knowledges SET deleted_at = ? WHERE folder_id = ?`, time.Now(), "f-shared").Error)
	rows, _, total, err = repo.SearchFoldersInScopes(ctx, scopes, "", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"reports", "reports/2026"}, []string{rows[0].Path, rows[1].Path})

	// A soft-deleted folder disappears even while holding documents.
	require.NoError(t, db.Exec(
		`UPDATE knowledge_folders SET deleted_at = ? WHERE id = ?`, time.Now(), "f-2026").Error)
	rows, _, total, err = repo.SearchFoldersInScopes(ctx, scopes, "", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "reports", rows[0].Path)
}

// TestKnowledgeFolder_UpdateSubtreeIsAtomic pins the transactional guarantee a
// folder move depends on: the service hands over the whole recomputed subtree at
// once, so a failure halfway through must leave every materialized path at its
// old value. A per-row update loop would strand the batch half-applied, with
// path no longer agreeing with the parent_id adjacency.
func TestKnowledgeFolder_UpdateSubtreeIsAtomic(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db)
	ctx := context.Background()
	kbID := "kb-tx"

	parent := makeKnowledgeFolder("f-parent", kbID, types.KnowledgeFolderRootID, "docs", "docs", 1)
	child := makeKnowledgeFolder("f-child", kbID, parent.ID, "2026", "docs/2026", 2)
	require.NoError(t, repo.Create(ctx, parent))
	require.NoError(t, repo.Create(ctx, child))

	// An empty batch is a no-op, not an error: a rename that resolves to the
	// current path reaches the repository with nothing to write.
	require.NoError(t, repo.UpdateSubtree(ctx, nil))

	// Batch of three where the last row no longer exists — the same shape as a
	// concurrent delete landing between the service's ListAll and the write.
	renamedParent := makeKnowledgeFolder(parent.ID, kbID, types.KnowledgeFolderRootID, "archive", "archive", 1)
	renamedChild := makeKnowledgeFolder(child.ID, kbID, parent.ID, "2026", "archive/2026", 2)
	vanished := makeKnowledgeFolder("f-gone", kbID, parent.ID, "gone", "archive/gone", 2)

	err := repo.UpdateSubtree(ctx, []*types.KnowledgeFolder{renamedParent, renamedChild, vanished})
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)

	// Rows written before the failure must be rolled back.
	gotParent, err := repo.GetByID(ctx, kbID, parent.ID)
	require.NoError(t, err)
	assert.Equal(t, "docs", gotParent.Name)
	assert.Equal(t, "docs", gotParent.Path)

	gotChild, err := repo.GetByID(ctx, kbID, child.ID)
	require.NoError(t, err)
	assert.Equal(t, "docs/2026", gotChild.Path)

	// The same batch minus the vanished row commits in full.
	require.NoError(t, repo.UpdateSubtree(ctx, []*types.KnowledgeFolder{renamedParent, renamedChild}))

	gotParent, err = repo.GetByID(ctx, kbID, parent.ID)
	require.NoError(t, err)
	assert.Equal(t, "archive", gotParent.Path)

	gotChild, err = repo.GetByID(ctx, kbID, child.ID)
	require.NoError(t, err)
	assert.Equal(t, "archive/2026", gotChild.Path)
}

// TestUpdateKnowledge_DoesNotClobberFolderAssignment pins the omission of
// FolderID from full-row saves. Document parsing is asynchronous: a worker
// loads the row, does slow work (parsing, an LLM summary), then saves an
// unrelated field. If the user moved the document to another folder in the
// meantime, a full-row Save carrying the worker's stale FolderID would
// silently undo that move. Placement is owned by the folder repository's
// targeted folder_id updates.
func TestUpdateKnowledge_DoesNotClobberFolderAssignment(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	folderRepo := NewKnowledgeFolderRepository(db)
	knowledgeRepo := NewKnowledgeRepository(db)
	ctx := context.Background()
	kbID := "kb-race"

	require.NoError(t, folderRepo.Create(ctx,
		makeKnowledgeFolder("f-target", kbID, types.KnowledgeFolderRootID, "target", "target", 1)))
	docID := insertKnowledgeInFolder(t, db, 1, kbID, types.KnowledgeFolderRootID, "doc.pdf")

	// The worker's in-memory snapshot, taken while the doc was still at the root.
	stale, err := knowledgeRepo.GetKnowledgeByID(ctx, 1, docID)
	require.NoError(t, err)
	require.Equal(t, types.KnowledgeFolderRootID, stale.FolderID)

	// Meanwhile the user files the document into a folder.
	moved, err := folderRepo.BatchUpdateKnowledgeFolder(ctx, kbID, []string{docID}, "f-target")
	require.NoError(t, err)
	require.Equal(t, int64(1), moved)

	// The worker finishes and saves an unrelated field from its stale snapshot.
	stale.Description = "summary produced by the async worker"
	require.NoError(t, knowledgeRepo.UpdateKnowledge(ctx, stale))

	assert.Equal(t, "f-target", listFolderOf(t, db, docID),
		"a full-row save must not write back the folder_id it read before the move")

	// The unrelated field did land — the omission is surgical, not a no-op save.
	reloaded, err := knowledgeRepo.GetKnowledgeByID(ctx, 1, docID)
	require.NoError(t, err)
	assert.Equal(t, "summary produced by the async worker", reloaded.Description)

	// An explicit column write is still the way to reset placement (cross-KB move).
	require.NoError(t, knowledgeRepo.UpdateKnowledgeColumns(ctx, docID, map[string]interface{}{
		"folder_id": types.KnowledgeFolderRootID,
	}))
	assert.Equal(t, types.KnowledgeFolderRootID, listFolderOf(t, db, docID))
}
