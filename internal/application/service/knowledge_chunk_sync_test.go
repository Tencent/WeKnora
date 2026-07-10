package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupStableChunkSyncRepo(t *testing.T) interfaces.ChunkRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.KnowledgeTag{}))
	return repository.NewChunkRepository(db)
}

func stableSyncTestChunk(id, content string) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		TenantID:        1,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         content,
		ContentHash:     contentcache.ChunkContentHash(content, ""),
		ChunkIndex:      1,
		IsEnabled:       true,
		Flags:           types.ChunkFlagRecommended,
		Status:          int(types.ChunkStatusIndexed),
		ChunkType:       types.ChunkTypeImageOCR,
		ParentChunkID:   "parent-1",
		CreatedAt:       time.Unix(100, 0),
		UpdatedAt:       time.Unix(100, 0),
	}
}

func TestUpsertStableChunksCreatesUpdatesAndReusesByID(t *testing.T) {
	ctx := context.Background()
	repo := setupStableChunkSyncRepo(t)

	reused := stableSyncTestChunk("chunk-reused", "same content")
	updated := stableSyncTestChunk("chunk-updated", "old content")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{reused, updated}))
	reusedSeqID := reused.SeqID
	updatedSeqID := updated.SeqID

	stats, err := upsertStableChunks(ctx, repo, 1, []*types.Chunk{
		stableSyncTestChunk("chunk-reused", "same content"),
		stableSyncTestChunk("chunk-updated", "new content"),
		stableSyncTestChunk("chunk-created", "brand new content"),
	})
	require.NoError(t, err)
	require.Equal(t, 3, stats.Planned)
	require.Equal(t, 1, stats.Created)
	require.Equal(t, 1, stats.Updated)
	require.Equal(t, 1, stats.Reused)

	gotReused, err := repo.GetChunkByID(ctx, 1, "chunk-reused")
	require.NoError(t, err)
	require.Equal(t, reusedSeqID, gotReused.SeqID)
	require.Equal(t, "same content", gotReused.Content)

	gotUpdated, err := repo.GetChunkByID(ctx, 1, "chunk-updated")
	require.NoError(t, err)
	require.Equal(t, updatedSeqID, gotUpdated.SeqID)
	require.Equal(t, "new content", gotUpdated.Content)

	gotCreated, err := repo.GetChunkByID(ctx, 1, "chunk-created")
	require.NoError(t, err)
	require.Equal(t, "brand new content", gotCreated.Content)
}

func TestUpsertStableChunksHardDeletesSoftDeletedIDBeforeCreate(t *testing.T) {
	ctx := context.Background()
	repo := setupStableChunkSyncRepo(t)

	softDeleted := stableSyncTestChunk("chunk-soft-deleted", "old content")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{softDeleted}))
	require.NoError(t, repo.DeleteChunks(ctx, 1, []string{softDeleted.ID}))

	stats, err := upsertStableChunks(ctx, repo, 1, []*types.Chunk{
		stableSyncTestChunk("chunk-soft-deleted", "recreated content"),
	})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Created)
	require.Equal(t, 0, stats.Updated)
	require.Equal(t, 0, stats.Reused)

	got, err := repo.GetChunkByID(ctx, 1, "chunk-soft-deleted")
	require.NoError(t, err)
	require.Equal(t, "recreated content", got.Content)
}
