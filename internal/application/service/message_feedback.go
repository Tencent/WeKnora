package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type messageFeedbackService struct {
	repo             interfaces.MessageFeedbackRepository
	messageRepo      interfaces.MessageRepository
	sessionRepo      interfaces.SessionRepository
	systemSettingSvc interfaces.SystemSettingService
}

func NewMessageFeedbackService(
	repo interfaces.MessageFeedbackRepository,
	messageRepo interfaces.MessageRepository,
	sessionRepo interfaces.SessionRepository,
	systemSettingSvc interfaces.SystemSettingService,
) interfaces.MessageFeedbackService {
	return &messageFeedbackService{
		repo:             repo,
		messageRepo:      messageRepo,
		sessionRepo:      sessionRepo,
		systemSettingSvc: systemSettingSvc,
	}
}

func (s *messageFeedbackService) UpsertFeedback(
	ctx context.Context,
	sessionID, messageID string,
	req *types.MessageFeedbackRequest,
) (*types.MessageFeedback, error) {
	if req == nil || !req.Action.Valid() {
		return nil, errors.New("invalid feedback action")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	if _, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID); err != nil {
		return nil, err
	}
	message, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	if message.Role != "assistant" {
		return nil, errors.New("feedback can only be submitted for assistant messages")
	}
	if !message.IsCompleted {
		return nil, errors.New("feedback can only be submitted after the assistant message is completed")
	}

	userID := types.SessionOwnerIDFromContext(ctx)
	feedback := &types.MessageFeedback{
		TenantID:  tenantID,
		SessionID: sessionID,
		MessageID: messageID,
		UserID:    userID,
		Action:    req.Action,
		Reason:    req.Reason,
	}

	chunkIDs, err := s.referencedChunkIDs(ctx, tenantID, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	saved, err := s.repo.UpsertFeedbackAndRefreshChunks(ctx, feedback, chunkIDs, resolveChunkFeedbackConfig(ctx, s.systemSettingSvc))
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func (s *messageFeedbackService) CancelFeedback(ctx context.Context, sessionID, messageID string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	if _, err := s.sessionRepo.Get(ctx, tenantID, sessionUserIDForLookup(ctx), sessionID); err != nil {
		return err
	}
	if _, err := s.messageRepo.GetMessage(ctx, sessionID, messageID); err != nil {
		return err
	}

	chunkIDs, err := s.referencedChunkIDs(ctx, tenantID, sessionID, messageID)
	if err != nil {
		return err
	}
	return s.repo.DeleteFeedbackAndRefreshChunks(ctx, tenantID, sessionID, messageID, types.SessionOwnerIDFromContext(ctx), chunkIDs, resolveChunkFeedbackConfig(ctx, s.systemSettingSvc))
}

func (s *messageFeedbackService) RecordMessageChunkReferences(ctx context.Context, message *types.Message) error {
	if message == nil || len(message.KnowledgeReferences) == 0 {
		return nil
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	seen := make(map[string]struct{})
	refs := make([]*types.MessageChunkReference, 0, len(message.KnowledgeReferences))
	for _, ref := range message.KnowledgeReferences {
		if ref == nil || ref.ID == "" || ref.ChunkType == types.ChunkTypeWebSearch {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		refs = append(refs, &types.MessageChunkReference{
			TenantID:        tenantID,
			SessionID:       message.SessionID,
			MessageID:       message.ID,
			ChunkID:         ref.ID,
			KnowledgeID:     ref.KnowledgeID,
			KnowledgeBaseID: ref.KnowledgeBaseID,
		})
	}
	return s.repo.CreateMessageChunkReferences(ctx, refs)
}

func (s *messageFeedbackService) applyFeedbackDeltaToReferencedChunks(
	ctx context.Context,
	tenantID uint64,
	sessionID, messageID string,
	likeDelta, dislikeDelta int64,
) error {
	refs, err := s.repo.ListMessageChunkReferences(ctx, tenantID, sessionID, messageID)
	if err != nil {
		return err
	}
	chunkIDs := make([]string, 0, len(refs))
	for _, ref := range refs {
		chunkIDs = append(chunkIDs, ref.ChunkID)
	}
	return s.repo.ApplyChunkFeedbackDelta(ctx, tenantID, chunkIDs, likeDelta, dislikeDelta, messageID, resolveChunkFeedbackConfig(ctx, s.systemSettingSvc))
}

func (s *messageFeedbackService) referencedChunkIDs(ctx context.Context, tenantID uint64, sessionID, messageID string) ([]string, error) {
	refs, err := s.repo.ListMessageChunkReferences(ctx, tenantID, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	chunkIDs := make([]string, 0, len(refs))
	for _, ref := range refs {
		chunkIDs = append(chunkIDs, ref.ChunkID)
	}
	return chunkIDs, nil
}

func resolveChunkFeedbackConfig(ctx context.Context, svc interfaces.SystemSettingService) types.ChunkFeedbackConfig {
	cfg := types.DefaultChunkFeedbackConfig()
	if svc == nil {
		return cfg
	}
	cfg.HighPositiveRateThreshold = percentToRate(svc.GetInt(ctx,
		"chunk_feedback.high_positive_rate_percent", "WEKNORA_CHUNK_FEEDBACK_HIGH_RATE_PERCENT", 80), cfg.HighPositiveRateThreshold)
	cfg.LowPositiveRateThreshold = percentToRate(svc.GetInt(ctx,
		"chunk_feedback.low_positive_rate_percent", "WEKNORA_CHUNK_FEEDBACK_LOW_RATE_PERCENT", 50), cfg.LowPositiveRateThreshold)
	cfg.OptimizeRateThreshold = percentToRate(svc.GetInt(ctx,
		"chunk_feedback.optimize_rate_percent", "WEKNORA_CHUNK_FEEDBACK_OPTIMIZE_RATE_PERCENT", 30), cfg.OptimizeRateThreshold)
	cfg.HighRecallWeight = percentToWeight(svc.GetInt(ctx,
		"chunk_feedback.high_recall_weight_percent", "WEKNORA_CHUNK_FEEDBACK_HIGH_WEIGHT_PERCENT", 120), cfg.HighRecallWeight)
	cfg.DefaultRecallWeight = percentToWeight(svc.GetInt(ctx,
		"chunk_feedback.default_recall_weight_percent", "WEKNORA_CHUNK_FEEDBACK_DEFAULT_WEIGHT_PERCENT", 100), cfg.DefaultRecallWeight)
	cfg.LowRecallWeight = percentToWeight(svc.GetInt(ctx,
		"chunk_feedback.low_recall_weight_percent", "WEKNORA_CHUNK_FEEDBACK_LOW_WEIGHT_PERCENT", 80), cfg.LowRecallWeight)
	return cfg
}

func percentToRate(value int64, fallback float64) float64 {
	if value < 0 || value > 100 {
		return fallback
	}
	return float64(value) / 100
}

func percentToWeight(value int64, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return float64(value) / 100
}

func feedbackDelta(previous, current *types.MessageFeedback) (int64, int64) {
	var likeDelta, dislikeDelta int64
	if previous != nil {
		if previous.Action == types.FeedbackActionLike {
			likeDelta--
		}
		if previous.Action == types.FeedbackActionDislike {
			dislikeDelta--
		}
	}
	if current != nil {
		if current.Action == types.FeedbackActionLike {
			likeDelta++
		}
		if current.Action == types.FeedbackActionDislike {
			dislikeDelta++
		}
	}
	return likeDelta, dislikeDelta
}
