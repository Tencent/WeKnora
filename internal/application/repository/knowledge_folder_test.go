package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeFolderRepositoryTestDDL = `
CREATE TABLE knowledge_folders (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    parent_id TEXT,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    depth INTEGER NOT NULL,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME
);
CREATE TABLE knowledges (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    folder_id TEXT,
    parse_status TEXT NOT NULL DEFAULT 'completed',
    updated_at DATETIME,
    deleted_at DATETIME
);
`

func setupKnowledgeFolderRepositoryTest(t *testing.T) (*gorm.DB, *knowledgeFolderRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgeFolderRepositoryTestDDL).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, &knowledgeFolderRepository{db: db}
}

func seedKnowledgeFolderRepositoryTree(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`
INSERT INTO knowledge_folders
    (id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
VALUES
    ('root', 1, 'kb-1', NULL, 'Root', '/root', 1),
    ('child', 1, 'kb-1', 'root', 'Child', '/root/child', 2),
    ('sibling', 1, 'kb-1', NULL, 'Sibling', '/sibling', 1),
    ('empty', 1, 'kb-1', NULL, 'Empty', '/empty', 1),
    ('other-tenant', 2, 'kb-1', NULL, 'Other', '/other-tenant', 1);
INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id)
VALUES
    ('doc-root', 1, 'kb-1', 'root'),
    ('doc-child', 1, 'kb-1', 'child'),
    ('doc-sibling', 1, 'kb-1', 'sibling'),
    ('doc-kb-root', 1, 'kb-1', NULL),
    ('doc-other-tenant', 2, 'kb-1', 'other-tenant');
`).Error)
}

func TestKnowledgeFolderRepositoryListKnowledgeIDs(t *testing.T) {
	db, repo := setupKnowledgeFolderRepositoryTest(t)
	seedKnowledgeFolderRepositoryTree(t, db)

	descendants, err := repo.ListKnowledgeIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{"root"},
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-child", "doc-root"}, descendants)

	direct, err := repo.ListKnowledgeIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{"root"},
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-root"}, direct)

	_, err = repo.ListKnowledgeIDs(
		context.Background(),
		1,
		"kb-1",
		[]string{"root", "missing"},
		true,
	)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderRepositoryDeleteEmpty(t *testing.T) {
	db, repo := setupKnowledgeFolderRepositoryTest(t)
	seedKnowledgeFolderRepositoryTree(t, db)
	ctx := context.Background()

	require.ErrorIs(t, repo.DeleteEmpty(ctx, 1, "kb-1", "root"), ErrKnowledgeFolderNotEmpty)
	require.ErrorIs(t, repo.DeleteEmpty(ctx, 1, "kb-1", "child"), ErrKnowledgeFolderNotEmpty)

	require.NoError(t, repo.DeleteEmpty(ctx, 1, "kb-1", "empty"))
	_, err := repo.Get(ctx, 1, "kb-1", "empty")
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderRepositoryMoveKnowledgeIsScoped(t *testing.T) {
	db, repo := setupKnowledgeFolderRepositoryTest(t)
	seedKnowledgeFolderRepositoryTree(t, db)

	count, err := repo.MoveKnowledge(
		context.Background(),
		1,
		"kb-1",
		[]string{"doc-root", "doc-other-tenant"},
		stringPointer("child"),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	var folderID string
	require.NoError(t, db.Raw(
		"SELECT folder_id FROM knowledges WHERE id = ?",
		"doc-root",
	).Scan(&folderID).Error)
	assert.Equal(t, "child", folderID)
}

func TestApplyKnowledgeListFilterFolderScope(t *testing.T) {
	db, _ := setupKnowledgeFolderRepositoryTest(t)
	seedKnowledgeFolderRepositoryTree(t, db)

	queryIDs := func(filter types.KnowledgeListFilter) []string {
		t.Helper()
		query := db.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ?", 1, "kb-1")
		query = applyKnowledgeListFilter(query, 1, "kb-1", filter)
		var ids []string
		require.NoError(t, query.Order("id ASC").Pluck("id", &ids).Error)
		return ids
	}

	assert.Equal(t, []string{"doc-kb-root"}, queryIDs(types.KnowledgeListFilter{
		FolderIDSet: true,
	}))
	assert.Equal(t, []string{"doc-child", "doc-kb-root", "doc-root", "doc-sibling"}, queryIDs(
		types.KnowledgeListFilter{
			FolderIDSet:              true,
			IncludeFolderDescendants: true,
		},
	))
	assert.Equal(t, []string{"doc-root"}, queryIDs(types.KnowledgeListFilter{
		FolderIDSet: true,
		FolderID:    stringPointer("root"),
	}))
	assert.Equal(t, []string{"doc-child", "doc-root"}, queryIDs(types.KnowledgeListFilter{
		FolderIDSet:              true,
		FolderID:                 stringPointer("root"),
		IncludeFolderDescendants: true,
	}))
}

func stringPointer(value string) *string {
	return &value
}
