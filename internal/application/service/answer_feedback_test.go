package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newAnswerFeedbackTestService(t *testing.T, policy types.ChunkFeedbackConfig) (*answerFeedbackService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Session{},
		&types.Message{},
		&types.Chunk{},
		&types.Knowledge{},
		&types.AnswerChunkLink{},
		&types.AnswerFeedback{},
		&types.ChunkFeedbackAttribution{},
		&types.ChunkWeightLog{},
	))
	svc := &answerFeedbackService{
		db:       db,
		sessions: repository.NewSessionRepository(db),
		policy:   func(context.Context, uint64) types.ChunkFeedbackConfig { return policy },
	}
	return svc, db
}

func feedbackContext(tenantID uint64, userID string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return context.WithValue(ctx, types.UserIDContextKey, userID)
}

func TestAnswerFeedbackAttributesCountsWeightAndReset(t *testing.T) {
	svc, db := newAnswerFeedbackTestService(t, types.DefaultChunkFeedbackConfig())
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Title: "Handbook",
	}).Error)
	chunkA := &types.Chunk{
		ID: "chunk-a", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
		Content: "alpha", IsEnabled: true, RecallWeight: 1,
	}
	chunkB := &types.Chunk{
		ID: "chunk-b", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
		Content: "beta", IsEnabled: true, RecallWeight: 1,
	}
	require.NoError(t, db.Create(chunkA).Error)
	require.NoError(t, db.Create(chunkB).Error)

	sessionA := &types.Session{TenantID: 1, UserID: "user-a", Title: "qa-a"}
	sessionB := &types.Session{TenantID: 1, UserID: "user-b", Title: "qa-b"}
	require.NoError(t, db.Create(sessionA).Error)
	require.NoError(t, db.Create(sessionB).Error)
	both := &types.Message{
		SessionID:   sessionA.ID,
		Role:        "assistant",
		Content:     "both chunks",
		IsCompleted: true,
		KnowledgeReferences: types.References{
			{ID: "chunk-a", KnowledgeID: "doc", KnowledgeBaseID: "kb", MatchType: types.MatchTypeEmbedding},
			{ID: "chunk-b", KnowledgeID: "doc", KnowledgeBaseID: "kb", MatchType: types.MatchTypeKeywords},
			{ID: "nearby", KnowledgeID: "doc", KnowledgeBaseID: "kb", MatchType: types.MatchTypeNearByChunk},
		},
	}
	onlyA := &types.Message{
		SessionID:   sessionB.ID,
		Role:        "assistant",
		Content:     "only a",
		IsCompleted: true,
		KnowledgeReferences: types.References{
			{ID: "chunk-a", KnowledgeID: "doc", KnowledgeBaseID: "kb", MatchType: types.MatchTypeEmbedding},
		},
	}
	require.NoError(t, db.Create(both).Error)
	require.NoError(t, db.Create(onlyA).Error)

	ctxA := feedbackContext(1, "user-a")
	liked, err := svc.Submit(ctxA, sessionA.ID, both.ID, "like", "")
	require.NoError(t, err)
	require.Equal(t, types.AnswerFeedbackLike, liked.Rating)
	require.Equal(t, 2, liked.ChunkCount)

	var links int64
	require.NoError(t, db.Model(&types.AnswerChunkLink{}).Where("message_id = ?", both.ID).Count(&links).Error)
	require.Equal(t, int64(2), links)

	requireChunkStats(t, db, "chunk-a", 1, 0, 1, 1.2, false)
	requireChunkStats(t, db, "chunk-b", 1, 0, 1, 1.2, false)

	ctxB := feedbackContext(1, "user-b")
	disliked, err := svc.Submit(ctxB, sessionB.ID, onlyA.ID, "dislike", "outdated figure")
	require.NoError(t, err)
	require.Equal(t, 1, disliked.ChunkCount)
	requireChunkStats(t, db, "chunk-a", 1, 1, 0.5, 1, false)
	requireChunkStats(t, db, "chunk-b", 1, 0, 1, 1.2, false)

	_, err = svc.Submit(ctxA, sessionA.ID, both.ID, "none", "")
	require.NoError(t, err)
	requireChunkStats(t, db, "chunk-a", 0, 1, 0, 0.8, true)
	requireChunkStats(t, db, "chunk-b", 0, 0, 0, 1, false)

	low, err := svc.ListChunkFeedback(ctxA, "kb", types.ChunkFeedbackListFilter{LowQuality: true})
	require.NoError(t, err)
	require.Equal(t, int64(1), low.Total)
	require.Equal(t, "chunk-a", low.Items[0].ChunkID)
	require.Equal(t, "Handbook", low.Items[0].KnowledgeTitle)
	require.Equal(t, 2, low.Items[0].SessionCount)

	detail, err := svc.GetChunkFeedback(ctxA, "kb", "chunk-a")
	require.NoError(t, err)
	require.Equal(t, []types.ChunkFeedbackReason{{Reason: "outdated figure", Count: 1}}, detail.DislikeReasons)
	require.NotEmpty(t, detail.WeightLogs)

	reset, err := svc.ResetChunkFeedback(ctxA, "kb", "chunk-a")
	require.NoError(t, err)
	require.Equal(t, 0, reset.LikeCount)
	require.Equal(t, 0, reset.DislikeCount)
	require.Nil(t, reset.ApprovalRate)
	require.Equal(t, 1.0, reset.RecallWeight)
	require.False(t, reset.NeedsOptimization)
	require.Equal(t, types.ChunkWeightTriggerReset, reset.WeightLogs[0].TriggerSource)
	require.Empty(t, reset.DislikeReasons)

	var attrs int64
	require.NoError(t, db.Model(&types.ChunkFeedbackAttribution{}).Where("chunk_id = ?", "chunk-a").Count(&attrs).Error)
	require.Zero(t, attrs)

	reliked, err := svc.Submit(ctxB, sessionB.ID, onlyA.ID, "like", "")
	require.NoError(t, err)
	require.Equal(t, types.AnswerFeedbackLike, reliked.Rating)
	requireChunkStats(t, db, "chunk-a", 1, 0, 1, 1.2, false)

	echo, err := svc.ListForMessages(ctxB, []string{onlyA.ID, both.ID})
	require.NoError(t, err)
	require.Equal(t, types.AnswerFeedbackLike, echo[onlyA.ID].Rating)
	require.NotContains(t, echo, both.ID)
}

func TestAnswerFeedbackDisabledPolicyKeepsWeight(t *testing.T) {
	policy := types.DefaultChunkFeedbackConfig()
	policy.Enabled = false
	svc, db := newAnswerFeedbackTestService(t, policy)
	require.NoError(t, db.Create(&types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
		Content: "text", IsEnabled: true, RecallWeight: 1,
	}).Error)
	session := &types.Session{TenantID: 1, UserID: "user-a", Title: "qa"}
	require.NoError(t, db.Create(session).Error)
	message := &types.Message{
		SessionID: session.ID, Role: "assistant", Content: "answer", IsCompleted: true,
		KnowledgeReferences: types.References{{ID: "chunk", KnowledgeID: "doc", KnowledgeBaseID: "kb"}},
	}
	require.NoError(t, db.Create(message).Error)
	_, err := svc.Submit(feedbackContext(1, "user-a"), session.ID, message.ID, "dislike", "wrong")
	require.NoError(t, err)
	requireChunkStats(t, db, "chunk", 0, 1, 0, 1, true)
	var logs int64
	require.NoError(t, db.Model(&types.ChunkWeightLog{}).Count(&logs).Error)
	require.Zero(t, logs)
}

func TestRecordAnswerChunksIsIdempotent(t *testing.T) {
	svc, db := newAnswerFeedbackTestService(t, types.DefaultChunkFeedbackConfig())
	ctx := feedbackContext(1, "user-a")
	refs := []types.AnswerChunkRef{{ChunkID: "c1", KnowledgeID: "d", KnowledgeBaseID: "kb"}}
	require.NoError(t, svc.RecordAnswerChunks(ctx, "s", "m", refs))
	require.NoError(t, svc.RecordAnswerChunks(ctx, "s", "m", refs))
	var n int64
	require.NoError(t, db.Model(&types.AnswerChunkLink{}).Count(&n).Error)
	require.Equal(t, int64(1), n)
}

func requireChunkStats(t *testing.T, db *gorm.DB, id string, likes, dislikes int, rate, weight float64, needs bool) {
	t.Helper()
	var chunk types.Chunk
	require.NoError(t, db.First(&chunk, "id = ?", id).Error)
	require.Equal(t, likes, chunk.LikeCount)
	require.Equal(t, dislikes, chunk.DislikeCount)
	require.Equal(t, needs, chunk.NeedsOptimization)
	require.InDelta(t, weight, chunk.RecallWeight, 1e-9)
	if likes+dislikes == 0 {
		require.Nil(t, chunk.ApprovalRate)
		return
	}
	require.NotNil(t, chunk.ApprovalRate)
	require.InDelta(t, rate, *chunk.ApprovalRate, 1e-9)
}
