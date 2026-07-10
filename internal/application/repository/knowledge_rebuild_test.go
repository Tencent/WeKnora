package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupKnowledgeRebuildTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			parse_status TEXT NOT NULL DEFAULT 'pending',
			pending_subtasks_count INTEGER NOT NULL DEFAULT 0,
			error_message TEXT NOT NULL DEFAULT '',
			processed_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		)
	`).Error)
	require.NoError(t, db.AutoMigrate(
		&types.KnowledgeRebuildRun{},
		&types.KnowledgeRebuildChunkResult{},
		&types.KnowledgeRebuildImageResult{},
		&types.KnowledgeRebuildArtifactResult{},
	))
	return db
}

func startKnowledgeRebuildTestRun(
	t *testing.T,
	db *gorm.DB,
	tenantID uint64,
	knowledgeID string,
) (*knowledgeRebuildRunRepository, *types.KnowledgeRebuildRun) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges(id, tenant_id) VALUES (?, ?)", knowledgeID, tenantID,
	).Error)
	repo := &knowledgeRebuildRunRepository{db: db}
	run := &types.KnowledgeRebuildRun{
		TenantID:    tenantID,
		KnowledgeID: knowledgeID,
		Status:      types.RebuildRunStatusParsed,
	}
	require.NoError(t, repo.Start(context.Background(), run))
	require.NotEmpty(t, run.ID)
	return repo, run
}

func rebuildChunkResult(id string, chunkType types.ChunkType, classification string) *types.KnowledgeRebuildChunkResult {
	return &types.KnowledgeRebuildChunkResult{
		ChunkID:             id,
		ChunkType:           chunkType,
		Classification:      classification,
		ContentFingerprint:  "content-" + id,
		MetadataFingerprint: "metadata-" + id,
	}
}

func TestKnowledgeRebuildChunkResultsReplaceAndUpsert(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 7, "knowledge-1")
	ctx := context.Background()

	require.NoError(t, repo.ReplaceChunkResults(ctx, 7, run.ID, []*types.KnowledgeRebuildChunkResult{
		rebuildChunkResult("unchanged", types.ChunkTypeText, types.RebuildChunkClassUnchanged),
		rebuildChunkResult("metadata", types.ChunkTypeText, types.RebuildChunkClassMetadataOnly),
		rebuildChunkResult("changed", types.ChunkTypeParentText, types.RebuildChunkClassChangedNew),
		rebuildChunkResult("image", types.ChunkTypeImageOCR, types.RebuildChunkClassStale),
	}))

	saved, err := repo.Get(ctx, 7, run.ID)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, types.RebuildRunStatusChunksClassified, saved.Status)
	assert.Equal(t, 3, saved.CandidateChunks)
	assert.Equal(t, 1, saved.UnchangedChunks)
	assert.Equal(t, 1, saved.MetadataOnlyChunks)
	assert.Equal(t, 1, saved.ChangedNewChunks)
	assert.Equal(t, 1, saved.StaleChunks)
	assert.NotNil(t, saved.ChunkDiffReadyAt)

	// The image worker replaces the base pass's provisional stale result once
	// the OCR/caption candidate for that image has been reconstructed.
	require.NoError(t, repo.UpsertChunkResults(ctx, 7, run.ID, []*types.KnowledgeRebuildChunkResult{
		rebuildChunkResult("image", types.ChunkTypeImageOCR, types.RebuildChunkClassUnchanged),
	}))

	saved, err = repo.Get(ctx, 7, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 4, saved.CandidateChunks)
	assert.Equal(t, 2, saved.UnchangedChunks)
	assert.Equal(t, 0, saved.StaleChunks)
	assert.Equal(t, types.RebuildRunStatusChunksClassified, saved.Status)

	filtered, err := repo.ListChunkResults(
		ctx,
		7,
		run.ID,
		[]string{types.RebuildChunkClassUnchanged},
		[]types.ChunkType{types.ChunkTypeImageOCR},
	)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "image", filtered[0].ChunkID)
	assert.Equal(t, types.RebuildChunkClassUnchanged, filtered[0].Classification)
}

func TestKnowledgeRebuildChunkResultsEmptyReplace(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 9, "knowledge-empty")
	ctx := context.Background()

	require.NoError(t, repo.ReplaceChunkResults(ctx, 9, run.ID, nil))
	saved, err := repo.Get(ctx, 9, run.ID)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, types.RebuildRunStatusChunksClassified, saved.Status)
	assert.Zero(t, saved.CandidateChunks)
	assert.Zero(t, saved.UnchangedChunks)
	assert.Zero(t, saved.MetadataOnlyChunks)
	assert.Zero(t, saved.ChangedNewChunks)
	assert.Zero(t, saved.StaleChunks)
}

func TestKnowledgeRebuildArtifactFinalizeAndCommit(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 11, "knowledge-artifacts")
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET parse_status = ?, pending_subtasks_count = ? WHERE id = ?",
		types.ParseStatusFinalizing, 3, run.KnowledgeID,
	).Error)
	require.NoError(t, repo.BeginArtifacts(ctx, 11, run.ID, 2, true, false))

	inserted, err := repo.FinalizeArtifact(
		ctx, 11, run.ID, run.KnowledgeID,
		types.RebuildArtifactStageSummary, "summary", true, "",
	)
	require.NoError(t, err)
	assert.True(t, inserted)
	inserted, err = repo.FinalizeArtifact(
		ctx, 11, run.ID, run.KnowledgeID,
		types.RebuildArtifactStageSummary, "summary", true, "",
	)
	require.NoError(t, err)
	assert.False(t, inserted, "duplicate artifact must not drain twice")
	inserted, err = repo.FinalizeArtifact(
		ctx, 11, run.ID, run.KnowledgeID,
		types.RebuildArtifactStageGraph, "chunk-1", true, "",
	)
	require.NoError(t, err)
	assert.True(t, inserted)

	var knowledgeState struct {
		ParseStatus          string
		PendingSubtasksCount int
	}
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusFinalizing, knowledgeState.ParseStatus)
	assert.Equal(t, 1, knowledgeState.PendingSubtasksCount)

	saved, err := repo.Get(ctx, 11, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, saved.ArtifactsCompleted)
	assert.Zero(t, saved.ArtifactsFailed)

	require.NoError(t, repo.MarkStaleCleanupComplete(ctx, 11, run.ID))
	promoted, err := repo.FinalizeCommit(ctx, 11, run.ID, run.KnowledgeID)
	require.NoError(t, err)
	assert.True(t, promoted)
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusCompleted, knowledgeState.ParseStatus)
	assert.Zero(t, knowledgeState.PendingSubtasksCount)
	saved, err = repo.Get(ctx, 11, run.ID)
	require.NoError(t, err)
	assert.Equal(t, types.RebuildRunStatusCompleted, saved.Status)
	assert.NotNil(t, saved.CommitCompletedAt)
}

func TestKnowledgeRebuildCommitWaitsForWiki(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 12, "knowledge-wiki")
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET parse_status = ?, pending_subtasks_count = ? WHERE id = ?",
		types.ParseStatusFinalizing, 2, run.KnowledgeID,
	).Error)
	require.NoError(t, repo.BeginArtifacts(ctx, 12, run.ID, 0, false, true))
	require.NoError(t, repo.MarkStaleCleanupComplete(ctx, 12, run.ID))
	require.NoError(t, repo.MarkWikiReduceEnqueued(ctx, 12, run.ID))

	promoted, err := repo.FinalizeCommit(ctx, 12, run.ID, run.KnowledgeID)
	require.NoError(t, err)
	assert.False(t, promoted)
	var knowledgeState struct {
		ParseStatus          string
		PendingSubtasksCount int
	}
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusFinalizing, knowledgeState.ParseStatus)
	assert.Equal(t, 1, knowledgeState.PendingSubtasksCount)

	promoted, err = repo.FinalizeWiki(ctx, 12, run.ID, run.KnowledgeID, true, "")
	require.NoError(t, err)
	assert.True(t, promoted)
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusCompleted, knowledgeState.ParseStatus)
	assert.Zero(t, knowledgeState.PendingSubtasksCount)
	saved, err := repo.Get(ctx, 12, run.ID)
	require.NoError(t, err)
	assert.Equal(t, types.RebuildRunStatusCompleted, saved.Status)
	assert.NotNil(t, saved.WikiCompletedAt)
}

func TestKnowledgeRebuildWikiCanFinishBeforeCommit(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 14, "knowledge-wiki-first")
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET parse_status = ?, pending_subtasks_count = ? WHERE id = ?",
		types.ParseStatusFinalizing, 2, run.KnowledgeID,
	).Error)
	require.NoError(t, repo.BeginArtifacts(ctx, 14, run.ID, 0, false, true))
	require.NoError(t, repo.MarkStaleCleanupComplete(ctx, 14, run.ID))
	require.NoError(t, repo.MarkWikiReduceEnqueued(ctx, 14, run.ID))

	promoted, err := repo.FinalizeWiki(ctx, 14, run.ID, run.KnowledgeID, true, "")
	require.NoError(t, err)
	assert.False(t, promoted)
	var knowledgeState struct {
		ParseStatus          string
		PendingSubtasksCount int
	}
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusFinalizing, knowledgeState.ParseStatus)
	assert.Equal(t, 1, knowledgeState.PendingSubtasksCount)

	promoted, err = repo.FinalizeCommit(ctx, 14, run.ID, run.KnowledgeID)
	require.NoError(t, err)
	assert.True(t, promoted)
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusCompleted, knowledgeState.ParseStatus)
	assert.Zero(t, knowledgeState.PendingSubtasksCount)
	saved, err := repo.Get(ctx, 14, run.ID)
	require.NoError(t, err)
	assert.Equal(t, types.RebuildRunStatusCompleted, saved.Status)
	assert.NotNil(t, saved.WikiCompletedAt)
	assert.NotNil(t, saved.CommitCompletedAt)
}

func TestKnowledgeRebuildWikiFailureCannotBeOverwrittenByCommit(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 15, "knowledge-wiki-failed-first")
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET parse_status = ?, pending_subtasks_count = ? WHERE id = ?",
		types.ParseStatusFinalizing, 2, run.KnowledgeID,
	).Error)
	require.NoError(t, repo.BeginArtifacts(ctx, 15, run.ID, 0, false, true))

	promoted, err := repo.FinalizeWiki(ctx, 15, run.ID, run.KnowledgeID, false, "wiki failed")
	require.NoError(t, err)
	assert.False(t, promoted)
	promoted, err = repo.FinalizeCommit(ctx, 15, run.ID, run.KnowledgeID)
	require.NoError(t, err)
	assert.False(t, promoted)

	var knowledgeState struct {
		ParseStatus          string
		PendingSubtasksCount int
		ErrorMessage         string
	}
	require.NoError(t, db.Table("knowledges").Where("id = ?", run.KnowledgeID).Take(&knowledgeState).Error)
	assert.Equal(t, types.ParseStatusFailed, knowledgeState.ParseStatus)
	assert.Zero(t, knowledgeState.PendingSubtasksCount)
	assert.Equal(t, "wiki failed", knowledgeState.ErrorMessage)
	saved, err := repo.Get(ctx, 15, run.ID)
	require.NoError(t, err)
	assert.Equal(t, types.RebuildRunStatusFailed, saved.Status)
	assert.Nil(t, saved.CommitCompletedAt)
}

func TestKnowledgeRebuildArtifactFailureIsCounted(t *testing.T) {
	db := setupKnowledgeRebuildTestDB(t)
	repo, run := startKnowledgeRebuildTestRun(t, db, 13, "knowledge-failed-artifact")
	ctx := context.Background()
	require.NoError(t, db.Exec(
		"UPDATE knowledges SET parse_status = ?, pending_subtasks_count = ? WHERE id = ?",
		types.ParseStatusFinalizing, 2, run.KnowledgeID,
	).Error)
	require.NoError(t, repo.BeginArtifacts(ctx, 13, run.ID, 1, true, false))
	_, err := repo.FinalizeArtifact(
		ctx, 13, run.ID, run.KnowledgeID,
		types.RebuildArtifactStageSummary, "summary", false, "summary failed",
	)
	require.NoError(t, err)
	saved, err := repo.Get(ctx, 13, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, saved.ArtifactsCompleted)
	assert.Equal(t, 1, saved.ArtifactsFailed)
}
