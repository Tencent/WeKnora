package repository

import (
	"context"
	"testing"
	"unicode/utf8"

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

func TestCreateChunks_CleansContextHeaderBeforePersistence(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	chunk := makeChunk(uuid.New().String(), uuid.New().String(), types.ChunkTypeText)
	chunk.ContextHeader = "# \x00投资评级说明\xff"

	require.NoError(t, repo.CreateChunks(context.Background(), []*types.Chunk{chunk}))

	require.Equal(t, "# 投资评级说明", chunk.ContextHeader)
	require.NotContains(t, chunk.ContextHeader, "\x00")
	require.True(t, utf8.ValidString(chunk.ContextHeader))

	var saved types.Chunk
	require.NoError(t, db.First(&saved, "id = ?", chunk.ID).Error)
	assert.Equal(t, "# 投资评级说明", saved.ContextHeader)
	assert.NotContains(t, saved.ContextHeader, "\x00")
	assert.True(t, utf8.ValidString(saved.ContextHeader))
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

func TestListPagedChunksByKnowledgeID_FiltersEnabledState(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	enabledChunk := makeChunk("kb-1", "faq-knowledge", types.ChunkTypeFAQ)
	disabledChunk := makeChunk("kb-1", "faq-knowledge", types.ChunkTypeFAQ)
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{enabledChunk, disabledChunk}))
	require.NoError(t, db.Model(&types.Chunk{}).
		Where("id = ?", disabledChunk.ID).
		Update("is_enabled", false).Error)

	enabled := true
	chunks, total, err := repo.ListPagedChunksByKnowledgeID(
		ctx, 1, "faq-knowledge", &types.Pagination{Page: 1, PageSize: 20},
		[]types.ChunkType{types.ChunkTypeFAQ}, nil, "", "", "", types.KnowledgeTypeFAQ, &enabled,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, chunks, 1)
	assert.Equal(t, enabledChunk.ID, chunks[0].ID)

	disabled := false
	chunks, total, err = repo.ListPagedChunksByKnowledgeID(
		ctx, 1, "faq-knowledge", &types.Pagination{Page: 1, PageSize: 20},
		[]types.ChunkType{types.ChunkTypeFAQ}, nil, "", "", "", types.KnowledgeTypeFAQ, &disabled,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, chunks, 1)
	assert.Equal(t, disabledChunk.ID, chunks[0].ID)

	allChunks, allTotal, err := repo.ListPagedChunksByKnowledgeID(
		ctx, 1, "faq-knowledge", &types.Pagination{Page: 1, PageSize: 20},
		[]types.ChunkType{types.ChunkTypeFAQ}, nil, "", "", "", types.KnowledgeTypeFAQ, nil,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), allTotal)
	assert.Len(t, allChunks, 2)
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

func TestCreateChunks_LeavesSourceContentEmpty(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)

	chunk := makeChunk("kb-src", "k-src", types.ChunkTypeText)
	require.NoError(t, repo.CreateChunks(context.Background(), []*types.Chunk{chunk}))

	var saved types.Chunk
	require.NoError(t, db.First(&saved, "id = ?", chunk.ID).Error)
	assert.Equal(t, "test content", saved.Content)
	assert.Empty(t, saved.SourceContent, "source_content is backfilled lazily on first edit, not on create")
	assert.Equal(t, "ready", saved.IndexStatus)
}

func TestUpdateChunks_UpdatesOnlyDocumentedColumns(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	chunks := make([]*types.Chunk, 0, 3)
	for i := 0; i < 3; i++ {
		c := makeChunk("kb-upd", "k-upd", types.ChunkTypeFAQ)
		c.Metadata = types.JSON(`{"keep":"me"}`)
		c.ContentHash = "hash-original"
		chunks = append(chunks, c)
	}
	require.NoError(t, repo.CreateChunks(ctx, chunks))

	var before types.Chunk
	require.NoError(t, db.First(&before, "id = ?", chunks[0].ID).Error)

	chunks[0].Content = "edited-0"
	chunks[0].IsEnabled = false
	chunks[0].Flags = 0
	chunks[0].Status = int(types.ChunkStatusIndexed)
	chunks[0].TagID = "tag-a"
	chunks[0].ContentHash = "hash-should-not-persist"
	chunks[1].Content = "edited-1"
	chunks[1].Status = int(types.ChunkStatusStored)
	// chunks[2] is deliberately not passed to UpdateChunks.
	require.NoError(t, repo.UpdateChunks(ctx, chunks[:2]))

	var saved []types.Chunk
	require.NoError(t, db.Order("chunk_index").Find(&saved, "knowledge_id = ?", "k-upd").Error)
	byID := make(map[string]types.Chunk, len(saved))
	for _, s := range saved {
		byID[s.ID] = s
	}

	c0 := byID[chunks[0].ID]
	assert.Equal(t, "edited-0", c0.Content)
	assert.False(t, c0.IsEnabled, "zero-value bool must be written")
	assert.Equal(t, types.ChunkFlags(0), c0.Flags, "zero-value flags must be written")
	assert.Equal(t, int(types.ChunkStatusIndexed), c0.Status)
	assert.Equal(t, "tag-a", c0.TagID)
	assert.Equal(t, "hash-original", c0.ContentHash, "content_hash is outside the UpdateChunks contract")
	assert.JSONEq(t, `{"keep":"me"}`, string(c0.Metadata), "metadata is outside the UpdateChunks contract")
	assert.Equal(t, before.SeqID, c0.SeqID)
	assert.False(t, c0.UpdatedAt.Before(before.UpdatedAt))

	c1 := byID[chunks[1].ID]
	assert.Equal(t, "edited-1", c1.Content)
	assert.True(t, c1.IsEnabled)
	assert.Equal(t, int(types.ChunkStatusStored), c1.Status)

	c2 := byID[chunks[2].ID]
	assert.Equal(t, "test content", c2.Content, "rows not in the batch stay untouched")
}

func TestUpdateChunks_CleansInvalidUTF8(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	chunk := makeChunk("kb-utf8", "k-utf8", types.ChunkTypeText)
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))

	chunk.Content = "ok\xffbad"
	require.NoError(t, repo.UpdateChunks(ctx, []*types.Chunk{chunk}))

	var saved types.Chunk
	require.NoError(t, db.First(&saved, "id = ?", chunk.ID).Error)
	assert.True(t, utf8.ValidString(saved.Content))
}

func TestUpdateChunkFieldsByIDs_ScopesByTenantAndSetsUpdatedAt(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	mine := makeChunk("kb-fields", "k-fields", types.ChunkTypeFAQ)
	other := makeChunk("kb-fields", "k-fields", types.ChunkTypeFAQ)
	other.TenantID = 2
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{mine, other}))

	var before types.Chunk
	require.NoError(t, db.First(&before, "id = ?", mine.ID).Error)

	require.NoError(t, repo.UpdateChunkFieldsByIDs(ctx, 1, []string{mine.ID, other.ID}, map[string]interface{}{
		"status": int(types.ChunkStatusIndexed),
	}))

	var savedMine, savedOther types.Chunk
	require.NoError(t, db.First(&savedMine, "id = ?", mine.ID).Error)
	require.NoError(t, db.First(&savedOther, "id = ?", other.ID).Error)
	assert.Equal(t, int(types.ChunkStatusIndexed), savedMine.Status)
	assert.Equal(t, "test content", savedMine.Content, "other columns are untouched")
	assert.False(t, savedMine.UpdatedAt.Before(before.UpdatedAt))
	assert.Equal(t, int(types.ChunkStatusDefault), savedOther.Status, "rows of another tenant must not change")

	// Empty inputs are no-ops.
	require.NoError(t, repo.UpdateChunkFieldsByIDs(ctx, 1, nil, map[string]interface{}{"status": 0}))
	require.NoError(t, repo.UpdateChunkFieldsByIDs(ctx, 1, []string{mine.ID}, nil))
}
