package service

import (
	"context"
	"encoding/json"
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
