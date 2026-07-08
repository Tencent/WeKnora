package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupKnowledgeFolderServiceTest(t *testing.T) (*knowledgeFolderService, interfaces.KnowledgeRepository, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeFolder{}))

	kgRepo := repository.NewKnowledgeRepository(db)
	folderRepo := repository.NewKnowledgeFolderRepository(db)
	svc := NewKnowledgeFolderService(folderRepo, kgRepo).(*knowledgeFolderService)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	return svc, kgRepo, ctx
}

func TestKnowledgeFolderCreateListAndDuplicate(t *testing.T) {
	t.Parallel()

	svc, _, ctx := setupKnowledgeFolderServiceTest(t)

	root, err := svc.CreateFolder(ctx, "kb-1", "", "Product Docs")
	require.NoError(t, err)
	require.Equal(t, "Product Docs", root.Name)
	require.Equal(t, "", root.ParentID)
	require.Equal(t, "Product Docs", root.Path)

	child, err := svc.CreateFolder(ctx, "kb-1", root.ID, "API")
	require.NoError(t, err)
	require.Equal(t, root.ID, child.ParentID)
	require.Equal(t, "Product Docs/API", child.Path)

	_, err = svc.CreateFolder(ctx, "kb-1", root.ID, "API")
	require.ErrorIs(t, err, ErrKnowledgeFolderExists)

	folders, err := svc.ListFolders(ctx, "kb-1", root.ID)
	require.NoError(t, err)
	require.Len(t, folders, 1)
	require.Equal(t, child.ID, folders[0].ID)
}

func TestKnowledgeFolderMoveKnowledgeAndFilter(t *testing.T) {
	t.Parallel()

	svc, kgRepo, ctx := setupKnowledgeFolderServiceTest(t)
	folder, err := svc.CreateFolder(ctx, "kb-1", "", "Guides")
	require.NoError(t, err)

	inFolder := &types.Knowledge{
		ID:              "k-in",
		TenantID:        10000,
		KnowledgeBaseID: "kb-1",
		Type:            "document",
		Title:           "in folder",
		Source:          "manual",
	}
	root := &types.Knowledge{
		ID:              "k-root",
		TenantID:        10000,
		KnowledgeBaseID: "kb-1",
		Type:            "document",
		Title:           "root",
		Source:          "manual",
	}
	require.NoError(t, kgRepo.CreateKnowledge(ctx, inFolder))
	require.NoError(t, kgRepo.CreateKnowledge(ctx, root))

	moved, err := svc.MoveKnowledgeToFolder(ctx, "k-in", folder.ID)
	require.NoError(t, err)
	require.Equal(t, folder.ID, moved.FolderID)

	result, total, err := kgRepo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx,
		10000,
		"kb-1",
		&types.Pagination{Page: 1, PageSize: 20},
		types.KnowledgeListFilter{FolderID: &folder.ID},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, result, 1)
	require.Equal(t, "k-in", result[0].ID)

	rootFolder := ""
	rootResult, rootTotal, err := kgRepo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx,
		10000,
		"kb-1",
		&types.Pagination{Page: 1, PageSize: 20},
		types.KnowledgeListFilter{FolderID: &rootFolder},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, rootTotal)
	require.Len(t, rootResult, 1)
	require.Equal(t, "k-root", rootResult[0].ID)
}

func TestKnowledgeFolderRenameUpdatesDescendantPaths(t *testing.T) {
	t.Parallel()

	svc, _, ctx := setupKnowledgeFolderServiceTest(t)
	root, err := svc.CreateFolder(ctx, "kb-1", "", "Products")
	require.NoError(t, err)
	child, err := svc.CreateFolder(ctx, "kb-1", root.ID, "Docs")
	require.NoError(t, err)
	grandchild, err := svc.CreateFolder(ctx, "kb-1", child.ID, "v2")
	require.NoError(t, err)

	renamed, err := svc.RenameFolder(ctx, "kb-1", root.ID, "Items")
	require.NoError(t, err)
	require.Equal(t, "Items", renamed.Path)

	children, err := svc.ListFolders(ctx, "kb-1", renamed.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, child.ID, children[0].ID)
	require.Equal(t, "Items/Docs", children[0].Path)

	grandchildren, err := svc.ListFolders(ctx, "kb-1", child.ID)
	require.NoError(t, err)
	require.Len(t, grandchildren, 1)
	require.Equal(t, grandchild.ID, grandchildren[0].ID)
	require.Equal(t, "Items/Docs/v2", grandchildren[0].Path)
}

func TestKnowledgeFolderRenameEscapesLikeWildcards(t *testing.T) {
	t.Parallel()

	svc, _, ctx := setupKnowledgeFolderServiceTest(t)
	wildcardRoot, err := svc.CreateFolder(ctx, "kb-1", "", "Sale 50% Off")
	require.NoError(t, err)
	wildcardChild, err := svc.CreateFolder(ctx, "kb-1", wildcardRoot.ID, "Docs")
	require.NoError(t, err)
	underscoreRoot, err := svc.CreateFolder(ctx, "kb-1", "", "SKU_A")
	require.NoError(t, err)
	underscoreChild, err := svc.CreateFolder(ctx, "kb-1", underscoreRoot.ID, "Specs")
	require.NoError(t, err)

	percentLookalike, err := svc.CreateFolder(ctx, "kb-1", "", "Sale 50X Off")
	require.NoError(t, err)
	percentLookalikeChild, err := svc.CreateFolder(ctx, "kb-1", percentLookalike.ID, "Other")
	require.NoError(t, err)
	underscoreLookalike, err := svc.CreateFolder(ctx, "kb-1", "", "SKUBA")
	require.NoError(t, err)
	underscoreLookalikeChild, err := svc.CreateFolder(ctx, "kb-1", underscoreLookalike.ID, "Other")
	require.NoError(t, err)

	_, err = svc.RenameFolder(ctx, "kb-1", wildcardRoot.ID, "Discount 50% Off")
	require.NoError(t, err)
	_, err = svc.RenameFolder(ctx, "kb-1", underscoreRoot.ID, "SKU_Main")
	require.NoError(t, err)

	wildcardChildren, err := svc.ListFolders(ctx, "kb-1", wildcardRoot.ID)
	require.NoError(t, err)
	require.Len(t, wildcardChildren, 1)
	require.Equal(t, wildcardChild.ID, wildcardChildren[0].ID)
	require.Equal(t, "Discount 50% Off/Docs", wildcardChildren[0].Path)

	underscoreChildren, err := svc.ListFolders(ctx, "kb-1", underscoreRoot.ID)
	require.NoError(t, err)
	require.Len(t, underscoreChildren, 1)
	require.Equal(t, underscoreChild.ID, underscoreChildren[0].ID)
	require.Equal(t, "SKU_Main/Specs", underscoreChildren[0].Path)

	percentLookalikeChildren, err := svc.ListFolders(ctx, "kb-1", percentLookalike.ID)
	require.NoError(t, err)
	require.Len(t, percentLookalikeChildren, 1)
	require.Equal(t, percentLookalikeChild.ID, percentLookalikeChildren[0].ID)
	require.Equal(t, "Sale 50X Off/Other", percentLookalikeChildren[0].Path)

	underscoreLookalikeChildren, err := svc.ListFolders(ctx, "kb-1", underscoreLookalike.ID)
	require.NoError(t, err)
	require.Len(t, underscoreLookalikeChildren, 1)
	require.Equal(t, underscoreLookalikeChild.ID, underscoreLookalikeChildren[0].ID)
	require.Equal(t, "SKUBA/Other", underscoreLookalikeChildren[0].Path)
}

func TestKnowledgeFolderDeleteRequiresEmptyFolder(t *testing.T) {
	t.Parallel()

	svc, kgRepo, ctx := setupKnowledgeFolderServiceTest(t)
	folder, err := svc.CreateFolder(ctx, "kb-1", "", "Archive")
	require.NoError(t, err)
	require.NoError(t, kgRepo.CreateKnowledge(ctx, &types.Knowledge{
		ID:              "k-archive",
		TenantID:        10000,
		KnowledgeBaseID: "kb-1",
		FolderID:        folder.ID,
		Type:            "document",
		Title:           "archive",
		Source:          "manual",
	}))

	err = svc.DeleteEmptyFolder(ctx, "kb-1", folder.ID)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotEmpty)

	_, err = svc.MoveKnowledgeToFolder(ctx, "k-archive", types.KnowledgeFolderRootID)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteEmptyFolder(ctx, "kb-1", folder.ID))

	_, err = svc.ListFolders(ctx, "kb-1", folder.ID)
	require.True(t, errors.Is(err, ErrKnowledgeFolderNotFound))
}
