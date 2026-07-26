package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeFolderService_DepthLimitAndCycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}))
	repo := repository.NewKnowledgeFolderRepository(db)
	svc := NewKnowledgeFolderService(repo)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
	const kbID = "kb-1"

	root, err := svc.Create(ctx, kbID, &types.KnowledgeFolderCreateRequest{Name: "level-1"})
	require.NoError(t, err)
	parent := root
	for depth := 2; depth <= types.MaxKnowledgeFolderDepth; depth++ {
		parentID := parent.ID
		parent, err = svc.Create(ctx, kbID, &types.KnowledgeFolderCreateRequest{
			ParentFolderID: &parentID,
			Name:           fmt.Sprintf("level-%d", depth),
		})
		require.NoError(t, err)
	}
	deepestID := parent.ID
	_, err = svc.Create(ctx, kbID, &types.KnowledgeFolderCreateRequest{
		ParentFolderID: &deepestID,
		Name:           "too-deep",
	})
	require.ErrorIs(t, err, ErrKnowledgeFolderDepthLimit)

	secondRoot, err := svc.Create(ctx, kbID, &types.KnowledgeFolderCreateRequest{Name: "second-root"})
	require.NoError(t, err)
	_, err = svc.Update(ctx, kbID, root.ID, &types.KnowledgeFolderUpdateRequest{ParentFolderID: &secondRoot.ID})
	require.ErrorIs(t, err, ErrKnowledgeFolderDepthLimit)

	_, err = svc.Update(ctx, kbID, root.ID, &types.KnowledgeFolderUpdateRequest{ParentFolderID: &deepestID})
	require.ErrorIs(t, err, ErrKnowledgeFolderCycle)
}
