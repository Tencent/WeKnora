package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// feedbackService implements interfaces.FeedbackService.
type feedbackService struct {
	db               *gorm.DB
	feedbackRepo     interfaces.FeedbackRepository
	weightLogRepo    interfaces.ChunkWeightLogRepository
	statsRepo        interfaces.ChunkFeedbackStatsRepository
	chunkRepo        interfaces.ChunkRepository
	messageRepo      interfaces.MessageRepository
	thresholds       *types.FeedbackThresholds
}

// NewFeedbackService creates a new FeedbackService.
func NewFeedbackService(
	db *gorm.DB,
	feedbackRepo interfaces.FeedbackRepository,
	weightLogRepo interfaces.ChunkWeightLogRepository,
	statsRepo interfaces.ChunkFeedbackStatsRepository,
	chunkRepo interfaces.ChunkRepository,
	messageRepo interfaces.MessageRepository,
) interfaces.FeedbackService {
	return &feedbackService{
		db:            db,
		feedbackRepo:  feedbackRepo,
		weightLogRepo: weightLogRepo,
		statsRepo:     statsRepo,
		chunkRepo:     chunkRepo,
		messageRepo:   messageRepo,
		thresholds:    types.DefaultFeedbackThresholds(),
	}
}

// SubmitFeedback records a user's like/dislike/cancel on a message and
// propagates the effect to all cited chunks.
func (s *feedbackService) SubmitFeedback(ctx context.Context, req *types.FeedbackRequest) (*types.MessageFeedback, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("tenant ID not found in context")
	}
	userID, ok := types.UserIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("user ID not found in context")
	}

	// Fetch the assistant message to validate it exists and get its knowledge references.
	msg, err := s.messageRepo.GetMessage(ctx, req.SessionID, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("fetching message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found")
	}
	if msg.Role != "assistant" {
		return nil, fmt.Errorf("feedback can only be submitted on assistant messages")
	}

	// Ensure chunk refs are persisted for this message.
	if err := s.feedbackRepo.EnsureChunkRefs(ctx, msg); err != nil {
		logger.Warnf(ctx, "Failed to ensure chunk refs for message %s: %v", req.MessageID, err)
		// Non-fatal: we proceed even if ref population fails.
	}

	// Get cited chunk IDs.
	chunkIDs, err := s.feedbackRepo.ListChunkIDsByMessage(ctx, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("listing chunk refs: %w", err)
	}

	// Get the user's previous feedback (if any) to compute deltas.
	prevFeedback, err := s.feedbackRepo.GetFeedback(ctx, userID, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("fetching previous feedback: %w", err)
	}

	// Handle "cancel" (FeedbackNone): delete the feedback row and reverse deltas.
	if req.FeedbackType == types.FeedbackNone {
		if prevFeedback != nil {
			if err := s.reverseFeedbackOnChunks(ctx, tenantID, chunkIDs, prevFeedback); err != nil {
				return nil, fmt.Errorf("reversing feedback on chunks: %w", err)
			}
			if err := s.feedbackRepo.DeleteFeedback(ctx, userID, req.MessageID); err != nil {
				return nil, fmt.Errorf("deleting feedback: %w", err)
			}
		}
		return nil, nil
	}

	// Build the new feedback record.
	newFeedback := &types.MessageFeedback{
		TenantID:     tenantID,
		UserID:       userID,
		SessionID:    req.SessionID,
		MessageID:    req.MessageID,
		FeedbackType: req.FeedbackType,
		Reason:       req.Reason,
		ReasonDetail: req.ReasonDetail,
	}

	// Persist feedback (upsert).
	if err := s.feedbackRepo.UpsertFeedback(ctx, newFeedback); err != nil {
		return nil, fmt.Errorf("upserting feedback: %w", err)
	}

	// Apply delta to each cited chunk.
	if err := s.applyFeedbackDelta(ctx, tenantID, chunkIDs, prevFeedback, newFeedback); err != nil {
		return nil, fmt.Errorf("applying feedback delta to chunks: %w", err)
	}

	return newFeedback, nil
}

// applyFeedbackDelta adjusts like/dislike counters on each cited chunk based
// on the difference between the previous and new feedback type.
func (s *feedbackService) applyFeedbackDelta(
	ctx context.Context,
	tenantID uint64,
	chunkIDs []string,
	prev, new *types.MessageFeedback,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	// Compute the delta for like and dislike counters.
	// For each chunk: new_count = old_count + (new_contribution - old_contribution)
	for _, chunkID := range chunkIDs {
		chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
		if err != nil {
			logger.Warnf(ctx, "Failed to fetch chunk %s for feedback update: %v", chunkID, err)
			continue
		}

		oldLike := chunk.LikeCount
		oldDislike := chunk.DislikeCount

		// Reverse previous feedback contribution.
		if prev != nil {
			switch prev.FeedbackType {
			case types.FeedbackLike:
				chunk.LikeCount--
			case types.FeedbackDislike:
				chunk.DislikeCount--
			}
		}

		// Apply new feedback contribution.
		switch new.FeedbackType {
		case types.FeedbackLike:
			chunk.LikeCount++
		case types.FeedbackDislike:
			chunk.DislikeCount++
		}

		// Clamp to non-negative.
		if chunk.LikeCount < 0 {
			chunk.LikeCount = 0
		}
		if chunk.DislikeCount < 0 {
			chunk.DislikeCount = 0
		}

		// Recalculate approval rate.
		chunk.ApprovalRate = types.ComputeApprovalRate(chunk.LikeCount, chunk.DislikeCount)

		// Recalculate weight and needs_optimization.
		oldWeight := chunk.RecallWeight
		newWeight, needsOpt := types.ComputeWeight(chunk.LikeCount, chunk.DislikeCount, s.thresholds)
		chunk.RecallWeight = newWeight
		chunk.NeedsOptimization = needsOpt
		now := time.Now()
		chunk.FeedbackUpdatedAt = &now

		// Update the chunk.
		if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s feedback fields: %v", chunkID, err)
			continue
		}

		// Log weight change if weight actually changed.
		if oldWeight != newWeight {
			detail := ""
			if new != nil {
				detail = fmt.Sprintf("feedback_type=%s", new.FeedbackType)
				if new.Reason != "" {
					detail += fmt.Sprintf(", reason=%s", new.Reason)
				}
			}
			log := &types.ChunkWeightLog{
				TenantID:        tenantID,
				ChunkID:         chunkID,
				OldWeight:       oldWeight,
				NewWeight:       newWeight,
				OldApprovalRate: types.ComputeApprovalRate(oldLike, oldDislike),
				NewApprovalRate: chunk.ApprovalRate,
				OldLikeCount:    oldLike,
				NewLikeCount:    chunk.LikeCount,
				OldDislikeCount: oldDislike,
				NewDislikeCount: chunk.DislikeCount,
				TriggerType:     types.WeightTriggerUserFeedback,
				TriggerDetail:   detail,
			}
			if err := s.weightLogRepo.CreateLog(ctx, log); err != nil {
				logger.Warnf(ctx, "Failed to create weight log for chunk %s: %v", chunkID, err)
			}
		}
	}
	return nil
}

// reverseFeedbackOnChunks undoes the effect of a previous feedback on all
// cited chunks. Used when feedback is cancelled (FeedbackNone).
func (s *feedbackService) reverseFeedbackOnChunks(
	ctx context.Context,
	tenantID uint64,
	chunkIDs []string,
	prev *types.MessageFeedback,
) error {
	if prev == nil || len(chunkIDs) == 0 {
		return nil
	}

	for _, chunkID := range chunkIDs {
		chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
		if err != nil {
			logger.Warnf(ctx, "Failed to fetch chunk %s for feedback reversal: %v", chunkID, err)
			continue
		}

		oldLike := chunk.LikeCount
		oldDislike := chunk.DislikeCount

		switch prev.FeedbackType {
		case types.FeedbackLike:
			chunk.LikeCount--
		case types.FeedbackDislike:
			chunk.DislikeCount--
		}

		if chunk.LikeCount < 0 {
			chunk.LikeCount = 0
		}
		if chunk.DislikeCount < 0 {
			chunk.DislikeCount = 0
		}

		chunk.ApprovalRate = types.ComputeApprovalRate(chunk.LikeCount, chunk.DislikeCount)
		oldWeight := chunk.RecallWeight
		newWeight, needsOpt := types.ComputeWeight(chunk.LikeCount, chunk.DislikeCount, s.thresholds)
		chunk.RecallWeight = newWeight
		chunk.NeedsOptimization = needsOpt
		now := time.Now()
		chunk.FeedbackUpdatedAt = &now

		if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s on reversal: %v", chunkID, err)
			continue
		}

		if oldWeight != newWeight {
			log := &types.ChunkWeightLog{
				TenantID:        tenantID,
				ChunkID:         chunkID,
				OldWeight:       oldWeight,
				NewWeight:       newWeight,
				OldApprovalRate: types.ComputeApprovalRate(oldLike, oldDislike),
				NewApprovalRate: chunk.ApprovalRate,
				OldLikeCount:    oldLike,
				NewLikeCount:    chunk.LikeCount,
				OldDislikeCount: oldDislike,
				NewDislikeCount: chunk.DislikeCount,
				TriggerType:     types.WeightTriggerUserFeedback,
				TriggerDetail:   "feedback cancelled",
			}
			if err := s.weightLogRepo.CreateLog(ctx, log); err != nil {
				logger.Warnf(ctx, "Failed to create weight log for chunk %s: %v", chunkID, err)
			}
		}
	}
	return nil
}

func (s *feedbackService) GetFeedback(ctx context.Context, messageID string) (*types.MessageFeedback, error) {
	userID, ok := types.UserIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("user ID not found in context")
	}
	return s.feedbackRepo.GetFeedback(ctx, userID, messageID)
}

func (s *feedbackService) GetChunkFeedbackStats(ctx context.Context, chunkID string) (*types.ChunkFeedbackStats, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("tenant ID not found in context")
	}

	stats, err := s.statsRepo.GetChunkFeedbackStats(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}

	// Enrich with session count and dislike reasons.
	sessionCount, err := s.feedbackRepo.CountDistinctSessionsByChunk(ctx, tenantID, chunkID)
	if err != nil {
		logger.Warnf(ctx, "Failed to count sessions for chunk %s: %v", chunkID, err)
	}
	stats.SessionCount = sessionCount

	feedbackCount, err := s.feedbackRepo.CountFeedbackByChunk(ctx, tenantID, chunkID)
	if err != nil {
		logger.Warnf(ctx, "Failed to count feedback for chunk %s: %v", chunkID, err)
	}
	stats.FeedbackCount = feedbackCount

	reasons, err := s.feedbackRepo.AggregateDislikeReasonsByChunk(ctx, tenantID, chunkID)
	if err != nil {
		logger.Warnf(ctx, "Failed to aggregate dislike reasons for chunk %s: %v", chunkID, err)
	}
	stats.DislikeReasons = reasons

	return stats, nil
}

func (s *feedbackService) ListChunkFeedbackStats(
	ctx context.Context,
	kbID string,
	page, pageSize int,
	minApproval, maxApproval float64,
	needsOptimizationOnly bool,
) ([]*types.ChunkFeedbackStats, int64, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, 0, fmt.Errorf("tenant ID not found in context")
	}
	return s.statsRepo.ListChunkFeedbackStats(ctx, tenantID, kbID, page, pageSize, minApproval, maxApproval, needsOptimizationOnly)
}

func (s *feedbackService) ListWeightLogs(ctx context.Context, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, 0, fmt.Errorf("tenant ID not found in context")
	}
	return s.weightLogRepo.ListLogsByChunk(ctx, tenantID, chunkID, page, pageSize)
}

func (s *feedbackService) ListAllWeightLogs(ctx context.Context, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, 0, fmt.Errorf("tenant ID not found in context")
	}
	return s.weightLogRepo.ListLogsByTenant(ctx, tenantID, page, pageSize)
}

// AdminResetChunkFeedback resets a chunk's feedback counters and weight to defaults.
func (s *feedbackService) AdminResetChunkFeedback(ctx context.Context, chunkID string, adminUserID string) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return fmt.Errorf("tenant ID not found in context")
	}

	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("chunk not found: %w", err)
	}

	oldWeight := chunk.RecallWeight
	oldLike := chunk.LikeCount
	oldDislike := chunk.DislikeCount
	oldApproval := chunk.ApprovalRate

	chunk.LikeCount = 0
	chunk.DislikeCount = 0
	chunk.ApprovalRate = 0
	chunk.RecallWeight = 1.0
	chunk.NeedsOptimization = false
	now := time.Now()
	chunk.FeedbackUpdatedAt = &now

	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		return fmt.Errorf("resetting chunk feedback: %w", err)
	}

	// Log the reset.
	log := &types.ChunkWeightLog{
		TenantID:        tenantID,
		ChunkID:         chunkID,
		OldWeight:       oldWeight,
		NewWeight:       1.0,
		OldApprovalRate: oldApproval,
		NewApprovalRate: 0,
		OldLikeCount:    oldLike,
		NewLikeCount:    0,
		OldDislikeCount: oldDislike,
		NewDislikeCount: 0,
		TriggerType:     types.WeightTriggerAdminReset,
		TriggerDetail:   fmt.Sprintf("admin_reset by %s", adminUserID),
	}
	if err := s.weightLogRepo.CreateLog(ctx, log); err != nil {
		logger.Warnf(ctx, "Failed to create weight log for chunk %s reset: %v", chunkID, err)
	}

	return nil
}

// AdminSetChunkWeight manually sets a chunk's recall weight.
func (s *feedbackService) AdminSetChunkWeight(ctx context.Context, chunkID string, weight float64, adminUserID string) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return fmt.Errorf("tenant ID not found in context")
	}

	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("chunk not found: %w", err)
	}

	oldWeight := chunk.RecallWeight
	chunk.RecallWeight = weight
	now := time.Now()
	chunk.FeedbackUpdatedAt = &now

	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		return fmt.Errorf("setting chunk weight: %w", err)
	}

	log := &types.ChunkWeightLog{
		TenantID:        tenantID,
		ChunkID:         chunkID,
		OldWeight:       oldWeight,
		NewWeight:       weight,
		OldApprovalRate: chunk.ApprovalRate,
		NewApprovalRate: chunk.ApprovalRate,
		OldLikeCount:    chunk.LikeCount,
		NewLikeCount:    chunk.LikeCount,
		OldDislikeCount: chunk.DislikeCount,
		NewDislikeCount: chunk.DislikeCount,
		TriggerType:     types.WeightTriggerAdminManual,
		TriggerDetail:   fmt.Sprintf("manual_set by %s, weight=%.2f", adminUserID, weight),
	}
	if err := s.weightLogRepo.CreateLog(ctx, log); err != nil {
		logger.Warnf(ctx, "Failed to create weight log for chunk %s manual set: %v", chunkID, err)
	}

	return nil
}

func (s *feedbackService) GetThresholds(ctx context.Context) *types.FeedbackThresholds {
	return s.thresholds
}
