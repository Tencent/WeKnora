package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUpdateSingleChunkFeedbackStats_NewLikeDoesNotConsumeExistingDislikes(t *testing.T) {
	ctx := context.Background()
	chunkRepo := &chunkFeedbackChunkRepo{
		chunk: &types.Chunk{
			ID:            "chunk-1",
			TenantID:      1,
			LikeCount:     2,
			DislikeCount:  3,
			PositiveRate:  0.4,
			RecallWeight:  0.5,
			QualityStatus: types.ChunkQualityStatusNormal,
		},
	}
	svc := NewChunkFeedbackService(nil, nil, nil, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.updateSingleChunkFeedbackStats(ctx, 1, "chunk-1", &types.ChunkFeedback{
		WasCreated: true,
		IsChanged:  true,
		IsPositive: true,
	}, "")

	require.NoError(t, err)
	require.Equal(t, 3, chunkRepo.updatedLikeCount)
	require.Equal(t, 3, chunkRepo.updatedDislikeCount)
	require.InDelta(t, 0.5, chunkRepo.updatedPositiveRate, 0.001)
	require.Equal(t, 1.0, chunkRepo.updatedRecallWeight)
}

func TestUpdateSingleChunkFeedbackStats_SwitchDislikeToLikeMovesOneVote(t *testing.T) {
	ctx := context.Background()
	chunkRepo := &chunkFeedbackChunkRepo{
		chunk: &types.Chunk{
			ID:            "chunk-1",
			TenantID:      1,
			LikeCount:     2,
			DislikeCount:  3,
			PositiveRate:  0.4,
			RecallWeight:  0.5,
			QualityStatus: types.ChunkQualityStatusNormal,
		},
	}
	svc := NewChunkFeedbackService(nil, nil, nil, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.updateSingleChunkFeedbackStats(ctx, 1, "chunk-1", &types.ChunkFeedback{
		WasCreated:         false,
		IsChanged:          true,
		PreviousIsPositive: false,
		IsPositive:         true,
	}, "")

	require.NoError(t, err)
	require.Equal(t, 3, chunkRepo.updatedLikeCount)
	require.Equal(t, 2, chunkRepo.updatedDislikeCount)
	require.InDelta(t, 0.6, chunkRepo.updatedPositiveRate, 0.001)
	require.Equal(t, 1.0, chunkRepo.updatedRecallWeight)
}

func TestUpdateSingleChunkFeedbackStats_RepeatedDislikeDoesNotAppendReason(t *testing.T) {
	ctx := context.Background()
	reasons, err := json.Marshal([]string{"inaccurate"})
	require.NoError(t, err)
	chunkRepo := &chunkFeedbackChunkRepo{
		chunk: &types.Chunk{
			ID:             "chunk-1",
			TenantID:       1,
			LikeCount:      1,
			DislikeCount:   2,
			PositiveRate:   0.33,
			RecallWeight:   0.5,
			QualityStatus:  types.ChunkQualityStatusNormal,
			DislikeReasons: reasons,
		},
	}
	svc := NewChunkFeedbackService(nil, nil, nil, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err = svc.updateSingleChunkFeedbackStats(ctx, 1, "chunk-1", &types.ChunkFeedback{
		WasCreated: false,
		IsChanged:  false,
		IsPositive: false,
	}, "irrelevant")

	require.NoError(t, err)
	require.Equal(t, 1, chunkRepo.updatedLikeCount)
	require.Equal(t, 2, chunkRepo.updatedDislikeCount)
	require.JSONEq(t, `["inaccurate"]`, string(chunkRepo.chunk.DislikeReasons))
}

func TestUpdateChunksFeedbackStatsReturnsChunkUpdateError(t *testing.T) {
	ctx := context.Background()
	writeErr := errors.New("chunk stats write failed")
	chunkRepo := &chunkFeedbackChunkRepo{
		chunk: &types.Chunk{
			ID:            "chunk-1",
			TenantID:      1,
			RecallWeight:  1,
			QualityStatus: types.ChunkQualityStatusNormal,
		},
		updateErr: writeErr,
	}
	svc := NewChunkFeedbackService(nil, nil, nil, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.updateChunksFeedbackStats(ctx, 1, []feedbackChunkRef{{ChunkID: "chunk-1", ChunkTenantID: 1}}, &types.ChunkFeedback{
		WasCreated: true,
		IsChanged:  true,
		IsPositive: true,
	}, "")

	require.ErrorIs(t, err, writeErr)
}

func TestUpdateMessageFeedbackStats_NewLikeDoesNotConsumeExistingDislikes(t *testing.T) {
	ctx := context.Background()
	messageRepo := &chunkFeedbackMessageRepo{}
	svc := NewChunkFeedbackService(nil, nil, messageRepo, nil, &chunkFeedbackWeightLogRepo{})

	err := svc.updateMessageFeedbackStats(ctx, 1, "user-1", &types.Message{
		ID:           "message-1",
		LikeCount:    2,
		DislikeCount: 3,
	}, &types.ChunkFeedback{
		WasCreated: true,
		IsChanged:  true,
		IsPositive: true,
	})

	require.NoError(t, err)
	require.Equal(t, 3, messageRepo.updatedLikeCount)
	require.Equal(t, 3, messageRepo.updatedDislikeCount)
}

func TestCancelMessageFeedbackStats_DecrementsPreviousVote(t *testing.T) {
	ctx := context.Background()
	messageRepo := &chunkFeedbackMessageRepo{}
	svc := NewChunkFeedbackService(nil, nil, messageRepo, nil, &chunkFeedbackWeightLogRepo{})

	err := svc.cancelMessageFeedbackStats(ctx, 1, "user-1", &types.Message{
		ID:           "message-1",
		LikeCount:    1,
		DislikeCount: 2,
	}, false)

	require.NoError(t, err)
	require.Equal(t, 1, messageRepo.updatedLikeCount)
	require.Equal(t, 1, messageRepo.updatedDislikeCount)
}

func TestCancelFeedbackBackfillsChunkRefsFromMessageReferences(t *testing.T) {
	ctx := context.Background()
	feedbackRepo := &cancelFeedbackFeedbackRepo{
		feedback: &types.ChunkFeedback{
			ID:         "feedback-1",
			MessageID:  "message-1",
			SessionID:  "session-1",
			TenantID:   1,
			UserID:     "user-1",
			IsPositive: false,
		},
	}
	messageRepo := &cancelFeedbackMessageRepo{
		message: &types.Message{
			ID:           "message-1",
			SessionID:    "session-1",
			Role:         "assistant",
			DislikeCount: 1,
			KnowledgeReferences: types.References{
				&types.SearchResult{ID: "chunk-a", SubChunkID: []string{"chunk-b"}},
			},
		},
	}
	qaRefRepo := &cancelFeedbackQARefRepo{}
	chunkRepo := &cancelFeedbackChunkRepo{
		chunks: map[string]*types.Chunk{
			"chunk-a": {
				ID:            "chunk-a",
				TenantID:      1,
				DislikeCount:  1,
				RecallWeight:  0.5,
				QualityStatus: types.ChunkQualityStatusNormal,
			},
			"chunk-b": {
				ID:            "chunk-b",
				TenantID:      1,
				DislikeCount:  1,
				RecallWeight:  0.5,
				QualityStatus: types.ChunkQualityStatusNormal,
			},
		},
		updatedDislikeCounts: make(map[string]int),
	}
	svc := NewChunkFeedbackService(qaRefRepo, feedbackRepo, messageRepo, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.CancelFeedback(ctx, 1, "user-1", "message-1")

	require.NoError(t, err)
	require.Equal(t, "feedback-1", feedbackRepo.deletedID)
	require.Equal(t, 0, messageRepo.updatedDislikeCount)
	require.Equal(t, []string{"chunk-a", "chunk-b"}, qaRefRepo.savedChunkIDs)
	require.Equal(t, map[string]int{"chunk-a": 0, "chunk-b": 0}, chunkRepo.updatedDislikeCounts)
}

func TestSubmitFeedbackBackfillsOnlyMissingChunkStatsForUnchangedFeedback(t *testing.T) {
	ctx := context.Background()
	feedbackRepo := &submitFeedbackFeedbackRepo{
		feedback: &types.ChunkFeedback{
			ID:         "feedback-1",
			MessageID:  "message-1",
			SessionID:  "session-1",
			TenantID:   1,
			UserID:     "user-1",
			IsPositive: true,
			IsChanged:  false,
			WasCreated: false,
		},
	}
	messageRepo := &cancelFeedbackMessageRepo{
		message: &types.Message{
			ID:        "message-1",
			SessionID: "session-1",
			Role:      "assistant",
			LikeCount: 1,
			KnowledgeReferences: types.References{
				&types.SearchResult{ID: "chunk-a", SubChunkID: []string{"chunk-b"}},
			},
		},
	}
	qaRefRepo := &submitFeedbackQARefRepo{
		refs: []*types.QAReplyChunkRef{{MessageID: "message-1", ChunkID: "chunk-a", TenantID: 1}},
	}
	chunkRepo := &cancelFeedbackChunkRepo{
		chunks: map[string]*types.Chunk{
			"chunk-a": {
				ID:            "chunk-a",
				TenantID:      1,
				LikeCount:     1,
				PositiveRate:  1,
				RecallWeight:  1.5,
				QualityStatus: types.ChunkQualityStatusNormal,
			},
			"chunk-b": {
				ID:            "chunk-b",
				TenantID:      1,
				RecallWeight:  1,
				QualityStatus: types.ChunkQualityStatusNormal,
			},
		},
		updatedLikeCounts:    make(map[string]int),
		updatedDislikeCounts: make(map[string]int),
	}
	svc := NewChunkFeedbackService(qaRefRepo, feedbackRepo, messageRepo, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.SubmitFeedback(ctx, 1, "user-1", &types.SubmitFeedbackRequest{
		MessageID:  "message-1",
		IsPositive: true,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"chunk-b"}, qaRefRepo.savedChunkIDs)
	require.Equal(t, map[string]int{"chunk-b": 1}, chunkRepo.updatedLikeCounts)
	require.Equal(t, map[string]int{"chunk-b": 0}, chunkRepo.updatedDislikeCounts)
}

func TestSubmitFeedbackBackfillsSharedKnowledgeChunkTenant(t *testing.T) {
	ctx := context.Background()
	feedbackRepo := &submitFeedbackFeedbackRepo{
		feedback: &types.ChunkFeedback{
			ID:         "feedback-1",
			MessageID:  "message-1",
			SessionID:  "session-1",
			TenantID:   1,
			UserID:     "user-1",
			IsPositive: true,
			IsChanged:  true,
			WasCreated: true,
		},
	}
	messageRepo := &cancelFeedbackMessageRepo{
		message: &types.Message{
			ID:        "message-1",
			SessionID: "session-1",
			Role:      "assistant",
			KnowledgeReferences: types.References{
				&types.SearchResult{ID: "chunk-shared", KnowledgeBaseID: "kb-shared"},
			},
		},
	}
	qaRefRepo := &submitFeedbackQARefRepo{}
	chunkRepo := &cancelFeedbackChunkRepo{
		chunks: map[string]*types.Chunk{
			"chunk-shared": {
				ID:            "chunk-shared",
				TenantID:      2,
				RecallWeight:  1,
				QualityStatus: types.ChunkQualityStatusNormal,
			},
		},
		updatedLikeCounts:    make(map[string]int),
		updatedDislikeCounts: make(map[string]int),
		updatedTenantIDs:     make(map[string]uint64),
	}
	svc := NewChunkFeedbackService(qaRefRepo, feedbackRepo, messageRepo, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.SubmitFeedback(ctx, 1, "user-1", &types.SubmitFeedbackRequest{
		MessageID:  "message-1",
		IsPositive: true,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"chunk-shared"}, qaRefRepo.savedChunkIDs)
	require.Equal(t, uint64(2), qaRefRepo.savedChunkTenantIDs["chunk-shared"])
	require.Equal(t, uint64(2), chunkRepo.updatedTenantIDs["chunk-shared"])
	require.Equal(t, map[string]int{"chunk-shared": 1}, chunkRepo.updatedLikeCounts)
}

func TestSubmitFeedbackDoesNotBackfillResetChunkRef(t *testing.T) {
	ctx := context.Background()
	feedbackRepo := &submitFeedbackFeedbackRepo{
		feedback: &types.ChunkFeedback{
			ID:         "feedback-1",
			MessageID:  "message-1",
			SessionID:  "session-1",
			TenantID:   1,
			UserID:     "user-1",
			IsPositive: true,
			IsChanged:  true,
			WasCreated: true,
		},
	}
	messageRepo := &cancelFeedbackMessageRepo{
		message: &types.Message{
			ID:        "message-1",
			SessionID: "session-1",
			Role:      "assistant",
			KnowledgeReferences: types.References{
				&types.SearchResult{ID: "chunk-reset", KnowledgeBaseID: "kb-shared"},
			},
		},
	}
	qaRefRepo := &submitFeedbackQARefRepo{
		cancelFeedbackQARefRepo: cancelFeedbackQARefRepo{
			tombstones: []*types.QAReplyChunkRefTombstone{
				{TenantID: 1, MessageID: "message-1", ChunkID: "chunk-reset", ChunkTenantID: 2},
			},
		},
	}
	chunkRepo := &cancelFeedbackChunkRepo{
		chunks: map[string]*types.Chunk{
			"chunk-reset": {
				ID:            "chunk-reset",
				TenantID:      2,
				RecallWeight:  1,
				QualityStatus: types.ChunkQualityStatusNormal,
			},
		},
		updatedLikeCounts:    make(map[string]int),
		updatedDislikeCounts: make(map[string]int),
	}
	svc := NewChunkFeedbackService(qaRefRepo, feedbackRepo, messageRepo, chunkRepo, &chunkFeedbackWeightLogRepo{})

	err := svc.SubmitFeedback(ctx, 1, "user-1", &types.SubmitFeedbackRequest{
		MessageID:  "message-1",
		IsPositive: true,
	})

	require.NoError(t, err)
	require.Empty(t, qaRefRepo.savedChunkIDs)
	require.Empty(t, chunkRepo.updatedLikeCounts)
	require.Empty(t, chunkRepo.updatedDislikeCounts)
}

func TestChunkFeedbackFullFlowLikeDislikeCancelReset(t *testing.T) {
	ctx := context.Background()
	db := setupChunkFeedbackFlowDB(t)
	svc := NewChunkFeedbackServiceWithUnitOfWork(
		repository.NewQAReplyChunkRefRepository(db),
		repository.NewChunkFeedbackRepository(db),
		repository.NewMessageRepository(db),
		repository.NewChunkRepository(db),
		repository.NewChunkWeightLogRepository(db),
		repository.NewChunkFeedbackUnitOfWork(db),
	)

	require.NoError(t, svc.SubmitFeedback(ctx, 1, "user-1", &types.SubmitFeedbackRequest{
		MessageID:  "message-1",
		IsPositive: true,
	}))
	requireFeedbackFlowCounts(t, db, 1, 0, 1, 0, 1, 1, 0)

	require.NoError(t, svc.SubmitFeedback(ctx, 1, "user-1", &types.SubmitFeedbackRequest{
		MessageID:     "message-1",
		IsPositive:    false,
		DislikeReason: "inaccurate",
	}))
	requireFeedbackFlowCounts(t, db, 0, 1, 0, 1, 1, 1, 0)

	require.NoError(t, svc.CancelFeedback(ctx, 1, "user-1", "message-1"))
	requireFeedbackFlowCounts(t, db, 0, 0, 0, 0, 1, 0, 0)

	require.NoError(t, svc.ResetChunkFeedback(ctx, 1, "chunk-1", "admin-1"))
	requireFeedbackFlowCounts(t, db, 0, 0, 0, 0, 0, 0, 1)

	require.NoError(t, svc.SubmitFeedback(ctx, 1, "user-1", &types.SubmitFeedbackRequest{
		MessageID:  "message-1",
		IsPositive: true,
	}))
	requireFeedbackFlowCounts(t, db, 0, 0, 1, 0, 0, 1, 1)
}

func TestNormalizeFeedbackRequestRequiresDislikeReason(t *testing.T) {
	err := normalizeFeedbackRequest(nil)
	if !errors.Is(err, ErrInvalidFeedbackRequest) {
		t.Fatalf("normalizeFeedbackRequest(nil) error = %v, want ErrInvalidFeedbackRequest", err)
	}

	err = normalizeFeedbackRequest(&types.SubmitFeedbackRequest{
		MessageID:  "message-1",
		IsPositive: false,
	})

	if !errors.Is(err, ErrDislikeReasonRequired) {
		t.Fatalf("normalizeFeedbackRequest() error = %v, want ErrDislikeReasonRequired", err)
	}
}

func TestAggregateDislikeReasonsCountsAndSorts(t *testing.T) {
	got := aggregateDislikeReasons([]string{"unclear", "inaccurate", "unclear", "incomplete"})
	want := []types.DislikeReasonStat{
		{Reason: "unclear", Count: 2},
		{Reason: "inaccurate", Count: 1},
		{Reason: "incomplete", Count: 1},
	}

	require.Equal(t, want, got)
}

func TestResetChunkFeedbackDeletesRefsAndPropagatesWeightLogError(t *testing.T) {
	ctx := context.Background()
	logErr := errors.New("weight log failed")
	qaRefRepo := &resetFeedbackQARefRepo{}
	chunkRepo := &resetFeedbackChunkRepo{
		chunk: &types.Chunk{
			ID:            "chunk-1",
			TenantID:      1,
			RecallWeight:  0.5,
			QualityStatus: types.ChunkQualityStatusPendingOpt,
		},
	}
	weightLogRepo := &chunkFeedbackWeightLogRepo{createErr: logErr}
	svc := NewChunkFeedbackService(qaRefRepo, nil, nil, chunkRepo, weightLogRepo)

	err := svc.ResetChunkFeedback(ctx, 1, "chunk-1", "admin-1")

	require.ErrorIs(t, err, logErr)
	require.Equal(t, "chunk-1", qaRefRepo.deletedChunkID)
	require.Equal(t, uint64(1), qaRefRepo.deletedTenantID)
	require.Equal(t, []string{"message-1"}, qaRefRepo.tombstonedMessageIDs)
	require.True(t, chunkRepo.resetCalled)
}

func setupChunkFeedbackFlowDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:chunk-feedback-flow?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.Exec(`
		CREATE TABLE sessions (
			id varchar(36) PRIMARY KEY,
			tenant_id integer NOT NULL,
			user_id varchar(512),
			deleted_at datetime
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE messages (
			id varchar(36) PRIMARY KEY,
			session_id varchar(36),
			role text,
			knowledge_references text,
			updated_at datetime,
			deleted_at datetime
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE chunks (
			id varchar(36) PRIMARY KEY,
			tenant_id integer NOT NULL,
			knowledge_base_id varchar(36),
			knowledge_id varchar(36),
			content text,
			updated_at datetime,
			deleted_at datetime
		)
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO sessions (id, tenant_id, user_id) VALUES ('session-1', 1, 'user-1')
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO messages (id, session_id, role, knowledge_references)
		VALUES ('message-1', 'session-1', 'assistant', '[{"id":"chunk-1","knowledge_base_id":"kb-1"}]')
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, content, updated_at)
		VALUES ('chunk-1', 1, 'kb-1', 'knowledge-1', 'chunk content', CURRENT_TIMESTAMP)
	`).Error)
	migrationSQL, err := os.ReadFile("../../../migrations/sqlite/000079_chunk_feedback.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migrationSQL)).Error)
	return db
}

func requireFeedbackFlowCounts(
	t *testing.T,
	db *gorm.DB,
	chunkLikes, chunkDislikes, messageLikes, messageDislikes int,
	refCount, feedbackCount, tombstoneCount int64,
) {
	t.Helper()
	var chunk struct {
		LikeCount    int
		DislikeCount int
	}
	require.NoError(t, db.Table("chunks").Select("like_count, dislike_count").Where("id = ?", "chunk-1").Scan(&chunk).Error)
	require.Equal(t, chunkLikes, chunk.LikeCount)
	require.Equal(t, chunkDislikes, chunk.DislikeCount)

	var message struct {
		LikeCount    int
		DislikeCount int
	}
	require.NoError(t, db.Table("messages").Select("like_count, dislike_count").Where("id = ?", "message-1").Scan(&message).Error)
	require.Equal(t, messageLikes, message.LikeCount)
	require.Equal(t, messageDislikes, message.DislikeCount)

	var count int64
	require.NoError(t, db.Table("qa_reply_chunk_refs").Count(&count).Error)
	require.Equal(t, refCount, count)
	require.NoError(t, db.Table("chunk_feedbacks").Count(&count).Error)
	require.Equal(t, feedbackCount, count)
	require.NoError(t, db.Table("qa_reply_chunk_ref_tombstones").Count(&count).Error)
	require.Equal(t, tombstoneCount, count)
}

type chunkFeedbackChunkRepo struct {
	interfaces.ChunkRepository

	chunk                *types.Chunk
	updateErr            error
	lastFeedbackErr      error
	updatedLikeCount     int
	updatedDislikeCount  int
	updatedPositiveRate  float64
	updatedRecallWeight  float64
	updatedQualityStatus types.ChunkQualityStatus
	lastFeedbackUpdated  bool
}

func (r *chunkFeedbackChunkRepo) GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	return r.chunk, nil
}

func (r *chunkFeedbackChunkRepo) UpdateChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, likeCount, dislikeCount int, positiveRate float64, recallWeight float64, qualityStatus types.ChunkQualityStatus) error {
	r.updatedLikeCount = likeCount
	r.updatedDislikeCount = dislikeCount
	r.updatedPositiveRate = positiveRate
	r.updatedRecallWeight = recallWeight
	r.updatedQualityStatus = qualityStatus
	return r.updateErr
}

func (r *chunkFeedbackChunkRepo) UpdateChunkLastFeedbackAt(ctx context.Context, tenantID uint64, chunkID string) error {
	r.lastFeedbackUpdated = true
	return r.lastFeedbackErr
}

type chunkFeedbackMessageRepo struct {
	interfaces.MessageRepository

	updatedLikeCount    int
	updatedDislikeCount int
}

func (r *chunkFeedbackMessageRepo) UpdateMessageFeedbackStats(ctx context.Context, tenantID uint64, userID, messageID string, likeCount, dislikeCount int) error {
	r.updatedLikeCount = likeCount
	r.updatedDislikeCount = dislikeCount
	return nil
}

type chunkFeedbackWeightLogRepo struct {
	createErr error
}

func (r *chunkFeedbackWeightLogRepo) Create(ctx context.Context, log *types.ChunkWeightLog) error {
	return r.createErr
}

func (r *chunkFeedbackWeightLogRepo) GetByChunkID(ctx context.Context, tenantID uint64, chunkID string, limit int) ([]*types.ChunkWeightLog, error) {
	return nil, nil
}

func (r *chunkFeedbackWeightLogRepo) CountByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	return 0, nil
}

type cancelFeedbackFeedbackRepo struct {
	interfaces.ChunkFeedbackRepository

	feedback  *types.ChunkFeedback
	deletedID string
}

func (r *cancelFeedbackFeedbackRepo) GetByMessageAndUser(ctx context.Context, tenantID uint64, messageID, userID string) (*types.ChunkFeedback, error) {
	return r.feedback, nil
}

func (r *cancelFeedbackFeedbackRepo) Delete(ctx context.Context, tenantID uint64, id string) error {
	r.deletedID = id
	return nil
}

type cancelFeedbackMessageRepo struct {
	interfaces.MessageRepository

	message             *types.Message
	updatedLikeCount    int
	updatedDislikeCount int
}

func (r *cancelFeedbackMessageRepo) GetMessageByID(ctx context.Context, tenantID uint64, userID, messageID string) (*types.Message, error) {
	return r.message, nil
}

func (r *cancelFeedbackMessageRepo) UpdateMessageFeedbackStats(ctx context.Context, tenantID uint64, userID, messageID string, likeCount, dislikeCount int) error {
	r.updatedLikeCount = likeCount
	r.updatedDislikeCount = dislikeCount
	return nil
}

type cancelFeedbackQARefRepo struct {
	interfaces.QAReplyChunkRefRepository

	savedChunkIDs       []string
	savedChunkTenantIDs map[string]uint64
	tombstones          []*types.QAReplyChunkRefTombstone
}

func (r *cancelFeedbackQARefRepo) GetByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRef, error) {
	return nil, nil
}

func (r *cancelFeedbackQARefRepo) CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error {
	if r.savedChunkTenantIDs == nil {
		r.savedChunkTenantIDs = make(map[string]uint64)
	}
	for _, ref := range refs {
		r.savedChunkIDs = append(r.savedChunkIDs, ref.ChunkID)
		r.savedChunkTenantIDs[ref.ChunkID] = ref.ChunkTenantID
	}
	return nil
}

func (r *cancelFeedbackQARefRepo) CreateResetTombstones(ctx context.Context, refs []*types.QAReplyChunkRef, operator string) error {
	for _, ref := range refs {
		r.tombstones = append(r.tombstones, &types.QAReplyChunkRefTombstone{
			TenantID:      ref.TenantID,
			MessageID:     ref.MessageID,
			ChunkID:       ref.ChunkID,
			ChunkTenantID: ref.ChunkTenantID,
			Operator:      operator,
		})
	}
	return nil
}

func (r *cancelFeedbackQARefRepo) GetResetTombstonesByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRefTombstone, error) {
	var tombstones []*types.QAReplyChunkRefTombstone
	for _, tombstone := range r.tombstones {
		if tombstone.TenantID == tenantID && tombstone.MessageID == messageID {
			tombstones = append(tombstones, tombstone)
		}
	}
	return tombstones, nil
}

type cancelFeedbackChunkRepo struct {
	interfaces.ChunkRepository

	chunks               map[string]*types.Chunk
	updatedLikeCounts    map[string]int
	updatedDislikeCounts map[string]int
	updatedTenantIDs     map[string]uint64
}

func (r *cancelFeedbackChunkRepo) GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	return r.chunks[id], nil
}

func (r *cancelFeedbackChunkRepo) ListChunksByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	chunks := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk, ok := r.chunks[id]; ok {
			chunks = append(chunks, chunk)
		}
	}
	return chunks, nil
}

func (r *cancelFeedbackChunkRepo) UpdateChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, likeCount, dislikeCount int, positiveRate float64, recallWeight float64, qualityStatus types.ChunkQualityStatus) error {
	if r.updatedLikeCounts != nil {
		r.updatedLikeCounts[chunkID] = likeCount
	}
	r.updatedDislikeCounts[chunkID] = dislikeCount
	if r.updatedTenantIDs != nil {
		r.updatedTenantIDs[chunkID] = tenantID
	}
	return nil
}

func (r *cancelFeedbackChunkRepo) UpdateChunkLastFeedbackAt(ctx context.Context, tenantID uint64, chunkID string) error {
	return nil
}

type resetFeedbackQARefRepo struct {
	interfaces.QAReplyChunkRefRepository

	deletedTenantID       uint64
	deletedChunkID        string
	tombstonedMessageIDs  []string
	resetRefsForChunkID   string
	resetRefsForTenantID  uint64
	tombstoneCreateCalled bool
}

func (r *resetFeedbackQARefRepo) GetByChunkID(ctx context.Context, tenantID uint64, chunkID string) ([]*types.QAReplyChunkRef, error) {
	r.resetRefsForTenantID = tenantID
	r.resetRefsForChunkID = chunkID
	return []*types.QAReplyChunkRef{
		{TenantID: 1, MessageID: "message-1", ChunkID: chunkID, ChunkTenantID: tenantID},
	}, nil
}

func (r *resetFeedbackQARefRepo) CreateResetTombstones(ctx context.Context, refs []*types.QAReplyChunkRef, operator string) error {
	r.tombstoneCreateCalled = true
	for _, ref := range refs {
		r.tombstonedMessageIDs = append(r.tombstonedMessageIDs, ref.MessageID)
	}
	return nil
}

func (r *resetFeedbackQARefRepo) DeleteByChunkID(ctx context.Context, chunkTenantID uint64, chunkID string) error {
	r.deletedTenantID = chunkTenantID
	r.deletedChunkID = chunkID
	return nil
}

type resetFeedbackChunkRepo struct {
	interfaces.ChunkRepository

	chunk       *types.Chunk
	resetCalled bool
}

func (r *resetFeedbackChunkRepo) GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	return r.chunk, nil
}

func (r *resetFeedbackChunkRepo) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkID string) error {
	r.resetCalled = true
	return nil
}

type submitFeedbackFeedbackRepo struct {
	interfaces.ChunkFeedbackRepository

	feedback *types.ChunkFeedback
}

func (r *submitFeedbackFeedbackRepo) Upsert(ctx context.Context, messageID, sessionID, userID string, tenantID uint64, isPositive bool, dislikeReason string) (*types.ChunkFeedback, error) {
	return r.feedback, nil
}

type submitFeedbackQARefRepo struct {
	cancelFeedbackQARefRepo

	refs []*types.QAReplyChunkRef
}

func (r *submitFeedbackQARefRepo) GetByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRef, error) {
	return r.refs, nil
}
