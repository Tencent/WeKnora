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

// knowledgeFoldersTestDDL mirrors migrations/versioned/000078_knowledge_folders.up.sql
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
