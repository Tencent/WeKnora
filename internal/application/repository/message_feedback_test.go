package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupMessageFeedbackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{},
		&types.MessageFeedback{},
		&types.MessageChunkReference{},
		&types.ChunkFeedbackWeightLog{},
	))
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_message_feedbacks_unique
		ON message_feedbacks(tenant_id, message_id, user_id)
		WHERE deleted_at IS NULL
	`).Error)
	return db
}

func feedbackTestChunk(t *testing.T, db *gorm.DB) *types.Chunk {
	t.Helper()
	chunk := &types.Chunk{
		ID:              uuid.NewString(),
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		Content:         "chunk content",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		Status:          int(types.ChunkStatusIndexed),
		RecallWeight:    1,
	}
	require.NoError(t, db.Create(chunk).Error)
	return chunk
}

func feedbackTestRef(t *testing.T, db *gorm.DB, chunk *types.Chunk, sessionID, messageID string) {
	t.Helper()
	require.NoError(t, db.Create(&types.MessageChunkReference{
		TenantID:        chunk.TenantID,
		SessionID:       sessionID,
		MessageID:       messageID,
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
	}).Error)
}

func loadFeedbackTestChunk(t *testing.T, db *gorm.DB, id string) types.Chunk {
	t.Helper()
	var chunk types.Chunk
	require.NoError(t, db.First(&chunk, "id = ?", id).Error)
	return chunk
}

func TestMessageFeedbackRepository_AggregatesSwitchAndCancel(t *testing.T) {
	db := setupMessageFeedbackTestDB(t)
	repo := NewMessageFeedbackRepository(db)
	ctx := context.Background()
	cfg := types.DefaultChunkFeedbackConfig()
	chunk := feedbackTestChunk(t, db)
	feedbackTestRef(t, db, chunk, "session-1", "message-1")
	feedbackTestRef(t, db, chunk, "session-2", "message-2")

	_, err := repo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-1",
		MessageID: "message-1",
		UserID:    "user-1",
		Action:    types.FeedbackActionLike,
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)
	_, err = repo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-2",
		MessageID: "message-2",
		UserID:    "user-2",
		Action:    types.FeedbackActionLike,
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)

	saved := loadFeedbackTestChunk(t, db, chunk.ID)
	assert.Equal(t, int64(2), saved.LikeCount)
	assert.Equal(t, int64(0), saved.DislikeCount)
	require.NotNil(t, saved.PositiveRate)
	assert.InDelta(t, 1.0, *saved.PositiveRate, 0.0001)
	assert.InDelta(t, 1.2, saved.RecallWeight, 0.0001)

	_, err = repo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-2",
		MessageID: "message-2",
		UserID:    "user-2",
		Action:    types.FeedbackActionDislike,
		Reason:    "mismatch",
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)

	saved = loadFeedbackTestChunk(t, db, chunk.ID)
	assert.Equal(t, int64(1), saved.LikeCount)
	assert.Equal(t, int64(1), saved.DislikeCount)
	require.NotNil(t, saved.PositiveRate)
	assert.InDelta(t, 0.5, *saved.PositiveRate, 0.0001)
	assert.InDelta(t, 1.0, saved.RecallWeight, 0.0001)

	require.NoError(t, repo.DeleteFeedbackAndRefreshChunks(
		ctx, chunk.TenantID, "session-1", "message-1", "user-1", []string{chunk.ID}, cfg,
	))
	saved = loadFeedbackTestChunk(t, db, chunk.ID)
	assert.Equal(t, int64(0), saved.LikeCount)
	assert.Equal(t, int64(1), saved.DislikeCount)
	require.NotNil(t, saved.PositiveRate)
	assert.InDelta(t, 0.0, *saved.PositiveRate, 0.0001)
	assert.InDelta(t, 0.8, saved.RecallWeight, 0.0001)
	assert.True(t, saved.NeedsOptimization)
}

func TestMessageFeedbackRepository_ResetUsesBaseline(t *testing.T) {
	db := setupMessageFeedbackTestDB(t)
	feedbackRepo := NewMessageFeedbackRepository(db)
	chunkRepo := NewChunkRepository(db)
	ctx := context.Background()
	cfg := types.DefaultChunkFeedbackConfig()
	chunk := feedbackTestChunk(t, db)
	feedbackTestRef(t, db, chunk, "session-1", "message-1")
	feedbackTestRef(t, db, chunk, "session-2", "message-2")
	feedbackTestRef(t, db, chunk, "session-3", "message-3")

	for _, msg := range []string{"message-1", "message-2"} {
		_, err := feedbackRepo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
			TenantID:  chunk.TenantID,
			SessionID: "session-" + msg[len(msg)-1:],
			MessageID: msg,
			UserID:    "user-" + msg[len(msg)-1:],
			Action:    types.FeedbackActionLike,
		}, []string{chunk.ID}, cfg)
		require.NoError(t, err)
	}

	resetChunk, err := chunkRepo.ResetChunkFeedback(ctx, chunk.TenantID, chunk.ID, true, cfg)
	require.NoError(t, err)
	require.NotNil(t, resetChunk.FeedbackResetAt)
	assert.Equal(t, int64(0), resetChunk.LikeCount)
	assert.Equal(t, int64(0), resetChunk.DislikeCount)
	assert.Nil(t, resetChunk.PositiveRate)
	assert.InDelta(t, 1.0, resetChunk.RecallWeight, 0.0001)

	time.Sleep(10 * time.Millisecond)
	_, err = feedbackRepo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-3",
		MessageID: "message-3",
		UserID:    "user-3",
		Action:    types.FeedbackActionDislike,
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)

	saved := loadFeedbackTestChunk(t, db, chunk.ID)
	assert.Equal(t, int64(0), saved.LikeCount)
	assert.Equal(t, int64(1), saved.DislikeCount)
	require.NotNil(t, saved.PositiveRate)
	assert.InDelta(t, 0.0, *saved.PositiveRate, 0.0001)

	var feedbackCount int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).Count(&feedbackCount).Error)
	assert.Equal(t, int64(3), feedbackCount, "reset should establish a baseline without deleting audit history")
}

func TestChunkRepository_FeedbackStatsFilterDeletedRefsAndAggregateReasons(t *testing.T) {
	db := setupMessageFeedbackTestDB(t)
	feedbackRepo := NewMessageFeedbackRepository(db)
	chunkRepo := NewChunkRepository(db)
	ctx := context.Background()
	cfg := types.DefaultChunkFeedbackConfig()
	chunk := feedbackTestChunk(t, db)
	feedbackTestRef(t, db, chunk, "session-1", "message-1")
	feedbackTestRef(t, db, chunk, "session-2", "message-2")
	feedbackTestRef(t, db, chunk, "session-deleted", "message-deleted")

	_, err := feedbackRepo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-1",
		MessageID: "message-1",
		UserID:    "user-1",
		Action:    types.FeedbackActionDislike,
		Reason:    "mismatch",
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)
	_, err = feedbackRepo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-2",
		MessageID: "message-2",
		UserID:    "user-2",
		Action:    types.FeedbackActionDislike,
		Reason:    "mismatch",
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)
	_, err = feedbackRepo.UpsertFeedbackAndRefreshChunks(ctx, &types.MessageFeedback{
		TenantID:  chunk.TenantID,
		SessionID: "session-deleted",
		MessageID: "message-deleted",
		UserID:    "user-deleted",
		Action:    types.FeedbackActionDislike,
		Reason:    "mismatch",
	}, []string{chunk.ID}, cfg)
	require.NoError(t, err)
	require.NoError(t, db.Where("session_id = ?", "session-deleted").Delete(&types.MessageChunkReference{}).Error)

	chunks, total, err := chunkRepo.ListPagedChunksByKnowledgeID(
		ctx,
		chunk.TenantID,
		chunk.KnowledgeID,
		&types.Pagination{Page: 1, PageSize: 10},
		[]types.ChunkType{types.ChunkTypeText},
		nil,
		"",
		"",
		"",
		"",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, chunks, 1)
	assert.Equal(t, int64(2), chunks[0].FeedbackSessionCount)
	require.Len(t, chunks[0].DislikeReasons, 1)
	assert.Equal(t, "mismatch", chunks[0].DislikeReasons[0].Reason)
	assert.Equal(t, int64(2), chunks[0].DislikeReasons[0].Count)
}
