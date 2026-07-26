package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupKnowledgeFolderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_folders (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    parent_folder_id TEXT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    creator_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX idx_folder_sibling_name
ON knowledge_folders(tenant_id, knowledge_base_id, COALESCE(parent_folder_id, ''), LOWER(name))
WHERE deleted_at IS NULL;
CREATE TABLE knowledges (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    folder_id TEXT,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    source TEXT NOT NULL,
    parse_status TEXT NOT NULL DEFAULT 'completed',
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME
);`).Error)
	return db
}

func testFolder(id, kbID string, parentID *string, depth int) *types.KnowledgeFolder {
	now := time.Now()
	return &types.KnowledgeFolder{
		ID: id, TenantID: 1, KnowledgeBaseID: kbID, ParentFolderID: parentID,
		Name: id, Depth: depth, CreatedAt: now, UpdatedAt: now,
	}
}

func TestKnowledgeFolderRepository_RecursiveScopeAndMove(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()
	const kbID = "kb-1"

	root := testFolder("root", kbID, nil, 1)
	rootID := root.ID
	child := testFolder("child", kbID, &rootID, 2)
	require.NoError(t, repo.Create(ctx, root))
	require.NoError(t, repo.Create(ctx, child))
	require.NoError(t, db.Exec("INSERT INTO knowledges(id, tenant_id, knowledge_base_id, folder_id, type, title, source) VALUES (?, 1, ?, ?, 'file', 'root-doc', 'manual')", "doc-root", kbID, root.ID).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledges(id, tenant_id, knowledge_base_id, folder_id, type, title, source) VALUES (?, 1, ?, ?, 'file', 'child-doc', 'manual')", "doc-child", kbID, child.ID).Error)

	direct, err := repo.ListKnowledgeIDsByScope(ctx, 1, kbID, root.ID, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"doc-root"}, direct)

	recursive, err := repo.ListKnowledgeIDsByScope(ctx, 1, kbID, root.ID, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"doc-root", "doc-child"}, recursive)

	require.NoError(t, repo.MoveKnowledge(ctx, 1, kbID, []string{"doc-root"}, &child.ID))
	var folderID string
	require.NoError(t, db.Raw("SELECT folder_id FROM knowledges WHERE id = ?", "doc-root").Scan(&folderID).Error)
	require.Equal(t, child.ID, folderID)
	require.ErrorIs(t, repo.DeleteEmpty(ctx, 1, kbID, child.ID), ErrKnowledgeFolderNotEmpty)
}

func TestKnowledgeFolderRepository_RejectsSiblingNameConflict(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, testFolder("first", "kb-1", nil, 1)))
	duplicate := testFolder("second", "kb-1", nil, 1)
	duplicate.Name = "FIRST"
	require.ErrorIs(t, repo.Create(ctx, duplicate), ErrKnowledgeFolderConflict)
}

func TestKnowledgeFolderRepository_DeleteTree(t *testing.T) {
	db := setupKnowledgeFolderTestDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()
	const kbID = "kb-1"

	root := testFolder("root", kbID, nil, 1)
	rootID := root.ID
	child := testFolder("child", kbID, &rootID, 2)
	other := testFolder("other", kbID, nil, 1)
	require.NoError(t, repo.Create(ctx, root))
	require.NoError(t, repo.Create(ctx, child))
	require.NoError(t, repo.Create(ctx, other))

	require.NoError(t, db.Exec("INSERT INTO knowledges(id, tenant_id, knowledge_base_id, folder_id, type, title, source) VALUES (?, 1, ?, ?, 'file', 'root-doc', 'manual')", "doc-root", kbID, root.ID).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledges(id, tenant_id, knowledge_base_id, folder_id, type, title, source) VALUES (?, 1, ?, ?, 'file', 'child-doc', 'manual')", "doc-child", kbID, child.ID).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledges(id, tenant_id, knowledge_base_id, folder_id, type, title, source) VALUES (?, 1, ?, ?, 'file', 'other-doc', 'manual')", "doc-other", kbID, other.ID).Error)

	require.NoError(t, repo.DeleteTree(ctx, 1, kbID, root.ID))

	var deletedFolderCount int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM knowledge_folders WHERE id IN (?, ?) AND deleted_at IS NOT NULL", root.ID, child.ID).Scan(&deletedFolderCount).Error)
	require.Equal(t, int64(2), deletedFolderCount)

	var activeSiblingCount int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM knowledge_folders WHERE id = ? AND deleted_at IS NULL", other.ID).Scan(&activeSiblingCount).Error)
	require.Equal(t, int64(1), activeSiblingCount)

	var movedToRoot int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM knowledges WHERE id IN (?, ?) AND folder_id IS NULL", "doc-root", "doc-child").Scan(&movedToRoot).Error)
	require.Equal(t, int64(2), movedToRoot)

	var otherFolderID string
	require.NoError(t, db.Raw("SELECT folder_id FROM knowledges WHERE id = ?", "doc-other").Scan(&otherFolderID).Error)
	require.Equal(t, other.ID, otherFolderID)
}
