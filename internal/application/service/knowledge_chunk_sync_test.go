package service

import (
	"context"
	"errors"
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

type testVectorDeleter struct {
	ids           []string
	dimension     int
	knowledgeType string
	err           error
}

func (d *testVectorDeleter) DeleteByChunkIDList(
	_ context.Context,
	ids []string,
	dimension int,
	knowledgeType string,
) error {
	d.ids = append([]string(nil), ids...)
	d.dimension = dimension
	d.knowledgeType = knowledgeType
	return d.err
}

type testDimensionProvider struct {
	dimension int
}

func (p testDimensionProvider) GetDimensions() int { return p.dimension }

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

func TestUpsertStableChunksPreservesEditedRevisionFields(t *testing.T) {
	ctx := context.Background()
	repo := setupStableChunkSyncRepo(t)

	edited := stableSyncTestChunk("chunk-edited", "user edited content")
	edited.ChunkType = types.ChunkTypeText
	edited.SourceContent = "original parser content"
	edited.ContentRevision = 3
	edited.IndexStatus = "failed"
	edited.LastEditorID = "user-123"
	edited.IsEnabled = true
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{edited}))
	edited.IsEnabled = false
	require.NoError(t, repo.UpdateChunk(ctx, edited))

	incoming := stableSyncTestChunk("chunk-edited", "fresh parser content")
	incoming.ChunkType = types.ChunkTypeText
	incoming.SourceContent = "fresh parser content"
	incoming.ContentRevision = 0
	incoming.IndexStatus = "ready"
	incoming.LastEditorID = ""
	incoming.IsEnabled = true

	stats, err := upsertStableChunks(ctx, repo, 1, []*types.Chunk{incoming})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Reused)

	got, err := repo.GetChunkByID(ctx, 1, "chunk-edited")
	require.NoError(t, err)
	require.Equal(t, "user edited content", got.Content)
	require.Equal(t, "original parser content", got.SourceContent)
	require.Equal(t, 3, got.ContentRevision)
	require.Equal(t, "failed", got.IndexStatus)
	require.Equal(t, "user-123", got.LastEditorID)
	require.False(t, got.IsEnabled)
}

func TestCollectReparseStaleDerivedChunkIDs(t *testing.T) {
	ctx := context.Background()
	repo := setupStableChunkSyncRepo(t)

	summary := stableSyncTestChunk("summary-1", "old summary")
	summary.ChunkType = types.ChunkTypeSummary
	image := stableSyncTestChunk("image-1", "old image ocr")
	image.ChunkType = types.ChunkTypeImageOCR
	text := stableSyncTestChunk("text-1", "text")
	text.ChunkType = types.ChunkTypeText
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{summary, image, text}))

	svc := &knowledgeService{chunkRepo: repo}
	ids, err := svc.collectReparseStaleDerivedChunkIDs(ctx, 1, "knowledge-1", true, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"summary-1", "image-1"}, ids)

	ids, err = svc.collectReparseStaleDerivedChunkIDs(ctx, 1, "knowledge-1", false, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"image-1"}, ids)
}

func TestSyncReparseBaseChunksDefersStaleDeletionUntilVectorCleanupCanRun(t *testing.T) {
	ctx := context.Background()
	repo := setupStableChunkSyncRepo(t)

	stale := stableSyncTestChunk("stale-text", "old content")
	stale.ChunkType = types.ChunkTypeText
	keptExtra := stableSyncTestChunk("kept-image-ocr", "ocr")
	keptExtra.ParentChunkID = "desired-parent"
	desiredParent := stableSyncTestChunk("desired-parent", "parent")
	desiredParent.ChunkType = types.ChunkTypeParentText

	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{stale, keptExtra}))

	svc := &knowledgeService{chunkRepo: repo}
	stats, err := svc.syncReparseBaseChunks(ctx, 1, "knowledge-1", []*types.Chunk{desiredParent})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Created)
	require.Equal(t, 1, stats.Deleted)
	require.Equal(t, []string{"stale-text"}, stats.StaleIDs)
	require.Equal(t, 1, stats.ExtraKept)

	_, err = repo.GetChunkByID(ctx, 1, "stale-text")
	require.NoError(t, err, "stale DB chunk must survive until vector deletion succeeds")

	require.NoError(t, svc.deleteReparseStaleChunks(ctx, 1, stats.StaleIDs))
	_, err = repo.GetChunkByID(ctx, 1, "stale-text")
	require.Error(t, err)

	gotKept, err := repo.GetChunkByID(ctx, 1, "kept-image-ocr")
	require.NoError(t, err)
	require.Equal(t, "desired-parent", gotKept.ParentChunkID)
}

func TestDeleteReparseStaleVectorsPropagatesFailureBeforeDBCleanup(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("vector store unavailable")
	deleter := &testVectorDeleter{err: errBoom}

	err := deleteReparseStaleVectors(ctx, deleter, testDimensionProvider{dimension: 1536}, []string{"stale-1"}, types.KnowledgeBaseTypeDocument)
	require.ErrorIs(t, err, errBoom)
	require.Equal(t, []string{"stale-1"}, deleter.ids)
	require.Equal(t, 1536, deleter.dimension)
	require.Equal(t, types.KnowledgeBaseTypeDocument, deleter.knowledgeType)
}
