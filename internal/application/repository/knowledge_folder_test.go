package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const knowledgeFoldersTestDDL = `
CREATE TABLE knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    deleted_at DATETIME
);
INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1), ('kb-2', 1);
CREATE TABLE knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX uq_knowledge_folders_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;
`

func setupKnowledgeFolderRepo(t *testing.T) (*knowledgeFolderRepository, *knowledgeRepository, *gorm.DB) {
	t.Helper()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(knowledgeFoldersTestDDL).Error)
	return &knowledgeFolderRepository{db: db}, &knowledgeRepository{db: db}, db
}

func seedFolder(t *testing.T, db *gorm.DB, tenantID uint64, kbID, id, parentID, name, path string, depth int) {
	t.Helper()
	require.NoError(t, db.Create(&types.KnowledgeFolder{
		ID: id, TenantID: tenantID, KnowledgeBaseID: kbID, ParentID: parentID,
		Name: name, Path: path, Depth: depth,
	}).Error)
}

func seedFolderKnowledge(t *testing.T, db *gorm.DB, tenantID uint64, kbID, id, folderID string) {
	t.Helper()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, type, title, source, parse_status)
		VALUES (?, ?, ?, ?, 'document', ?, 'manual', 'completed')
	`, id, tenantID, kbID, folderID, id).Error)
}

func TestKnowledgeFolderRepository_ScopeAndRootChildren(t *testing.T) {
	folderRepo, _, db := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	seedFolder(t, db, 1, "kb-1", "root-child", "", "Root", "/root-child", 1)
	seedFolder(t, db, 1, "kb-1", "nested", "root-child", "Nested", "/root-child/nested", 2)
	seedFolder(t, db, 1, "kb-2", "other-kb", "", "Other KB", "/other-kb", 1)
	seedFolder(t, db, 2, "kb-1", "other-tenant", "", "Other Tenant", "/other-tenant", 1)

	children, err := folderRepo.ListByParent(ctx, 1, "kb-1", types.FolderRootID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	assert.Equal(t, "root-child", children[0].ID)

	all, err := folderRepo.ListAll(ctx, 1, "kb-1")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.ElementsMatch(t, []string{"root-child", "nested"}, []string{all[0].ID, all[1].ID})

	_, err = folderRepo.GetByID(ctx, 1, "kb-1", "other-kb")
	assert.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	_, err = folderRepo.GetByID(ctx, 1, "kb-1", "other-tenant")
	assert.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestEscapeFolderLike(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"percent":          {input: "/a%b", want: "/a!%b"},
		"underscore":       {input: "/a_b", want: "/a!_b"},
		"backslash":        {input: `/a\b`, want: `/a\b`},
		"escape character": {input: "/a!b", want: "/a!!b"},
		"all characters":   {input: `/a!%_\b`, want: `/a!!!%!_\b`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, escapeFolderLike(tt.input))
		})
	}
}

func TestKnowledgeFolderRepository_DescendantsEscapeLikeSpecialPaths(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		lookalike string
	}{
		{name: "percent", path: "/escaped%value", lookalike: "/escapedXvalue/child"},
		{name: "underscore", path: "/escaped_value", lookalike: "/escapedXvalue/child"},
		{name: "backslash", path: `/escaped\value`, lookalike: "/escapedvalue/child"},
		{name: "escape character", path: "/escaped!value", lookalike: "/escapedvalue/child"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folderRepo, _, db := setupKnowledgeFolderRepo(t)
			ctx := context.Background()
			seedFolder(t, db, 1, "kb-1", "root", "", "Root", tt.path, 1)
			seedFolder(t, db, 1, "kb-1", "child", "root", "Child", tt.path+"/child", 2)
			seedFolder(t, db, 1, "kb-1", "lookalike", "", "Lookalike", tt.lookalike, 2)
			seedFolder(t, db, 1, "kb-2", "cross-kb", "root", "Cross", tt.path+"/cross", 2)
			seedFolder(t, db, 2, "kb-1", "cross-tenant", "root", "Cross", tt.path+"/cross-tenant", 2)

			ids, err := folderRepo.GetDescendantIDs(ctx, 1, "kb-1", []string{"root"})
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"root", "child"}, ids)
		})
	}
}

func TestKnowledgeFolderRepository_SiblingNameCountAndScopedWrites(t *testing.T) {
	folderRepo, _, db := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	seedFolder(t, db, 1, "kb-1", "folder", "parent", "Same", "/parent/folder", 2)
	seedFolder(t, db, 1, "kb-2", "same-id", "parent", "Other", "/parent/same-id", 2)
	seedFolderKnowledge(t, db, 1, "kb-1", "doc-1", "folder")
	seedFolderKnowledge(t, db, 1, "kb-1", "doc-root", "")
	seedFolderKnowledge(t, db, 1, "kb-2", "doc-other-kb", "folder")
	seedFolderKnowledge(t, db, 2, "kb-1", "doc-other-tenant", "folder")

	exists, err := folderRepo.CheckSiblingName(ctx, 1, "kb-1", "parent", "Same", "")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = folderRepo.CheckSiblingName(ctx, 1, "kb-1", "parent", "Same", "folder")
	require.NoError(t, err)
	assert.False(t, exists)

	counts, err := folderRepo.CountKnowledgeByFolder(ctx, 1, "kb-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"": 1, "folder": 1}, counts)

	victim := &types.KnowledgeFolder{ID: "same-id", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "Changed"}
	assert.ErrorIs(t, folderRepo.Update(ctx, victim), ErrKnowledgeFolderNotFound)
	assert.ErrorIs(t, folderRepo.Delete(ctx, 1, "kb-1", "same-id"), ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderRepository_MoveSubtreeEscapesLikeSpecialPaths(t *testing.T) {
	tests := []struct {
		name      string
		oldPath   string
		lookalike string
	}{
		{name: "percent", oldPath: "/old/escaped%value", lookalike: "/old/escapedXvalue/child"},
		{name: "underscore", oldPath: "/old/escaped_value", lookalike: "/old/escapedXvalue/child"},
		{name: "backslash", oldPath: `/old/escaped\value`, lookalike: "/old/escapedvalue/child"},
		{name: "escape character", oldPath: "/old/escaped!value", lookalike: "/old/escapedvalue/child"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folderRepo, _, db := setupKnowledgeFolderRepo(t)
			ctx := context.Background()
			newPath := "/new/moving"
			seedFolder(t, db, 1, "kb-1", "moving", "old-parent", "Moving", tt.oldPath, 2)
			seedFolder(t, db, 1, "kb-1", "child", "moving", "Child", tt.oldPath+"/child", 3)
			seedFolder(t, db, 1, "kb-1", "lookalike", "", "Lookalike", tt.lookalike, 2)
			seedFolder(t, db, 1, "kb-2", "cross", "moving", "Cross", tt.oldPath+"/cross", 3)

			moving, err := folderRepo.GetByID(ctx, 1, "kb-1", "moving")
			require.NoError(t, err)
			moving.ParentID = "new-parent"
			moving.Path = newPath
			moving.Depth = 4
			require.NoError(t, folderRepo.MoveSubtree(ctx, moving, tt.oldPath, newPath, 2))

			child, err := folderRepo.GetByID(ctx, 1, "kb-1", "child")
			require.NoError(t, err)
			assert.Equal(t, newPath+"/child", child.Path)
			assert.Equal(t, 5, child.Depth)
			lookalike, err := folderRepo.GetByID(ctx, 1, "kb-1", "lookalike")
			require.NoError(t, err)
			assert.Equal(t, tt.lookalike, lookalike.Path)
			var cross types.KnowledgeFolder
			require.NoError(t, db.Where("tenant_id = 1 AND knowledge_base_id = 'kb-2' AND id = 'cross'").First(&cross).Error)
			assert.Equal(t, tt.oldPath+"/cross", cross.Path)
		})
	}
}

func TestListIDsByFolderIDs_RootIncludingChildrenMeansFullKB(t *testing.T) {
	_, repo, _ := setupKnowledgeFolderRepo(t)
	ids, fullKB, err := repo.ListIDsByFolderIDs(context.Background(), 1, "kb-1", []string{types.FolderRootID})
	require.NoError(t, err)
	require.True(t, fullKB)
	require.Nil(t, ids)
}

func TestListIDsByFolderIDs_EmptyScopeReturnsEmptyNotFullKB(t *testing.T) {
	_, repo, db := setupKnowledgeFolderRepo(t)
	seedFolder(t, db, 1, "kb-1", "empty-folder", "", "Empty", "/empty-folder", 1)
	ids, fullKB, err := repo.ListIDsByFolderIDs(context.Background(), 1, "kb-1", []string{"empty-folder"})
	require.NoError(t, err)
	require.False(t, fullKB)
	require.Empty(t, ids)
}

func TestListIDsByFolderIDs_NamedFoldersIncludeSubtrees(t *testing.T) {
	_, repo, db := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	seedFolder(t, db, 1, "kb-1", "folder", "", "Folder", "/folder", 1)
	seedFolder(t, db, 1, "kb-1", "child", "folder", "Child", "/folder/child", 2)
	seedFolder(t, db, 1, "kb-1", "prefix", "", "Prefix", "/folder-copy", 1)
	seedFolderKnowledge(t, db, 1, "kb-1", "root-doc", "")
	seedFolderKnowledge(t, db, 1, "kb-1", "folder-doc", "folder")
	seedFolderKnowledge(t, db, 1, "kb-1", "child-doc", "child")
	seedFolderKnowledge(t, db, 1, "kb-1", "prefix-doc", "prefix")
	seedFolderKnowledge(t, db, 1, "kb-2", "other-kb-doc", "folder")
	seedFolderKnowledge(t, db, 2, "kb-1", "other-tenant-doc", "folder")

	ids, fullKB, err := repo.ListIDsByFolderIDs(ctx, 1, "kb-1", []string{"folder"})
	require.NoError(t, err)
	assert.False(t, fullKB)
	assert.ElementsMatch(t, []string{"folder-doc", "child-doc"}, ids)

	_, _, err = repo.ListIDsByFolderIDs(ctx, 1, "kb-1", []string{"folder", "missing"})
	assert.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeFolderRepository_CreateIfAbsentAndTransactionRollback(t *testing.T) {
	folderRepo, _, db := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	candidate := &types.KnowledgeFolder{
		ID: "first", TenantID: 1, KnowledgeBaseID: "kb-1", ParentID: "", Name: "shared",
		Path: "/first", Depth: 1,
	}
	created, inserted, err := folderRepo.CreateIfAbsent(ctx, candidate)
	require.NoError(t, err)
	assert.True(t, inserted)
	assert.Equal(t, "first", created.ID)

	duplicate := &types.KnowledgeFolder{
		ID: "second", TenantID: 1, KnowledgeBaseID: "kb-1", ParentID: "", Name: "shared",
		Path: "/second", Depth: 1,
	}
	created, inserted, err = folderRepo.CreateIfAbsent(ctx, duplicate)
	require.NoError(t, err)
	assert.False(t, inserted)
	assert.Equal(t, "first", created.ID)

	err = folderRepo.Transaction(ctx, func(txRepo interfaces.KnowledgeFolderRepository) error {
		return txRepo.Create(ctx, &types.KnowledgeFolder{
			ID: "rolled-back", TenantID: 1, KnowledgeBaseID: "kb-1", ParentID: "",
			Name: "rolled-back", Path: "/rolled-back", Depth: 1,
		})
	})
	require.NoError(t, err)

	sentinel := errors.New("rollback")
	err = folderRepo.Transaction(ctx, func(txRepo interfaces.KnowledgeFolderRepository) error {
		require.NoError(t, txRepo.Create(ctx, &types.KnowledgeFolder{
			ID: "must-not-exist", TenantID: 1, KnowledgeBaseID: "kb-1", ParentID: "",
			Name: "must-not-exist", Path: "/must-not-exist", Depth: 1,
		}))
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)
	var count int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Where("id = ?", "must-not-exist").Count(&count).Error)
	assert.Zero(t, count)
}

func TestKnowledgeFolderRepository_PhysicalDeleteAllowsScopedNameReuse(t *testing.T) {
	repo, _, db := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	seedFolder(t, db, 1, "kb-1", "victim", "", "same", "/victim", 1)
	seedFolder(t, db, 1, "kb-2", "other-kb", "", "same", "/other-kb", 1)
	seedFolder(t, db, 2, "kb-1", "other-tenant", "", "same", "/other-tenant", 1)

	require.NoError(t, repo.DeleteSubtree(ctx, 1, "kb-1", []string{"victim"}))
	seedFolder(t, db, 1, "kb-1", "rebuilt", "", "same", "/rebuilt", 1)

	var ids []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Order("id").Pluck("id", &ids).Error)
	assert.ElementsMatch(t, []string{"rebuilt", "other-kb", "other-tenant"}, ids)
}

func TestKnowledgeFolderRepository_UpdateNameDoesNotOverwriteMoveFields(t *testing.T) {
	repo, _, db := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	seedFolder(t, db, 1, "kb-1", "folder", "parent", "before", "/parent/folder", 2)

	require.NoError(t, repo.UpdateName(ctx, 1, "kb-1", "folder", "after"))
	folder, err := repo.GetByID(ctx, 1, "kb-1", "folder")
	require.NoError(t, err)
	assert.Equal(t, "after", folder.Name)
	assert.Equal(t, "parent", folder.ParentID)
	assert.Equal(t, "/parent/folder", folder.Path)
	assert.Equal(t, 2, folder.Depth)
}

func TestKnowledgeFolderRepository_LockKnowledgeBaseIsScoped(t *testing.T) {
	repo, _, _ := setupKnowledgeFolderRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.Transaction(ctx, func(txRepo interfaces.KnowledgeFolderRepository) error {
		require.NoError(t, txRepo.LockKnowledgeBase(ctx, 1, "kb-1"))
		assert.ErrorIs(t, txRepo.LockKnowledgeBase(ctx, 2, "kb-1"), ErrKnowledgeFolderNotFound)
		return nil
	}))
}
