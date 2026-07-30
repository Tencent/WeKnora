package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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

func TestCreateChunks_SQLite_StableIdentitySurvivesRebuildWithRandomRowIDs(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.NewString()
	knowledgeID := uuid.NewString()
	stableIdentity := uuid.NewString()

	first := makeChunk(kbID, knowledgeID, types.ChunkTypeText)
	first.StableIdentity = stableIdentity
	first.IdentityVersion = "chunk-identity-v1"
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{first}))

	require.NoError(t, repo.DeleteChunksByKnowledgeID(ctx, 1, knowledgeID))

	second := makeChunk(kbID, knowledgeID, types.ChunkTypeText)
	second.StableIdentity = stableIdentity
	second.IdentityVersion = "chunk-identity-v1"
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{second}))

	require.NotEqual(t, first.ID, second.ID)

	var allRows []types.Chunk
	require.NoError(t, db.Unscoped().Where("knowledge_id = ?", knowledgeID).Order("seq_id").Find(&allRows).Error)
	require.Len(t, allRows, 2)
	require.Equal(t, stableIdentity, allRows[0].StableIdentity)
	require.Equal(t, stableIdentity, allRows[1].StableIdentity)
	require.True(t, allRows[0].DeletedAt.Valid)
	require.False(t, allRows[1].DeletedAt.Valid)

	var activeRows []types.Chunk
	require.NoError(t, db.Where(
		"tenant_id = ? AND knowledge_id = ? AND stable_identity = ?",
		1,
		knowledgeID,
		stableIdentity,
	).Find(&activeRows).Error)
	require.Len(t, activeRows, 1)
	require.Equal(t, second.ID, activeRows[0].ID)
	require.False(t, activeRows[0].DeletedAt.Valid)
}

func TestListActiveIngestionChunksByKnowledgeID_SQLite_ScopesManagedActiveRows(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	text := makeReconcileRepositoryChunk("text", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-text")
	parent := makeReconcileRepositoryChunk("parent", 1, "kb-1", "knowledge-1", types.ChunkTypeParentText, "stable-parent")
	legacy := makeReconcileRepositoryChunk("legacy", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "")
	derived := makeReconcileRepositoryChunk("summary", 1, "kb-1", "knowledge-1", types.ChunkTypeSummary, "")
	otherTenant := makeReconcileRepositoryChunk("other-tenant", 2, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-other-tenant")
	otherKnowledge := makeReconcileRepositoryChunk("other-knowledge", 1, "kb-1", "knowledge-2", types.ChunkTypeText, "stable-other-knowledge")
	deleted := makeReconcileRepositoryChunk("deleted", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-deleted")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{
		text, parent, legacy, derived, otherTenant, otherKnowledge, deleted,
	}))
	require.NoError(t, db.Delete(deleted).Error)

	got, err := repo.ListActiveIngestionChunksByKnowledgeID(ctx, 1, "knowledge-1")
	require.NoError(t, err)
	require.Equal(t, []string{"legacy", "parent", "text"}, chunkIDs(got))

	empty, err := repo.ListActiveIngestionChunksByKnowledgeID(ctx, 1, "missing")
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}

func TestApplyIngestionChunkReconcile_SQLite_AppliesAtomicManagedDiff(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	createdAt := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	matched := makeReconcileRepositoryChunk("matched", 1, "kb-old", "knowledge-1", types.ChunkTypeText, "stable-matched")
	matched.CreatedAt = createdAt
	matched.Flags = 17
	matched.Metadata = types.JSON(`{"preserved":true}`)
	matched.ImageInfo = `{"url":"preserved"}`
	matched.Status = 7
	matched.IsEnabled = false
	removed := makeReconcileRepositoryChunk("removed", 1, "kb-old", "knowledge-1", types.ChunkTypeParentText, "stable-removed")
	derived := makeReconcileRepositoryChunk("summary", 1, "kb-old", "knowledge-1", types.ChunkTypeSummary, "")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{matched, removed, derived}))

	desiredMatched := makeReconcileRepositoryChunk("temporary-id", 1, "kb-new", "knowledge-1", types.ChunkTypeText, "stable-matched")
	desiredMatched.Content = "updated content"
	desiredMatched.ChunkIndex = 9
	desiredMatched.StartAt = 100
	desiredMatched.EndAt = 200
	desiredMatched.ParentChunkID = "new-parent"
	desiredMatched.PreChunkID = "new-pre"
	desiredMatched.NextChunkID = "new-next"
	desiredMatched.Flags = 0
	desiredMatched.Metadata = nil
	desiredMatched.ImageInfo = ""
	desiredMatched.Status = 0
	desiredMatched.IsEnabled = true
	desiredMatched.UpdatedAt = time.Now().Add(time.Minute).Truncate(time.Millisecond)
	added := makeReconcileRepositoryChunk("added", 1, "kb-new", "knowledge-1", types.ChunkTypeParentText, "stable-added")

	err := repo.ApplyIngestionChunkReconcile(ctx, 1, "knowledge-1", interfaces.IngestionChunkReconcileMutation{
		ExpectedActive: []interfaces.IngestionChunkSnapshot{
			ingestionSnapshot(matched),
			ingestionSnapshot(removed),
		},
		Matched: []interfaces.IngestionChunkUpdate{{ExistingID: matched.ID, Desired: desiredMatched}},
		Added:   []*types.Chunk{added},
		RemovedIDs: []string{
			removed.ID,
		},
	})
	require.NoError(t, err)

	var savedMatched types.Chunk
	require.NoError(t, db.First(&savedMatched, "id = ?", matched.ID).Error)
	require.Equal(t, matched.ID, savedMatched.ID)
	require.Equal(t, matched.SeqID, savedMatched.SeqID)
	require.WithinDuration(t, createdAt, savedMatched.CreatedAt, time.Millisecond)
	require.Equal(t, types.ChunkFlags(17), savedMatched.Flags)
	require.Equal(t, types.JSON(`{"preserved":true}`), savedMatched.Metadata)
	require.Equal(t, `{"url":"preserved"}`, savedMatched.ImageInfo)
	require.Equal(t, 7, savedMatched.Status)
	require.False(t, savedMatched.IsEnabled)
	require.Equal(t, "kb-new", savedMatched.KnowledgeBaseID)
	require.Equal(t, "updated content", savedMatched.Content)
	require.Equal(t, 9, savedMatched.ChunkIndex)
	require.Equal(t, 100, savedMatched.StartAt)
	require.Equal(t, 200, savedMatched.EndAt)
	require.Equal(t, "new-parent", savedMatched.ParentChunkID)
	require.Equal(t, "new-pre", savedMatched.PreChunkID)
	require.Equal(t, "new-next", savedMatched.NextChunkID)

	var savedAdded types.Chunk
	require.NoError(t, db.First(&savedAdded, "id = ?", added.ID).Error)
	require.NotZero(t, savedAdded.SeqID)

	var removedCount int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", removed.ID).Count(&removedCount).Error)
	require.Zero(t, removedCount)
	var deletedRemoved types.Chunk
	require.NoError(t, db.Unscoped().First(&deletedRemoved, "id = ?", removed.ID).Error)
	require.True(t, deletedRemoved.DeletedAt.Valid)

	var savedDerived types.Chunk
	require.NoError(t, db.First(&savedDerived, "id = ?", derived.ID).Error)
	require.Equal(t, types.ChunkTypeSummary, savedDerived.ChunkType)
}

func TestApplyIngestionChunkReconcile_SQLite_RejectsStaleSnapshotWithoutWrites(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	existing := makeReconcileRepositoryChunk("existing", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-existing")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{existing}))
	added := makeReconcileRepositoryChunk("added", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-added")

	err := repo.ApplyIngestionChunkReconcile(ctx, 1, "knowledge-1", interfaces.IngestionChunkReconcileMutation{
		ExpectedActive: nil,
		Added:          []*types.Chunk{added},
	})
	require.ErrorContains(t, err, "snapshot changed")

	var addedCount int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", added.ID).Count(&addedCount).Error)
	require.Zero(t, addedCount)
	var existingCount int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", existing.ID).Count(&existingCount).Error)
	require.Equal(t, int64(1), existingCount)
}

func TestApplyIngestionChunkReconcile_SQLite_RollsBackEarlierUpdatesOnInsertFailure(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	existing := makeReconcileRepositoryChunk("existing", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-existing")
	existing.Content = "before"
	conflicting := makeReconcileRepositoryChunk("conflict", 1, "kb-1", "knowledge-2", types.ChunkTypeText, "stable-conflict")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{existing, conflicting}))
	desired := makeReconcileRepositoryChunk("temporary", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-existing")
	desired.Content = "after"
	addedWithConflictingID := makeReconcileRepositoryChunk(conflicting.ID, 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-added")

	err := repo.ApplyIngestionChunkReconcile(ctx, 1, "knowledge-1", interfaces.IngestionChunkReconcileMutation{
		ExpectedActive: []interfaces.IngestionChunkSnapshot{ingestionSnapshot(existing)},
		Matched:        []interfaces.IngestionChunkUpdate{{ExistingID: existing.ID, Desired: desired}},
		Added:          []*types.Chunk{addedWithConflictingID},
	})
	require.Error(t, err)

	var saved types.Chunk
	require.NoError(t, db.First(&saved, "id = ?", existing.ID).Error)
	require.Equal(t, "before", saved.Content)
}

func TestApplyIngestionChunkReconcile_SQLite_RejectsOutOfScopeMutation(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	desired := makeReconcileRepositoryChunk("id", 2, "kb-1", "knowledge-1", types.ChunkTypeText, "stable")

	err := repo.ApplyIngestionChunkReconcile(context.Background(), 1, "knowledge-1", interfaces.IngestionChunkReconcileMutation{
		Added: []*types.Chunk{desired},
	})
	require.ErrorContains(t, err, "outside tenant")
}

func TestApplyIngestionChunkReconcile_SQLite_RejectsAddedActiveIdentityDuplicate(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	existing := makeReconcileRepositoryChunk("existing", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-duplicate")
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{existing}))
	added := makeReconcileRepositoryChunk("added", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-duplicate")

	err := repo.ApplyIngestionChunkReconcile(ctx, 1, "knowledge-1", interfaces.IngestionChunkReconcileMutation{
		ExpectedActive: []interfaces.IngestionChunkSnapshot{ingestionSnapshot(existing)},
		Added:          []*types.Chunk{added},
	})
	require.ErrorContains(t, err, "duplicates active stable identity")

	var count int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("knowledge_id = ?", "knowledge-1").Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestApplyIngestionChunkReconcile_SQLite_RejectsSupersededAttempt(t *testing.T) {
	db := setupChunkTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_processing_spans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			knowledge_id TEXT NOT NULL,
			attempt INTEGER NOT NULL
		)
	`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_processing_spans (knowledge_id, attempt) VALUES (?, ?)",
		"knowledge-1", 2,
	).Error)
	repo := NewChunkRepository(db)
	added := makeReconcileRepositoryChunk("added", 1, "kb-1", "knowledge-1", types.ChunkTypeText, "stable-added")

	err := repo.ApplyIngestionChunkReconcile(context.Background(), 1, "knowledge-1", interfaces.IngestionChunkReconcileMutation{
		ExpectedAttempt: 1,
		Added:           []*types.Chunk{added},
	})
	require.ErrorContains(t, err, "superseded by attempt 2")

	var count int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", added.ID).Count(&count).Error)
	require.Zero(t, count)
}

func makeReconcileRepositoryChunk(
	id string,
	tenantID uint64,
	kbID string,
	knowledgeID string,
	chunkType types.ChunkType,
	stableIdentity string,
) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		Content:         id + " content",
		ChunkType:       chunkType,
		StableIdentity:  stableIdentity,
		IdentityVersion: "chunk-identity-v1",
		IsEnabled:       true,
	}
}

func ingestionSnapshot(chunk *types.Chunk) interfaces.IngestionChunkSnapshot {
	return interfaces.IngestionChunkSnapshot{
		ID:              chunk.ID,
		StableIdentity:  chunk.StableIdentity,
		IdentityVersion: chunk.IdentityVersion,
		ChunkType:       chunk.ChunkType,
	}
}

func chunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, len(chunks))
	for i, chunk := range chunks {
		ids[i] = chunk.ID
	}
	return ids
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
