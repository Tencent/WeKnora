package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupChunkTestDB creates an in-memory SQLite database with chunk and tag tables.
func setupChunkTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.KnowledgeTag{}))
	return db
}

func makeChunk(kbID, knowledgeID string, chunkType string) *types.Chunk {
	return &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		Content:         "test content",
		ChunkType:       chunkType,
		IsEnabled:       true,
	}
}

func TestCreateChunks_SQLite_SeqIDAutoAssigned(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()

	// Create a batch of 5 chunks
	chunks := []*types.Chunk{
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
	}

	err := repo.CreateChunks(ctx, chunks)
	require.NoError(t, err)

	// Verify all chunks got unique sequential seq_ids
	var saved []types.Chunk
	require.NoError(t, db.Order("seq_id").Find(&saved).Error)
	assert.Len(t, saved, 5)

	for i, c := range saved {
		assert.Equal(t, int64(i+1), c.SeqID, "chunk %d should have seq_id %d", i, i+1)
	}
}

func TestCreateChunks_SQLite_SeqIDContinuesFromExisting(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()

	// Create first batch
	batch1 := []*types.Chunk{
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
	}
	require.NoError(t, repo.CreateChunks(ctx, batch1))

	// Create second batch - seq_ids should continue from 3
	batch2 := []*types.Chunk{
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
	}
	require.NoError(t, repo.CreateChunks(ctx, batch2))

	var saved []types.Chunk
	require.NoError(t, db.Order("seq_id").Find(&saved).Error)
	assert.Len(t, saved, 5)

	for i, c := range saved {
		assert.Equal(t, int64(i+1), c.SeqID, "chunk %d should have seq_id %d", i, i+1)
	}
}

func TestCreateChunks_SQLite_SeqIDUniqueAcrossKBs(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kb1 := uuid.New().String()
	kb2 := uuid.New().String()
	k1 := uuid.New().String()
	k2 := uuid.New().String()

	// Create chunks in two different knowledge bases
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{
		makeChunk(kb1, k1, "faq"),
		makeChunk(kb1, k1, "faq"),
	}))
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{
		makeChunk(kb2, k2, "faq"),
		makeChunk(kb2, k2, "faq"),
	}))

	// All seq_ids should be globally unique (1,2,3,4)
	var saved []types.Chunk
	require.NoError(t, db.Order("seq_id").Find(&saved).Error)
	assert.Len(t, saved, 4)

	seqIDs := map[int64]bool{}
	for _, c := range saved {
		assert.NotZero(t, c.SeqID)
		assert.False(t, seqIDs[c.SeqID], "seq_id %d should be unique", c.SeqID)
		seqIDs[c.SeqID] = true
	}
}

func TestKnowledgeTag_SQLite_SeqIDAutoAssigned(t *testing.T) {
	db := setupChunkTestDB(t)
	ctx := context.Background()

	kbID := uuid.New().String()

	// Create tags one by one (as the application does)
	tag1 := &types.KnowledgeTag{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: kbID,
		Name:            "tag1",
	}
	tag2 := &types.KnowledgeTag{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: kbID,
		Name:            "tag2",
	}

	require.NoError(t, db.WithContext(ctx).Create(tag1).Error)
	require.NoError(t, db.WithContext(ctx).Create(tag2).Error)

	// Both should have non-zero, unique seq_ids
	assert.NotZero(t, tag1.SeqID)
	assert.NotZero(t, tag2.SeqID)
	assert.NotEqual(t, tag1.SeqID, tag2.SeqID)
}

func TestCreateChunks_SQLite_SeqIDAfterSoftDelete(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()

	// Create first batch
	batch1 := []*types.Chunk{
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
	}
	require.NoError(t, repo.CreateChunks(ctx, batch1))

	// Soft-delete all chunks (like frontend "clear" does)
	require.NoError(t, db.Where("knowledge_base_id = ?", kbID).Delete(&types.Chunk{}).Error)

	// Verify soft-deleted
	var activeCount int64
	db.Model(&types.Chunk{}).Where("knowledge_base_id = ?", kbID).Count(&activeCount)
	assert.Equal(t, int64(0), activeCount, "all chunks should be soft-deleted")

	// Create second batch — seq_ids must NOT conflict with soft-deleted ones
	batch2 := []*types.Chunk{
		makeChunk(kbID, knowledgeID, "faq"),
		makeChunk(kbID, knowledgeID, "faq"),
	}
	err := repo.CreateChunks(ctx, batch2)
	require.NoError(t, err, "should not get UNIQUE constraint error after soft delete")

	// Verify new seq_ids start after the soft-deleted max (3)
	var saved []types.Chunk
	require.NoError(t, db.Order("seq_id").Find(&saved).Error)
	assert.Len(t, saved, 2)
	assert.Equal(t, int64(4), saved[0].SeqID)
	assert.Equal(t, int64(5), saved[1].SeqID)
}

func TestHardDeleteChunksByKnowledgeID_AllowsStableIDReinsertAfterSoftDelete(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()
	chunk := makeChunk(kbID, knowledgeID, "text")
	chunk.ID = "stable-chunk-id"
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))
	require.NoError(t, repo.DeleteChunksByKnowledgeID(ctx, 1, knowledgeID))

	recreated := makeChunk(kbID, knowledgeID, "text")
	recreated.ID = "stable-chunk-id"
	require.Error(t, repo.CreateChunks(ctx, []*types.Chunk{recreated}),
		"soft-deleted stable IDs should still occupy the primary key")

	require.NoError(t, repo.HardDeleteChunksByKnowledgeID(ctx, 1, knowledgeID))
	recreated.SeqID = 0
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{recreated}))

	got, err := repo.GetChunkByID(ctx, 1, "stable-chunk-id")
	require.NoError(t, err)
	assert.Equal(t, "test content", got.Content)
}

func TestListAllChunksByKnowledgeID_IncludesEveryChunkType(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()
	otherKnowledgeID := uuid.New().String()

	textChunk := makeChunk(kbID, knowledgeID, types.ChunkTypeText)
	textChunk.ChunkIndex = 2
	ocrChunk := makeChunk(kbID, knowledgeID, types.ChunkTypeImageOCR)
	ocrChunk.ChunkIndex = 1
	otherChunk := makeChunk(kbID, otherKnowledgeID, types.ChunkTypeText)
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{textChunk, ocrChunk, otherChunk}))

	got, err := repo.ListAllChunksByKnowledgeID(ctx, 1, knowledgeID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, ocrChunk.ID, got[0].ID)
	assert.Equal(t, textChunk.ID, got[1].ID)
}

func TestHardDeleteChunks_AllowsStableIDReinsertAfterSoftDelete(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()
	stableID := "stable-chunk-id"
	chunk := makeChunk(kbID, knowledgeID, types.ChunkTypeText)
	chunk.ID = stableID
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))
	require.NoError(t, repo.DeleteChunks(ctx, 1, []string{stableID}))

	recreated := makeChunk(kbID, knowledgeID, types.ChunkTypeText)
	recreated.ID = stableID
	require.Error(t, repo.CreateChunks(ctx, []*types.Chunk{recreated}),
		"soft-deleted stable IDs should still occupy the primary key")

	require.NoError(t, repo.HardDeleteChunks(ctx, 1, []string{stableID}))
	recreated.SeqID = 0
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{recreated}))

	got, err := repo.GetChunkByID(ctx, 1, stableID)
	require.NoError(t, err)
	assert.Equal(t, "test content", got.Content)
}

func TestUpdateChunk_SQLite_NoNOWError(t *testing.T) {
	db := setupChunkTestDB(t)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()

	chunk := makeChunk(kbID, knowledgeID, "faq")
	require.NoError(t, db.WithContext(ctx).Create(chunk).Error)

	// Test updating a chunk field — verifies no NOW() related errors
	err := db.WithContext(ctx).Model(chunk).Update("content", "updated content").Error
	assert.NoError(t, err)

	var saved types.Chunk
	require.NoError(t, db.First(&saved, "id = ?", chunk.ID).Error)
	assert.Equal(t, "updated content", saved.Content)
}
