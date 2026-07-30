package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// feedbackService implements the feedback business logic.
type feedbackService struct {
	db           *gorm.DB
	feedbackRepo *repository.FeedbackRepository
	chunkRepo    interfaces.ChunkRepository
	messageRepo  interfaces.MessageRepository
	config       *types.FeedbackConfig
}

// NewFeedbackService creates a new feedback service.
func NewFeedbackService(
	db *gorm.DB,
	feedbackRepo *repository.FeedbackRepository,
	chunkRepo interfaces.ChunkRepository,
	messageRepo interfaces.MessageRepository,
) *feedbackService {
	return &feedbackService{
		db:           db,
		feedbackRepo: feedbackRepo,
		chunkRepo:    chunkRepo,
		messageRepo:  messageRepo,
		config:       types.DefaultFeedbackConfig(),
	}
}

// SubmitFeedback records a user's like/dislike on an assistant message and updates chunk stats.
func (s *feedbackService) SubmitFeedback(
	ctx context.Context,
	tenantID uint64,
	sessionID string,
	messageID string,
	feedbackType types.FeedbackType,
	reason string,
) (*types.Message, error) {
	msg, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	if msg == nil || msg.Role != "assistant" {
		return nil, werrors.NewBadRequestError("feedback only allowed on assistant messages")
	}

	links, _ := s.feedbackRepo.GetMessageChunkLinks(ctx, messageID)

	now := time.Now()
	msg.Feedback = &types.MessageFeedback{
		Type:      feedbackType,
		Reason:    reason,
		CreatedAt: now,
	}
	if len(links) > 0 {
		chunkIDs := make([]string, len(links))
		for i, link := range links {
			chunkIDs[i] = link.ChunkID
		}
		msg.Feedback.ChunkIDs = chunkIDs
	}
	if err := s.messageRepo.UpdateMessage(ctx, msg); err != nil {
		return nil, err
	}

	// Update chunk feedback stats asynchronously
	go func() {
		bgCtx := context.WithoutCancel(ctx)
		for _, link := range links {
			s.updateChunkFeedback(bgCtx, tenantID, link.ChunkID, feedbackType)
		}
	}()

	return msg, nil
}

// updateChunkFeedback updates a single chunk's like/dislike counters and adjusts weight.
func (s *feedbackService) updateChunkFeedback(ctx context.Context, tenantID uint64, chunkID string, feedbackType types.FeedbackType) {
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil || chunk == nil {
		return
	}

	oldWeight := chunk.RecallWeight

	if feedbackType == types.FeedbackTypeLike {
		chunk.LikeCount++
	} else if feedbackType == types.FeedbackTypeDislike {
		chunk.DislikeCount++
	}

	total := chunk.LikeCount + chunk.DislikeCount
	if total > 0 {
		chunk.LikeRate = float64(chunk.LikeCount) / float64(total)
	} else {
		chunk.LikeRate = 0
	}

	if s.shouldAdjustWeight(chunk) {
		newWeight := s.computeNewWeight(chunk, oldWeight)
		if newWeight != oldWeight {
			now := time.Now()
			chunk.RecallWeight = newWeight
			chunk.WeightUpdatedAt = &now
		}
	}

	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		logger.Errorf(ctx, "Failed to update chunk feedback: %v", err)
	}
}

// shouldAdjustWeight checks if the chunk meets the threshold for weight adjustment.
func (s *feedbackService) shouldAdjustWeight(chunk *types.Chunk) bool {
	total := chunk.LikeCount + chunk.DislikeCount
	return total >= s.config.MinFeedbackCount
}

// computeNewWeight determines the new weight based on thresholds.
func (s *feedbackService) computeNewWeight(chunk *types.Chunk, oldWeight float64) float64 {
	var newWeight float64
	var reason string

	if chunk.LikeRate >= s.config.LikeRateBoostThreshold {
		newWeight = oldWeight * s.config.WeightBoostFactor
		reason = "auto_boost"
	} else if chunk.LikeRate <= s.config.LikeRatePenaltyThreshold {
		newWeight = oldWeight * s.config.WeightPenaltyFactor
		reason = "auto_penalty"
	} else {
		return oldWeight
	}

	// Log the weight change
	log := &types.ChunkWeightLog{
		ID:              uuid.New().String(),
		TenantID:        chunk.TenantID,
		ChunkID:         chunk.ID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		KnowledgeID:     chunk.KnowledgeID,
		OldWeight:       oldWeight,
		NewWeight:       newWeight,
		Reason:          reason,
		Operator:        "system",
		CreatedAt:       time.Now(),
	}
	if err := s.feedbackRepo.CreateWeightLog(context.Background(), log); err != nil {
		logger.Errorf(context.Background(), "Failed to create weight log: %v", err)
	}

	return newWeight
}

// ListChunkFeedbackStats lists chunks with feedback stats.
func (s *feedbackService) ListChunkFeedbackStats(ctx context.Context, tenantID uint64, kbID string, page, pageSize int) ([]*types.ChunkFeedbackStats, int64, error) {
	return s.feedbackRepo.ListChunkFeedbackStats(ctx, tenantID, kbID, page, pageSize)
}

// ListWeightLogs lists weight change logs.
func (s *feedbackService) ListWeightLogs(ctx context.Context, tenantID uint64, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	return s.feedbackRepo.ListWeightLogs(ctx, tenantID, page, pageSize)
}

// ListWeightLogsByChunk lists weight change logs for a specific chunk.
func (s *feedbackService) ListWeightLogsByChunk(ctx context.Context, tenantID uint64, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	return s.feedbackRepo.ListWeightLogsByChunk(ctx, tenantID, chunkID, page, pageSize)
}

// ResetChunkFeedback resets feedback data for a chunk.
func (s *feedbackService) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkID string, operatorID string) error {
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil || chunk == nil {
		return err
	}

	oldWeight := chunk.RecallWeight

	if err := s.feedbackRepo.ResetChunkFeedback(ctx, tenantID, chunkID); err != nil {
		return err
	}

	now := time.Now()
	log := &types.ChunkWeightLog{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		ChunkID:         chunkID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		KnowledgeID:     chunk.KnowledgeID,
		OldWeight:       oldWeight,
		NewWeight:       1.0,
		Reason:          "manual_reset",
		Operator:        operatorID,
		CreatedAt:       now,
	}
	if err := s.feedbackRepo.CreateWeightLog(ctx, log); err != nil {
		logger.Errorf(ctx, "Failed to create reset weight log: %v", err)
	}

	return nil
}
