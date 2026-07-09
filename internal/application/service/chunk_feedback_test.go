package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
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

func TestNormalizeFeedbackRequestRequiresDislikeReason(t *testing.T) {
	err := normalizeFeedbackRequest(nil)
	if !errors.Is(err, ErrInvalidFeedbackRequest) {
		t.Fatalf("normalizeFeedbackRequest(nil) error = %v, want ErrInvalidFeedbackRequest", err)
	}

	err := normalizeFeedbackRequest(&types.SubmitFeedbackRequest{
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

type chunkFeedbackChunkRepo struct {
	interfaces.ChunkRepository

	chunk                *types.Chunk
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
	return nil
}

func (r *chunkFeedbackChunkRepo) UpdateChunkLastFeedbackAt(ctx context.Context, tenantID uint64, chunkID string) error {
	r.lastFeedbackUpdated = true
	return nil
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

type chunkFeedbackWeightLogRepo struct{}

func (r *chunkFeedbackWeightLogRepo) Create(ctx context.Context, log *types.ChunkWeightLog) error {
	return nil
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

	savedChunkIDs []string
}

func (r *cancelFeedbackQARefRepo) GetByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRef, error) {
	return nil, nil
}

func (r *cancelFeedbackQARefRepo) CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error {
	for _, ref := range refs {
		r.savedChunkIDs = append(r.savedChunkIDs, ref.ChunkID)
	}
	return nil
}

type cancelFeedbackChunkRepo struct {
	interfaces.ChunkRepository

	chunks               map[string]*types.Chunk
	updatedLikeCounts    map[string]int
	updatedDislikeCounts map[string]int
}

func (r *cancelFeedbackChunkRepo) GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	return r.chunks[id], nil
}

func (r *cancelFeedbackChunkRepo) UpdateChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, likeCount, dislikeCount int, positiveRate float64, recallWeight float64, qualityStatus types.ChunkQualityStatus) error {
	if r.updatedLikeCounts != nil {
		r.updatedLikeCounts[chunkID] = likeCount
	}
	r.updatedDislikeCounts[chunkID] = dislikeCount
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
