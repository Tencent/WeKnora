package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeRepositoryBatchFolderMoveIsScopedAndAtomic(t *testing.T) {
	db := setupFolderRepositoryTestDB(t)
	repo := NewKnowledgeRepository(db)
	folderRepo := NewFolderRepository(db)
	ctx := context.Background()
	target := createFolder(t, folderRepo, &types.Folder{ID: "target", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "Target"})
	for _, id := range []string{"knowledge-b", "knowledge-a"} {
		insertFolderKnowledge(t, db, id, 10001, "kb-1", nil, false)
	}
	insertFolderKnowledge(t, db, "other-kb", 10001, "kb-2", nil, false)

	require.NoError(t, repo.WithinFolderTransaction(ctx, func(knowledge interfaces.KnowledgeRepository, _ interfaces.FolderRepository) error {
		locked, err := knowledge.GetKnowledgeBatchForUpdate(ctx, 10001, "kb-1", []string{"knowledge-b", "other-kb", "knowledge-a"})
		require.NoError(t, err)
		require.Len(t, locked, 2)
		assert.Equal(t, []string{"knowledge-a", "knowledge-b"}, []string{locked[0].ID, locked[1].ID})
		rows, err := knowledge.UpdateKnowledgeFolderBatch(ctx, 10001, "kb-1", []string{"knowledge-a", "knowledge-b"}, &target.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(2), rows)
		return nil
	}))

	rollback := errors.New("rollback")
	err := repo.WithinFolderTransaction(ctx, func(knowledge interfaces.KnowledgeRepository, _ interfaces.FolderRepository) error {
		rows, updateErr := knowledge.UpdateKnowledgeFolderBatch(ctx, 10001, "kb-1", []string{"knowledge-a", "knowledge-b"}, nil)
		require.NoError(t, updateErr)
		assert.Equal(t, int64(2), rows)
		return rollback
	})
	assert.ErrorIs(t, err, rollback)
	for _, id := range []string{"knowledge-a", "knowledge-b"} {
		knowledge, getErr := repo.GetKnowledgeByID(ctx, 10001, id)
		require.NoError(t, getErr)
		require.NotNil(t, knowledge.FolderID)
		assert.Equal(t, target.ID, *knowledge.FolderID)
	}
	rows, err := repo.UpdateKnowledgeFolderBatch(ctx, 10001, "kb-2", []string{"knowledge-a", "knowledge-b"}, nil)
	require.NoError(t, err)
	assert.Zero(t, rows)
}
