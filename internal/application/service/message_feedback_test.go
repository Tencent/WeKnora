package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

func newMessageFeedbackTestService(t *testing.T) (*messageFeedbackService, *gorm.DB, context.Context) {
	return newMessageFeedbackTestServiceWithConfig(t, nil)
}

func newMessageFeedbackTestServiceWithConfig(t *testing.T, cfg *config.Config) (*messageFeedbackService, *gorm.DB, context.Context) {
	t.Helper()

	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.Session{},
		&types.Chunk{},
		&types.MessageChunkRef{},
		&types.MessageFeedback{},
		&types.ChunkWeightLog{},
	))
	require.NoError(t, db.Exec(`
		CREATE TABLE messages (
			id VARCHAR(36) PRIMARY KEY,
			session_id VARCHAR(36),
			request_id TEXT,
			content TEXT,
			role TEXT,
			knowledge_references TEXT,
			agent_steps TEXT,
			mentioned_items TEXT,
			images TEXT,
			attachments TEXT,
			is_completed BOOLEAN,
			is_fallback BOOLEAN,
			agent_duration_ms INTEGER DEFAULT 0,
			rendered_content TEXT DEFAULT '',
			channel VARCHAR(50) DEFAULT '',
			knowledge_id VARCHAR(36),
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		)
	`).Error)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")

	return NewMessageFeedbackService(
		repository.NewMessageFeedbackRepository(db, cfg),
		repository.NewMessageRepository(db),
		repository.NewSessionRepository(db),
		repository.NewChunkRepository(db),
	).(*messageFeedbackService), db, ctx
}

func createFeedbackFixture(t *testing.T, db *gorm.DB) (*types.Session, *types.Message, *types.Chunk) {
	t.Helper()

	session := &types.Session{TenantID: 1, UserID: "alice", Title: "feedback"}
	require.NoError(t, db.Create(session).Error)

	chunk := &types.Chunk{
		ID:              "chunk-1",
		TenantID:        2,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		Content:         "chunk content",
		ChunkType:       types.ChunkTypeText,
		RecallWeight:    1.0,
	}
	require.NoError(t, db.Create(chunk).Error)

	message := &types.Message{
		SessionID:   session.ID,
		Content:     "answer",
		Role:        "assistant",
		IsCompleted: true,
		KnowledgeReferences: types.References{
			{
				ID:              chunk.ID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeID:     chunk.KnowledgeID,
				ChunkType:       string(types.ChunkTypeText),
				Score:           0.9,
			},
		},
	}
	require.NoError(t, db.Create(message).Error)

	return session, message, chunk
}

func TestSetMessageFeedbackSwitchesAndClearsDislikeReason(t *testing.T) {
	svc, db, ctx := newMessageFeedbackTestService(t)
	session, message, chunk := createFeedbackFixture(t, db)

	_, err := svc.SetMessageFeedback(ctx, session.ID, message.ID, types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeDislike,
		ReasonCode:   "bad_reference",
		ReasonText:   "wrong chunk",
	})
	require.NoError(t, err)

	stats, err := svc.GetChunkFeedbackStats(ctx, chunk.ID)
	require.NoError(t, err)
	require.EqualValues(t, 0, stats.LikeCount)
	require.EqualValues(t, 1, stats.DislikeCount)
	require.NotNil(t, stats.PositiveRate)
	require.Equal(t, 0.0, *stats.PositiveRate)
	require.Len(t, stats.ReasonStats, 1)
	require.Equal(t, "bad_reference", stats.ReasonStats[0].ReasonCode)

	_, err = svc.SetMessageFeedback(ctx, session.ID, message.ID, types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeLike,
		ReasonCode:   "bad_reference",
		ReasonText:   "must be cleared",
	})
	require.NoError(t, err)

	var feedback types.MessageFeedback
	require.NoError(t, db.First(&feedback, "session_tenant_id = ? AND user_id = ? AND message_id = ?", 1, "alice", message.ID).Error)
	require.Equal(t, types.FeedbackTypeLike, feedback.FeedbackType)
	require.Empty(t, feedback.ReasonCode)
	require.Empty(t, feedback.ReasonText)

	stats, err = svc.GetChunkFeedbackStats(ctx, chunk.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.LikeCount)
	require.EqualValues(t, 0, stats.DislikeCount)
	require.NotNil(t, stats.PositiveRate)
	require.Equal(t, 1.0, *stats.PositiveRate)

	_, err = svc.SetMessageFeedback(ctx, session.ID, message.ID, types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeNone,
		ReasonCode:   "bad_reference",
		ReasonText:   "must also be cleared",
	})
	require.NoError(t, err)
	require.NoError(t, db.First(&feedback, "session_tenant_id = ? AND user_id = ? AND message_id = ?", 1, "alice", message.ID).Error)
	require.Equal(t, types.FeedbackTypeNone, feedback.FeedbackType)
	require.Empty(t, feedback.ReasonCode)
	require.Empty(t, feedback.ReasonText)

	stats, err = svc.GetChunkFeedbackStats(ctx, chunk.ID)
	require.NoError(t, err)
	require.EqualValues(t, 0, stats.LikeCount)
	require.EqualValues(t, 0, stats.DislikeCount)
	require.Nil(t, stats.PositiveRate)
	require.Equal(t, 1.0, stats.RecallWeight)
}

func TestSetMessageFeedbackRejectsLongReasonCode(t *testing.T) {
	svc, db, ctx := newMessageFeedbackTestService(t)
	session, message, _ := createFeedbackFixture(t, db)

	_, err := svc.SetMessageFeedback(ctx, session.ID, message.ID, types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeDislike,
		ReasonCode:   strings.Repeat("a", maxFeedbackReasonCodeRunes+1),
	})
	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok)
	require.Equal(t, apperrors.ErrBadRequest, appErr.Code)

	var count int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestResetChunkFeedbackIgnoresOldFeedbackEpoch(t *testing.T) {
	svc, db, ctx := newMessageFeedbackTestService(t)
	session, message, chunk := createFeedbackFixture(t, db)

	_, err := svc.SetMessageFeedback(ctx, session.ID, message.ID, types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeDislike,
		ReasonCode:   "wrong_answer",
	})
	require.NoError(t, err)

	resetStats, err := svc.ResetChunkFeedback(ctx, chunk.ID)
	require.NoError(t, err)
	require.EqualValues(t, 0, resetStats.LikeCount)
	require.EqualValues(t, 0, resetStats.DislikeCount)
	require.Nil(t, resetStats.PositiveRate)
	require.Equal(t, 1.0, resetStats.RecallWeight)
	require.NotNil(t, resetStats.FeedbackResetAt)

	logs, err := svc.GetChunkWeightLogs(ctx, chunk.ID, 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, types.ChunkWeightLogSourceAdminReset, logs[0].Source)

	beforeResetFeedbackAt := resetStats.FeedbackResetAt.Add(-time.Second)
	require.NoError(t, db.Model(&types.MessageFeedback{}).
		Where("session_tenant_id = ? AND user_id = ? AND message_id = ?", 1, "alice", message.ID).
		Update("feedback_at", beforeResetFeedbackAt).Error)

	_, err = svc.SetMessageFeedback(ctx, session.ID, message.ID, types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeNone,
	})
	require.NoError(t, err)

	stats, err := svc.GetChunkFeedbackStats(ctx, chunk.ID)
	require.NoError(t, err)
	require.EqualValues(t, 0, stats.LikeCount)
	require.EqualValues(t, 0, stats.DislikeCount)
	require.Nil(t, stats.PositiveRate)
	require.Equal(t, 1.0, stats.RecallWeight)
}

func TestMessageFeedbackUsesConfiguredWeightRules(t *testing.T) {
	cfg := &config.Config{MessageFeedback: &config.MessageFeedbackConfig{
		MinFeedbackCount:               3,
		BoostPositiveRateThreshold:     0.6,
		NeutralPositiveRateThreshold:   0.4,
		NeedsOptimizationRateThreshold: 0.2,
		BoostRecallWeight:              1.5,
		NeutralRecallWeight:            1.0,
		PenaltyRecallWeight:            0.6,
	}}
	svc, db, ctx := newMessageFeedbackTestServiceWithConfig(t, cfg)
	session, message1, chunk := createFeedbackFixture(t, db)
	message2 := createAssistantMessageForChunk(t, db, session.ID, chunk)
	message3 := createAssistantMessageForChunk(t, db, session.ID, chunk)

	_, err := svc.SetMessageFeedback(ctx, session.ID, message1.ID, types.MessageFeedbackRequest{FeedbackType: types.FeedbackTypeLike})
	require.NoError(t, err)
	_, err = svc.SetMessageFeedback(ctx, session.ID, message2.ID, types.MessageFeedbackRequest{FeedbackType: types.FeedbackTypeLike})
	require.NoError(t, err)
	_, err = svc.SetMessageFeedback(ctx, session.ID, message3.ID, types.MessageFeedbackRequest{FeedbackType: types.FeedbackTypeDislike})
	require.NoError(t, err)

	stats, err := svc.GetChunkFeedbackStats(ctx, chunk.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.LikeCount)
	require.EqualValues(t, 1, stats.DislikeCount)
	require.NotNil(t, stats.PositiveRate)
	require.InDelta(t, 2.0/3.0, *stats.PositiveRate, 0.000001)
	require.Equal(t, 1.5, stats.RecallWeight)
}

func createAssistantMessageForChunk(t *testing.T, db *gorm.DB, sessionID string, chunk *types.Chunk) *types.Message {
	t.Helper()
	message := &types.Message{
		SessionID:   sessionID,
		Content:     "answer",
		Role:        "assistant",
		IsCompleted: true,
		KnowledgeReferences: types.References{
			{
				ID:              chunk.ID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeID:     chunk.KnowledgeID,
				ChunkType:       string(types.ChunkTypeText),
				Score:           0.9,
			},
		},
	}
	require.NoError(t, db.Create(message).Error)
	return message
}
