package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKnowledgeRepositoryListIDsByFolderScopeSubtree(t *testing.T) {
	db := setupFolderRepositoryTestDB(t)
	knowledgeRepo := NewKnowledgeRepository(db)
	folderRepo := NewFolderRepository(db)
	ctx := context.Background()

	parent := createFolder(t, folderRepo, &types.Folder{ID: "folder-parent", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "Parent"})
	child := createFolder(t, folderRepo, &types.Folder{ID: "folder-child", TenantID: 10001, KnowledgeBaseID: "kb-1", ParentID: &parent.ID, Name: "Child"})
	grandchild := createFolder(t, folderRepo, &types.Folder{ID: "folder-grandchild", TenantID: 10001, KnowledgeBaseID: "kb-1", ParentID: &child.ID, Name: "Grandchild"})
	sibling := createFolder(t, folderRepo, &types.Folder{ID: "folder-sibling", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "Sibling"})
	otherTenantFolder := createFolder(t, folderRepo, &types.Folder{ID: "folder-other-tenant", TenantID: 10002, KnowledgeBaseID: "kb-3", Name: "Other"})
	otherKBFolder := createFolder(t, folderRepo, &types.Folder{ID: "folder-other-kb", TenantID: 10001, KnowledgeBaseID: "kb-2", Name: "Other KB"})

	insertFolderKnowledge(t, db, "00-root", 10001, "kb-1", nil, false)
	insertFolderKnowledge(t, db, "01-parent", 10001, "kb-1", &parent.ID, false)
	insertFolderKnowledge(t, db, "02-child", 10001, "kb-1", &child.ID, false)
	insertFolderKnowledge(t, db, "03-grandchild", 10001, "kb-1", &grandchild.ID, false)
	insertFolderKnowledge(t, db, "04-sibling", 10001, "kb-1", &sibling.ID, false)
	insertFolderKnowledge(t, db, "05-other-kb", 10001, "kb-2", &otherKBFolder.ID, false)
	insertFolderKnowledge(t, db, "06-other-tenant", 10002, "kb-3", &otherTenantFolder.ID, false)
	insertFolderKnowledge(t, db, "07-deleted-knowledge", 10001, "kb-1", &child.ID, true)

	ids, err := knowledgeRepo.ListIDsByFolderScopes(ctx, 10001, "kb-1", []string{parent.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"01-parent", "02-child", "03-grandchild"}, ids)

	unionIDs, err := knowledgeRepo.ListIDsByFolderScopes(
		ctx,
		10001,
		"kb-1",
		[]string{child.ID, sibling.ID, parent.ID, parent.ID},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"01-parent", "02-child", "03-grandchild", "04-sibling"}, unionIDs)
}

func TestKnowledgeRepositoryListIDsByFolderScopeEmptySoftDeleteAndMove(t *testing.T) {
	db := setupFolderRepositoryTestDB(t)
	knowledgeRepo := NewKnowledgeRepository(db)
	folderRepo := NewFolderRepository(db)
	ctx := context.Background()

	folderA := createFolder(t, folderRepo, &types.Folder{ID: "folder-a", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "A"})
	folderB := createFolder(t, folderRepo, &types.Folder{ID: "folder-b", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "B"})
	deletedBranch := createFolder(t, folderRepo, &types.Folder{ID: "folder-deleted", TenantID: 10001, KnowledgeBaseID: "kb-1", ParentID: &folderA.ID, Name: "Deleted"})
	deletedChild := createFolder(t, folderRepo, &types.Folder{ID: "folder-deleted-child", TenantID: 10001, KnowledgeBaseID: "kb-1", ParentID: &deletedBranch.ID, Name: "Deleted child"})

	insertFolderKnowledge(t, db, "doc-a", 10001, "kb-1", &folderA.ID, false)
	insertFolderKnowledge(t, db, "doc-deleted-branch", 10001, "kb-1", &deletedChild.ID, false)

	require.NoError(t, db.Delete(&types.Folder{}, "id = ?", deletedBranch.ID).Error)

	ids, err := knowledgeRepo.ListIDsByFolderScopes(ctx, 10001, "kb-1", []string{folderA.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-a"}, ids)

	emptyIDs, err := knowledgeRepo.ListIDsByFolderScopes(ctx, 10001, "kb-1", []string{folderB.ID})
	require.NoError(t, err)
	assert.Empty(t, emptyIDs)
	assert.NotNil(t, emptyIDs)

	require.NoError(t, knowledgeRepo.UpdateKnowledgeFolder(ctx, 10001, "kb-1", "doc-a", &folderB.ID))
	aIDs, err := knowledgeRepo.ListIDsByFolderScopes(ctx, 10001, "kb-1", []string{folderA.ID})
	require.NoError(t, err)
	assert.Empty(t, aIDs)
	bIDs, err := knowledgeRepo.ListIDsByFolderScopes(ctx, 10001, "kb-1", []string{folderB.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-a"}, bIDs)
}

func TestKnowledgeRepositoryListIDsByFolderScopesCycleConverges(t *testing.T) {
	db := setupFolderRepositoryTestDB(t)
	knowledgeRepo := NewKnowledgeRepository(db)
	folderRepo := NewFolderRepository(db)
	ctx := context.Background()

	a := createFolder(t, folderRepo, &types.Folder{ID: "cycle-a", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "A"})
	b := createFolder(t, folderRepo, &types.Folder{ID: "cycle-b", TenantID: 10001, KnowledgeBaseID: "kb-1", ParentID: &a.ID, Name: "B"})
	insertFolderKnowledge(t, db, "doc-cycle-a", 10001, "kb-1", &a.ID, false)
	insertFolderKnowledge(t, db, "doc-cycle-b", 10001, "kb-1", &b.ID, false)
	require.NoError(t, db.Model(&types.Folder{}).Where("id = ?", a.ID).Update("parent_id", b.ID).Error)

	ids, err := knowledgeRepo.ListIDsByFolderScopes(ctx, 10001, "kb-1", []string{a.ID, b.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-cycle-a", "doc-cycle-b"}, ids)
}

func TestKnowledgeRepositoryListIDsByFolderScopesBuildsPortablePostgresCTE(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DryRun: true})
	require.NoError(t, err)

	query := applyFolderScopesKnowledgeFilter(
		db.Model(&types.Knowledge{}),
		10001,
		"kb-1",
		[]string{"folder-a", "folder-b"},
	)
	statement := query.Distinct("knowledges.id").Order("knowledges.id ASC").Find(&[]string{}).Statement
	sql := statement.SQL.String()
	assert.Contains(t, sql, "WITH RECURSIVE folder_subtree")
	assert.Contains(t, sql, "id IN ($")
	assert.Contains(t, sql, "UNION")
	assert.NotContains(t, sql, "UNION ALL")
	assert.Contains(t, sql, "knowledges.folder_id IN")
	assert.Contains(t, sql, `"knowledges"."deleted_at" IS NULL`)
}
