package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSaveChunkRevisionIsAtomicAndOptimistic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.ChunkRevision{}))
	repo := NewChunkRepository(db)
	ctx := context.Background()
	now := time.Now()
	chunk := &types.Chunk{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		Content: "before", SourceContent: "before", ChunkType: types.ChunkTypeText,
		IsEnabled: true, IndexStatus: "ready", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))

	snapshot := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		ChunkID: chunk.ID, Revision: 0, Content: "before", IsEnabled: true,
		EditSource: "user", EditedAt: now, CreatedAt: now,
	}
	chunk.Content = "after"
	chunk.ContentRevision = 1
	require.NoError(t, repo.SaveChunkRevision(ctx, chunk, snapshot, 0))

	stored, err := repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "after", stored.Content)
	require.Equal(t, 1, stored.ContentRevision)
	revisions, err := repo.ListChunkRevisions(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	require.Equal(t, "before", revisions[0].Content)

	stale := *chunk
	stale.Content = "stale write"
	stale.ContentRevision = 1
	staleSnapshot := *snapshot
	staleSnapshot.ID = uuid.NewString()
	require.ErrorIs(t, repo.SaveChunkRevision(ctx, &stale, &staleSnapshot, 0), ErrChunkRevisionConflict)

	stored, err = repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "after", stored.Content)
	count := int64(0)
	require.NoError(t, db.Model(&types.ChunkRevision{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.False(t, errors.Is(gorm.ErrRecordNotFound, ErrChunkRevisionConflict))
}
