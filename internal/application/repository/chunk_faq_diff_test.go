package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffFAQChunkIDsByContentHash(t *testing.T) {
	toAdd, toDelete, matched := diffFAQChunkIDsByContentHash(
		[]chunkIDHash{
			{ID: "src-1", ContentHash: "hash-a"},
			{ID: "src-2", ContentHash: "hash-b"},
			{ID: "src-3", ContentHash: "hash-only-src"},
		},
		[]chunkIDHash{
			{ID: "dst-1", ContentHash: "hash-a"},
			{ID: "dst-2", ContentHash: "hash-only-dst"},
		},
	)

	assert.ElementsMatch(t, []string{"src-2", "src-3"}, toAdd)
	assert.ElementsMatch(t, []string{"dst-2"}, toDelete)
	require.Len(t, matched, 1)
	assert.Equal(t, "src-1", matched[0].SrcChunkID)
	assert.Equal(t, "dst-1", matched[0].DstChunkID)
}

func TestDiffFAQChunkIDsByContentHash_dstDuplicateHash(t *testing.T) {
	// Two destination chunks share hash-a (same FAQ content, e.g. stale copy after
	// answer_strategy-only change). Source has one chunk for hash-a → keep dst-1,
	// delete dst-dup, match for status sync.
	toAdd, toDelete, matched := diffFAQChunkIDsByContentHash(
		[]chunkIDHash{{ID: "src-1", ContentHash: "hash-a"}},
		[]chunkIDHash{
			{ID: "dst-1", ContentHash: "hash-a"},
			{ID: "dst-dup", ContentHash: "hash-a"},
		},
	)

	assert.Empty(t, toAdd)
	assert.ElementsMatch(t, []string{"dst-dup"}, toDelete)
	require.Len(t, matched, 1)
	assert.Equal(t, "src-1", matched[0].SrcChunkID)
	assert.Equal(t, "dst-1", matched[0].DstChunkID)
}

func TestDiffFAQChunkIDsByContentHash_dstDuplicateHash_notInSrc(t *testing.T) {
	// Duplicates whose hash is absent from source are all removed (orphan content).
	toAdd, toDelete, matched := diffFAQChunkIDsByContentHash(
		nil,
		[]chunkIDHash{
			{ID: "dst-orphan-1", ContentHash: "hash-orphan"},
			{ID: "dst-orphan-2", ContentHash: "hash-orphan"},
		},
	)

	assert.Empty(t, toAdd)
	assert.ElementsMatch(t, []string{"dst-orphan-1", "dst-orphan-2"}, toDelete)
	assert.Empty(t, matched)
}

func TestDiffFAQChunkIDsByContentHash_emptySide(t *testing.T) {
	toAdd, toDelete, matched := diffFAQChunkIDsByContentHash(
		[]chunkIDHash{{ID: "src-1", ContentHash: "hash-a"}},
		nil,
	)
	assert.Equal(t, []string{"src-1"}, toAdd)
	assert.Empty(t, toDelete)
	assert.Empty(t, matched)

	toAdd, toDelete, matched = diffFAQChunkIDsByContentHash(
		nil,
		[]chunkIDHash{{ID: "dst-1", ContentHash: "hash-a"}},
	)
	assert.Empty(t, toAdd)
	assert.Equal(t, []string{"dst-1"}, toDelete)
	assert.Empty(t, matched)
}

func TestFAQChunkDiff_SQLite(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	srcKBID := uuid.New().String()
	dstKBID := uuid.New().String()
	srcKnowledgeID := uuid.New().String()
	dstKnowledgeID := uuid.New().String()

	require.NoError(t, db.Create(&types.Chunk{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: srcKBID, KnowledgeID: srcKnowledgeID,
		ChunkType: types.ChunkTypeFAQ, ContentHash: "shared", Status: int(types.ChunkStatusIndexed),
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: srcKBID, KnowledgeID: srcKnowledgeID,
		ChunkType: types.ChunkTypeFAQ, ContentHash: "src-only", Status: int(types.ChunkStatusIndexed),
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: dstKBID, KnowledgeID: dstKnowledgeID,
		ChunkType: types.ChunkTypeFAQ, ContentHash: "shared", Status: int(types.ChunkStatusIndexed),
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: dstKBID, KnowledgeID: dstKnowledgeID,
		ChunkType: types.ChunkTypeFAQ, ContentHash: "dst-only", Status: int(types.ChunkStatusIndexed),
	}).Error)

	diff, err := repo.FAQChunkDiff(ctx, 1, srcKBID, 1, dstKBID)
	require.NoError(t, err)
	assert.Len(t, diff.ChunksToAdd, 1)
	assert.Len(t, diff.ChunksToDelete, 1)
	assert.Len(t, diff.MatchedPairs, 1)
}
