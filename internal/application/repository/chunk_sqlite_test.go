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

func TestUpsertChunksPreservesStableIdentityAndUserFields(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	chunk := makeChunk("kb-1", "knowledge-1", types.ChunkTypeText)
	chunk.ID = "111f15f1-cbb5-5da7-9a97-4da6fd01eec7"
	chunk.Content = "original"
	chunk.TagID = "user-tag"
	chunk.Flags = 7
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))
	originalSeqID := chunk.SeqID

	replacement := makeChunk("kb-1", "knowledge-1", types.ChunkTypeText)
	replacement.ID = chunk.ID
	replacement.Content = "updated"
	replacement.ChunkIndex = 3
	replacement.ContentHash = "hash-v2"
	require.NoError(t, repo.UpsertChunks(ctx, []*types.Chunk{replacement}))

	got, err := repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	assert.Equal(t, originalSeqID, got.SeqID)
	assert.Equal(t, "updated", got.Content)
	assert.Equal(t, 3, got.ChunkIndex)
	assert.Equal(t, "hash-v2", got.ContentHash)
	assert.Equal(t, "user-tag", got.TagID)
	assert.Equal(t, types.ChunkFlags(7), got.Flags)
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

func makeSuggestedFAQChunk(t *testing.T, kbID, knowledgeID, tagID, question string) *types.Chunk {
	t.Helper()
	chunk := makeChunk(kbID, knowledgeID, types.ChunkTypeFAQ)
	chunk.TagID = tagID
	chunk.Flags = types.ChunkFlagRecommended
	require.NoError(t, chunk.SetFAQMetadata(&types.FAQChunkMetadata{StandardQuestion: question}))
	return chunk
}

func makeSuggestedDocumentChunk(t *testing.T, kbID, knowledgeID, question string) *types.Chunk {
	t.Helper()
	chunk := makeChunk(kbID, knowledgeID, types.ChunkTypeText)
	require.NoError(t, chunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
		GeneratedQuestions: []types.GeneratedQuestion{{ID: uuid.NewString(), Question: question}},
	}))
	return chunk
}

func TestListRecommendedFAQChunks_FiltersByTagWithoutWideningToParentKB(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	selectedTag := uuid.NewString()
	otherTag := uuid.NewString()
	selected := makeSuggestedFAQChunk(t, "kb-1", "faq-knowledge", selectedTag, "selected question")
	other := makeSuggestedFAQChunk(t, "kb-1", "faq-knowledge", otherTag, "other question")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{selected, other}))

	got, err := repo.ListRecommendedFAQChunks(ctx, 1, nil, nil, []string{selectedTag}, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, selected.ID, got[0].ID)
}

func TestListRecommendedFAQChunks_UnionsOnlyExplicitScopes(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	selectedTag := uuid.NewString()
	tagged := makeSuggestedFAQChunk(t, "kb-tag", "faq-tag", selectedTag, "tagged question")
	explicitKB := makeSuggestedFAQChunk(t, "kb-explicit", "faq-explicit", uuid.NewString(), "explicit KB question")
	unselected := makeSuggestedFAQChunk(t, "kb-other", "faq-other", uuid.NewString(), "unselected question")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{tagged, explicitKB, unselected}))

	got, err := repo.ListRecommendedFAQChunks(ctx, 1, []string{"kb-explicit"}, nil, []string{selectedTag}, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.ElementsMatch(t, []string{tagged.ID, explicitKB.ID}, []string{got[0].ID, got[1].ID})
}

func TestListRecentDocumentChunksWithQuestions_KnowledgeScopeDoesNotIncludeSiblingDocuments(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	selected := makeSuggestedDocumentChunk(t, "kb-1", "doc-selected", "selected document question")
	sibling := makeSuggestedDocumentChunk(t, "kb-1", "doc-sibling", "sibling document question")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{selected, sibling}))

	got, err := repo.ListRecentDocumentChunksWithQuestions(ctx, 1, nil, []string{"doc-selected"}, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, selected.ID, got[0].ID)
}

func TestListRecentDocumentChunksWithQuestions_UnionsExplicitKBAndKnowledge(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	fromExplicitKB := makeSuggestedDocumentChunk(t, "kb-explicit", "doc-1", "explicit KB question")
	fromExplicitDocument := makeSuggestedDocumentChunk(t, "kb-other", "doc-selected", "selected document question")
	unselected := makeSuggestedDocumentChunk(t, "kb-other", "doc-other", "unselected question")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{fromExplicitKB, fromExplicitDocument, unselected}))

	got, err := repo.ListRecentDocumentChunksWithQuestions(
		ctx, 1, []string{"kb-explicit"}, []string{"doc-selected"}, 10,
	)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.ElementsMatch(t, []string{fromExplicitKB.ID, fromExplicitDocument.ID}, []string{got[0].ID, got[1].ID})
}
