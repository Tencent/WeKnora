package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type feedbackMessageStub struct {
	interfaces.MessageService
	message *types.Message
}

func (s *feedbackMessageStub) GetMessage(context.Context, string, string) (*types.Message, error) {
	return s.message, nil
}

func TestMessageFeedbackAttributesVotesAndReset(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{}, &types.MessageChunkRef{}, &types.MessageFeedback{}, &types.ChunkWeightLog{},
	))

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})

	good := &types.Chunk{
		ID: uuid.NewString(), TenantID: 7, KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1",
		Content: "good", ChunkType: types.ChunkTypeText, IsEnabled: true, RecallWeight: 1,
	}
	poor := &types.Chunk{
		ID: uuid.NewString(), TenantID: 7, KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1",
		Content: "poor", ChunkType: types.ChunkTypeText, IsEnabled: true, RecallWeight: 1, ChunkIndex: 1,
	}
	require.NoError(t, db.Create(good).Error)
	require.NoError(t, db.Create(poor).Error)

	message := &types.Message{
		ID: "msg-1", SessionID: "sess-1", Role: "assistant", IsCompleted: true,
		KnowledgeReferences: types.References{
			{ID: good.ID, KnowledgeID: good.KnowledgeID, KnowledgeBaseID: good.KnowledgeBaseID, Score: 0.9},
			{ID: poor.ID, KnowledgeID: poor.KnowledgeID, KnowledgeBaseID: poor.KnowledgeBaseID, Score: 0.4},
			{ID: "web-1", KnowledgeID: "web", MatchType: types.MatchTypeWebSearch, KnowledgeSource: "web_search"},
		},
	}
	repo := repository.NewMessageFeedbackRepository(db)
	chunks := repository.NewChunkRepository(db)
	created := NewMessageFeedbackService(repo, chunks, &feedbackMessageStub{message: message}, nil)
	svc := created.(*messageFeedbackService)
	svc.policy.MinFeedbackCount = 1

	require.NoError(t, svc.RecordAnswerChunks(ctx, message))
	var refCount int64
	require.NoError(t, db.Model(&types.MessageChunkRef{}).Count(&refCount).Error)
	require.Equal(t, int64(2), refCount)
	require.NoError(t, svc.RecordAnswerChunks(ctx, message))
	require.NoError(t, db.Model(&types.MessageChunkRef{}).Count(&refCount).Error)
	require.Equal(t, int64(2), refCount)

	_, err = svc.Submit(ctx, "sess-1", "msg-1", &types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeDislike,
		ReasonCode:   types.FeedbackReasonOutdated,
		ReasonText:   "figures are from last year",
	})
	require.NoError(t, err)

	var storedPoor types.Chunk
	require.NoError(t, db.First(&storedPoor, "id = ?", poor.ID).Error)
	require.Equal(t, int64(0), storedPoor.LikeCount)
	require.Equal(t, int64(1), storedPoor.DislikeCount)
	require.NotNil(t, storedPoor.PositiveRate)
	require.Equal(t, 0.0, *storedPoor.PositiveRate)
	require.Equal(t, 0.8, storedPoor.RecallWeight)
	require.True(t, storedPoor.NeedsOptimization)

	view, err := svc.Submit(ctx, "sess-1", "msg-1", &types.MessageFeedbackRequest{FeedbackType: types.FeedbackTypeLike})
	require.NoError(t, err)
	require.Equal(t, types.FeedbackTypeLike, view.FeedbackType)
	require.NoError(t, db.First(&storedPoor, "id = ?", poor.ID).Error)
	require.Equal(t, int64(1), storedPoor.LikeCount)
	require.Equal(t, int64(0), storedPoor.DislikeCount)
	require.Equal(t, 1.2, storedPoor.RecallWeight)
	require.False(t, storedPoor.NeedsOptimization)

	view, err = svc.Submit(ctx, "sess-1", "msg-1", &types.MessageFeedbackRequest{FeedbackType: types.FeedbackTypeNone})
	require.NoError(t, err)
	require.Equal(t, types.FeedbackTypeNone, view.FeedbackType)
	require.NoError(t, db.First(&storedPoor, "id = ?", poor.ID).Error)
	require.Equal(t, int64(0), storedPoor.LikeCount)
	require.Equal(t, int64(0), storedPoor.DislikeCount)
	require.Nil(t, storedPoor.PositiveRate)
	require.Equal(t, 1.0, storedPoor.RecallWeight)

	_, err = svc.Submit(ctx, "sess-1", "msg-1", &types.MessageFeedbackRequest{
		FeedbackType: types.FeedbackTypeDislike,
		ReasonCode:   types.FeedbackReasonIncomplete,
		ReasonText:   "missing steps",
	})
	require.NoError(t, err)

	stats, err := svc.ChunkStats(ctx, poor.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.DislikeCount)
	require.Equal(t, int64(1), stats.AssociatedSessionCount)
	require.Equal(t, "sess-1", stats.Sessions[0].SessionID)
	require.Equal(t, types.FeedbackReasonIncomplete, stats.ReasonStats[0].ReasonCode)
	require.Equal(t, []string{"missing steps"}, stats.RecentReasons)

	logs, err := svc.ListWeightLogs(ctx, poor.ID, 20)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(logs), 2)
	require.Equal(t, types.ChunkWeightLogSourceFeedback, logs[0].Source)

	low, total, err := repo.ListChunksByFeedback(ctx, 7, "doc-1", &types.Pagination{Page: 1, PageSize: 10},
		[]types.ChunkType{types.ChunkTypeText}, types.ChunkFeedbackFilter{MaxPositiveRate: floatPtr(0.5)})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, low, 2)

	reset, err := svc.ResetChunk(ctx, poor.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), reset.LikeCount)
	require.Equal(t, int64(0), reset.DislikeCount)
	require.Equal(t, 1.0, reset.RecallWeight)
	require.False(t, reset.NeedsOptimization)
	require.NotNil(t, reset.FeedbackResetAt)

	stats, err = svc.ChunkStats(ctx, poor.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), stats.DislikeCount)
	require.Empty(t, stats.ReasonStats)
	require.Equal(t, int64(1), stats.AssociatedSessionCount)
	low, total, err = repo.ListChunksByFeedback(ctx, 7, "doc-1", &types.Pagination{Page: 1, PageSize: 10},
		[]types.ChunkType{types.ChunkTypeText}, types.ChunkFeedbackFilter{MaxPositiveRate: floatPtr(0.5)})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, good.ID, low[0].ID)

	logs, err = svc.ListWeightLogs(ctx, poor.ID, 20)
	require.NoError(t, err)
	require.Equal(t, types.ChunkWeightLogSourceAdminReset, logs[0].Source)

	messages := []*types.Message{message}
	svc.Attach(ctx, messages)
	require.Equal(t, types.FeedbackTypeDislike, messages[0].Feedback.FeedbackType)

	// A vote from before the reset must not come back until the user votes again.
	time.Sleep(time.Millisecond)
	_, err = svc.Submit(ctx, "sess-1", "msg-1", &types.MessageFeedbackRequest{FeedbackType: types.FeedbackTypeLike})
	require.NoError(t, err)
	require.NoError(t, db.First(&storedPoor, "id = ?", poor.ID).Error)
	require.Equal(t, int64(1), storedPoor.LikeCount)
	require.Equal(t, 1.2, storedPoor.RecallWeight)
}

func floatPtr(v float64) *float64 { return &v }
