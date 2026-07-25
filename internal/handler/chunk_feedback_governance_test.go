package handler

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestClearChunkFeedbackGovernanceFields(t *testing.T) {
	rate := 0.42
	now := time.Now()
	chunk := &types.Chunk{
		LikeCount:            7,
		DislikeCount:         5,
		PositiveRate:         &rate,
		RecallWeight:         0.8,
		LastFeedbackAt:       &now,
		FeedbackResetAt:      &now,
		NeedsOptimization:    true,
		FeedbackSessionCount: 3,
		DislikeReasons: []types.ChunkDislikeReasonStat{
			{Reason: "mismatch", Count: 2},
		},
	}

	clearChunkFeedbackGovernanceFields(chunk)

	assert.Zero(t, chunk.LikeCount)
	assert.Zero(t, chunk.DislikeCount)
	assert.Nil(t, chunk.PositiveRate)
	assert.Equal(t, float64(1), chunk.RecallWeight)
	assert.Nil(t, chunk.LastFeedbackAt)
	assert.Nil(t, chunk.FeedbackResetAt)
	assert.False(t, chunk.NeedsOptimization)
	assert.Zero(t, chunk.FeedbackSessionCount)
	assert.Nil(t, chunk.DislikeReasons)
}
