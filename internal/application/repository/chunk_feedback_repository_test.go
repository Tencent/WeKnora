package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupChunkFeedbackRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Session{},
		&types.Chunk{},
		&types.ChunkFeedback{},
		&types.QAReplyChunkRef{},
		&types.QAReplyChunkRefTombstone{},
		&types.ChunkWeightLog{},
	))
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id varchar(36) PRIMARY KEY,
			session_id varchar(36),
			role text,
			deleted_at datetime
		)
	`).Error)
	return db
}

func TestListLowQualityChunksUsesStrictThresholdAndKnowledgeBaseFilter(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	repo := NewChunkRepository(db)
	chunks := []types.Chunk{
		{ID: "below-threshold", TenantID: 7, KnowledgeBaseID: "kb-1", Content: "below", LikeCount: 49, DislikeCount: 51, PositiveRate: 0.49, RecallWeight: 0.5},
		{ID: "at-threshold", TenantID: 7, KnowledgeBaseID: "kb-1", Content: "at", LikeCount: 50, DislikeCount: 50, PositiveRate: 0.50, RecallWeight: 1.0},
		{ID: "perfect", TenantID: 7, KnowledgeBaseID: "kb-1", Content: "perfect", LikeCount: 3, PositiveRate: 1.0, RecallWeight: 1.5},
		{ID: "other-kb", TenantID: 7, KnowledgeBaseID: "kb-2", Content: "other", LikeCount: 1, DislikeCount: 9, PositiveRate: 0.10, RecallWeight: 0.5},
		{ID: "unrated", TenantID: 7, KnowledgeBaseID: "kb-1", Content: "unrated", PositiveRate: 0.0, RecallWeight: 1.0},
	}
	require.NoError(t, db.Create(&chunks).Error)

	filtered, err := repo.ListLowQualityChunks(ctx, 7, "kb-1", 0.5, 10, 0)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "below-threshold", filtered[0].ID)

	count, err := repo.CountLowQualityChunks(ctx, 7, "kb-1", 0.5)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	allRated, err := repo.ListLowQualityChunks(ctx, 7, "kb-1", 1.01, 10, 0)
	require.NoError(t, err)
	require.Len(t, allRated, 3)
	require.Equal(t, []string{"below-threshold", "at-threshold", "perfect"}, []string{allRated[0].ID, allRated[1].ID, allRated[2].ID})
}

func TestCountSessionsByChunkIDUsesDistinctSessionsAndChunkTenant(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	repo := NewQAReplyChunkRefRepository(db)

	session1 := &types.Session{TenantID: 7, UserID: "user-1"}
	session2 := &types.Session{TenantID: 7, UserID: "user-1"}
	require.NoError(t, db.Create(session1).Error)
	require.NoError(t, db.Create(session2).Error)
	messages := []string{"message-1", "message-2", "message-3"}
	require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role) VALUES (?, ?, ?)", messages[0], session1.ID, "assistant").Error)
	require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role) VALUES (?, ?, ?)", messages[1], session1.ID, "assistant").Error)
	require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role) VALUES (?, ?, ?)", messages[2], session2.ID, "assistant").Error)
	require.NoError(t, db.Create(&[]types.QAReplyChunkRef{
		{TenantID: 7, MessageID: messages[0], ChunkID: "shared-chunk", ChunkTenantID: 99},
		{TenantID: 7, MessageID: messages[1], ChunkID: "shared-chunk", ChunkTenantID: 99},
		{TenantID: 7, MessageID: messages[2], ChunkID: "shared-chunk", ChunkTenantID: 99},
		{TenantID: 7, MessageID: messages[2], ChunkID: "shared-chunk", ChunkTenantID: 7},
	}).Error)

	ownerCount, err := repo.CountSessionsByChunkID(ctx, 99, "shared-chunk")
	require.NoError(t, err)
	require.Equal(t, int64(2), ownerCount)

	consumerCount, err := repo.CountSessionsByChunkID(ctx, 7, "shared-chunk")
	require.NoError(t, err)
	require.Equal(t, int64(1), consumerCount)
}

func TestCreateResetTombstonesPreventsDuplicateMarks(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	repo := NewQAReplyChunkRefRepository(db)

	refs := []*types.QAReplyChunkRef{
		{TenantID: 7, MessageID: "message-1", ChunkID: "shared-chunk", ChunkTenantID: 99},
		{TenantID: 7, MessageID: "message-1", ChunkID: "shared-chunk", ChunkTenantID: 99},
	}
	require.NoError(t, repo.CreateResetTombstones(ctx, refs, "admin-1"))
	require.NoError(t, repo.CreateResetTombstones(ctx, refs, "admin-1"))

	tombstones, err := repo.GetResetTombstonesByMessageID(ctx, 7, "message-1")
	require.NoError(t, err)
	require.Len(t, tombstones, 1)
	require.Equal(t, "shared-chunk", tombstones[0].ChunkID)
	require.Equal(t, uint64(99), tombstones[0].ChunkTenantID)
	require.Equal(t, "admin-1", tombstones[0].Operator)
}

func TestGetDislikeReasonsExcludesResetAssociations(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	repo := NewChunkFeedbackRepository(db)

	require.NoError(t, db.Exec(`
		INSERT INTO messages (id, session_id, role)
		VALUES ('message-reset', 'session-1', 'assistant'),
		       ('message-active', 'session-2', 'assistant')
	`).Error)
	require.NoError(t, db.Create(&[]types.QAReplyChunkRef{
		{TenantID: 7, MessageID: "message-reset", ChunkID: "chunk-1", ChunkTenantID: 7},
		{TenantID: 7, MessageID: "message-active", ChunkID: "chunk-1", ChunkTenantID: 7},
	}).Error)
	require.NoError(t, db.Create(&[]types.ChunkFeedback{
		{TenantID: 7, MessageID: "message-reset", SessionID: "session-1", UserID: "user-1", IsPositive: false, DislikeReason: "reset reason"},
		{TenantID: 7, MessageID: "message-active", SessionID: "session-2", UserID: "user-1", IsPositive: false, DislikeReason: "active reason"},
	}).Error)
	require.NoError(t, db.Create(&types.QAReplyChunkRefTombstone{
		TenantID:      7,
		MessageID:     "message-reset",
		ChunkID:       "chunk-1",
		ChunkTenantID: 7,
		Operator:      "admin-1",
	}).Error)

	reasons, err := repo.GetDislikeReasonsByChunkIDs(ctx, 7, []string{"chunk-1"})

	require.NoError(t, err)
	require.Equal(t, []string{"active reason"}, reasons["chunk-1"])
}

func TestChunkFeedbackUpsertIsIdempotentAndTracksDirectionChanges(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	repo := NewChunkFeedbackRepository(db)

	first, err := repo.Upsert(ctx, "message-1", "session-1", "user-1", 7, true, "")
	require.NoError(t, err)
	require.True(t, first.WasCreated)
	require.True(t, first.IsChanged)

	second, err := repo.Upsert(ctx, "message-1", "session-1", "user-1", 7, false, "inaccurate")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.False(t, second.WasCreated)
	require.True(t, second.PreviousIsPositive)
	require.True(t, second.IsChanged)

	third, err := repo.Upsert(ctx, "message-1", "session-1", "user-1", 7, false, "unclear")
	require.NoError(t, err)
	require.Equal(t, first.ID, third.ID)
	require.False(t, third.WasCreated)
	require.False(t, third.PreviousIsPositive)
	require.False(t, third.IsChanged)
	require.Equal(t, "unclear", third.DislikeReason)

	firstNegative, err := repo.Upsert(ctx, "message-2", "session-2", "user-1", 7, false, "inaccurate")
	require.NoError(t, err)
	require.True(t, firstNegative.WasCreated)
	require.False(t, firstNegative.IsPositive)
	persistedNegative, err := repo.GetByMessageAndUser(ctx, 7, "message-2", "user-1")
	require.NoError(t, err)
	require.NotNil(t, persistedNegative)
	require.False(t, persistedNegative.IsPositive,
		"a first-time dislike must not be replaced by the database default for a zero-value bool")
	require.Equal(t, "inaccurate", persistedNegative.DislikeReason)
}

func TestGetFeedbackByMessageIDsAndUserBatchesAndScopesResults(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	repo := &chunkFeedbackRepository{db: db}

	require.NoError(t, db.Create(&[]types.ChunkFeedback{
		{TenantID: 7, MessageID: "message-like", SessionID: "session-1", UserID: "user-1", IsPositive: true},
		{TenantID: 7, MessageID: "message-dislike", SessionID: "session-1", UserID: "user-1", IsPositive: false, DislikeReason: "unclear"},
		{TenantID: 7, MessageID: "message-other-user", SessionID: "session-1", UserID: "user-2", IsPositive: true},
		{TenantID: 8, MessageID: "message-other-tenant", SessionID: "session-1", UserID: "user-1", IsPositive: true},
	}).Error)

	feedbacks, err := repo.GetByMessageIDsAndUser(
		ctx,
		7,
		[]string{"message-like", "message-dislike", "message-other-user", "message-other-tenant"},
		"user-1",
	)

	require.NoError(t, err)
	require.Len(t, feedbacks, 2)
	byMessageID := map[string]*types.ChunkFeedback{}
	for _, feedback := range feedbacks {
		byMessageID[feedback.MessageID] = feedback
	}
	require.True(t, byMessageID["message-like"].IsPositive)
	require.False(t, byMessageID["message-dislike"].IsPositive)
	require.Equal(t, "unclear", byMessageID["message-dislike"].DislikeReason)
}

func TestChunkFeedbackUnitOfWorkRollsBackOnError(t *testing.T) {
	db := setupChunkFeedbackRepoTestDB(t)
	ctx := context.Background()
	uow := NewChunkFeedbackUnitOfWork(db)
	sentinel := errors.New("rollback")

	err := uow.Do(ctx, func(ctx context.Context, repos interfaces.ChunkFeedbackRepositories) error {
		if createErr := repos.QARefRepo.Create(ctx, &types.QAReplyChunkRef{
			TenantID:      7,
			MessageID:     "message-1",
			ChunkID:       "chunk-1",
			ChunkTenantID: 7,
		}); createErr != nil {
			return createErr
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	var count int64
	require.NoError(t, db.Model(&types.QAReplyChunkRef{}).Count(&count).Error)
	require.Equal(t, int64(0), count)
}
