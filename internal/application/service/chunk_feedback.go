package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/chunkfeedback"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	ErrInvalidFeedbackRequest     = errors.New("feedback request is required")
	ErrFeedbackTargetNotAssistant = errors.New("feedback target must be an assistant message")
	ErrFeedbackTargetNotCompleted = errors.New("feedback target must be a completed assistant message")
	ErrDislikeReasonRequired      = errors.New("dislike reason is required for negative feedback")
	ErrDislikeReasonTooLong       = errors.New("dislike reason is too long")
)

type feedbackChunkLocker interface {
	LockChunkForFeedback(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error)
}

type feedbackMessageLocker interface {
	LockMessageForFeedback(ctx context.Context, tenantID uint64, userID, messageID string) (*types.Message, error)
}

type feedbackRecordLocker interface {
	LockByMessageAndUser(ctx context.Context, tenantID uint64, messageID, userID string) (*types.ChunkFeedback, error)
}

type feedbackChunkRef struct {
	ChunkID       string
	ChunkTenantID uint64
}

// ChunkFeedbackService 片段反馈服务
type ChunkFeedbackService struct {
	qaRefRepo     interfaces.QAReplyChunkRefRepository
	feedbackRepo  interfaces.ChunkFeedbackRepository
	messageRepo   interfaces.MessageRepository
	chunkRepo     interfaces.ChunkRepository
	weightLogRepo interfaces.ChunkWeightLogRepository
	uow           interfaces.ChunkFeedbackUnitOfWork
	config        *types.ChunkFeedbackConfig
}

// NewChunkFeedbackService 创建反馈服务实例
func NewChunkFeedbackService(
	qaRefRepo interfaces.QAReplyChunkRefRepository,
	feedbackRepo interfaces.ChunkFeedbackRepository,
	messageRepo interfaces.MessageRepository,
	chunkRepo interfaces.ChunkRepository,
	weightLogRepo interfaces.ChunkWeightLogRepository,
) *ChunkFeedbackService {
	return &ChunkFeedbackService{
		qaRefRepo:     qaRefRepo,
		feedbackRepo:  feedbackRepo,
		messageRepo:   messageRepo,
		chunkRepo:     chunkRepo,
		weightLogRepo: weightLogRepo,
		config:        types.DefaultChunkFeedbackConfig(),
	}
}

func NewChunkFeedbackServiceWithUnitOfWork(
	qaRefRepo interfaces.QAReplyChunkRefRepository,
	feedbackRepo interfaces.ChunkFeedbackRepository,
	messageRepo interfaces.MessageRepository,
	chunkRepo interfaces.ChunkRepository,
	weightLogRepo interfaces.ChunkWeightLogRepository,
	uow interfaces.ChunkFeedbackUnitOfWork,
) *ChunkFeedbackService {
	svc := NewChunkFeedbackService(qaRefRepo, feedbackRepo, messageRepo, chunkRepo, weightLogRepo)
	svc.uow = uow
	return svc
}

func NewConfiguredChunkFeedbackServiceWithUnitOfWork(
	qaRefRepo interfaces.QAReplyChunkRefRepository,
	feedbackRepo interfaces.ChunkFeedbackRepository,
	messageRepo interfaces.MessageRepository,
	chunkRepo interfaces.ChunkRepository,
	weightLogRepo interfaces.ChunkWeightLogRepository,
	uow interfaces.ChunkFeedbackUnitOfWork,
	config *types.ChunkFeedbackConfig,
) *ChunkFeedbackService {
	svc := NewChunkFeedbackServiceWithUnitOfWork(
		qaRefRepo,
		feedbackRepo,
		messageRepo,
		chunkRepo,
		weightLogRepo,
		uow,
	)
	if config != nil {
		configCopy := *config
		svc.config = &configCopy
	}
	return svc
}

func (s *ChunkFeedbackService) withFeedbackTransaction(ctx context.Context, fn func(context.Context, *ChunkFeedbackService) error) error {
	if s.uow == nil {
		return fn(ctx, s)
	}
	return s.uow.Do(ctx, func(txCtx context.Context, repos interfaces.ChunkFeedbackRepositories) error {
		txSvc := *s
		txSvc.qaRefRepo = repos.QARefRepo
		txSvc.feedbackRepo = repos.FeedbackRepo
		txSvc.messageRepo = repos.MessageRepo
		txSvc.chunkRepo = repos.ChunkRepo
		txSvc.weightLogRepo = repos.WeightLogRepo
		txSvc.uow = nil
		return fn(txCtx, &txSvc)
	})
}

// SubmitFeedback 处理用户提交反馈
func (s *ChunkFeedbackService) SubmitFeedback(ctx context.Context, tenantID uint64, userID string, req *types.SubmitFeedbackRequest) error {
	if err := normalizeFeedbackRequest(req); err != nil {
		return err
	}
	logger.Infof(ctx, "Processing feedback submission: messageID=%s, isPositive=%v, tenantID=%d",
		req.MessageID, req.IsPositive, tenantID)
	return s.withFeedbackTransaction(ctx, func(txCtx context.Context, txSvc *ChunkFeedbackService) error {
		return txSvc.submitFeedback(txCtx, tenantID, userID, req)
	})
}

func (s *ChunkFeedbackService) submitFeedback(ctx context.Context, tenantID uint64, userID string, req *types.SubmitFeedbackRequest) error {
	message, err := s.messageRepo.GetMessageByID(ctx, tenantID, userID, req.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %w", err)
	}
	if message.Role != "assistant" {
		return ErrFeedbackTargetNotAssistant
	}
	if !message.IsCompleted {
		return ErrFeedbackTargetNotCompleted
	}

	chunkRefs, backfilledChunkRefs, err := s.resolveFeedbackChunkRefs(ctx, tenantID, req.MessageID, message)
	if err != nil {
		return err
	}

	feedback, err := s.feedbackRepo.Upsert(ctx, req.MessageID, message.SessionID, userID, tenantID, req.IsPositive, req.DislikeReason)
	if err != nil {
		return fmt.Errorf("failed to upsert feedback: %w", err)
	}
	if err := s.updateMessageFeedbackStats(ctx, tenantID, userID, message, feedback); err != nil {
		return err
	}

	if len(chunkRefs) == 0 {
		logger.Warnf(ctx, "No chunk refs found for message %s", req.MessageID)
		return nil
	}

	if !feedback.IsChanged && len(backfilledChunkRefs) == 0 {
		logger.Infof(ctx, "Feedback unchanged for message %s, skipping chunk updates", req.MessageID)
		return nil
	}

	chunkFeedback := feedback
	chunksToUpdate := chunkRefs
	if !feedback.IsChanged {
		backfill := *feedback
		backfill.WasCreated = true
		backfill.IsChanged = true
		chunkFeedback = &backfill
		chunksToUpdate = backfilledChunkRefs
	}

	if err := s.updateChunksFeedbackStats(ctx, tenantID, chunksToUpdate, chunkFeedback, req.DislikeReason); err != nil {
		logger.Errorf(ctx, "Failed to update chunks feedback stats: %v", err)
		return err
	}

	logger.Infof(ctx, "Feedback processed successfully for message %s, %d chunks affected", req.MessageID, len(chunksToUpdate))
	return nil
}

// CancelFeedback 取消用户对某条回答的反馈，并回退关联片段上的累计计数。
func (s *ChunkFeedbackService) CancelFeedback(ctx context.Context, tenantID uint64, userID, messageID string) error {
	return s.withFeedbackTransaction(ctx, func(txCtx context.Context, txSvc *ChunkFeedbackService) error {
		return txSvc.cancelFeedback(txCtx, tenantID, userID, messageID)
	})
}

func (s *ChunkFeedbackService) cancelFeedback(ctx context.Context, tenantID uint64, userID, messageID string) error {
	var (
		feedback *types.ChunkFeedback
		err      error
	)
	if locker, ok := s.feedbackRepo.(feedbackRecordLocker); ok {
		feedback, err = locker.LockByMessageAndUser(ctx, tenantID, messageID, userID)
	} else {
		feedback, err = s.feedbackRepo.GetByMessageAndUser(ctx, tenantID, messageID, userID)
	}
	if err != nil {
		return fmt.Errorf("failed to get feedback: %w", err)
	}
	if feedback == nil {
		return nil
	}

	message, err := s.messageRepo.GetMessageByID(ctx, tenantID, userID, messageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %w", err)
	}
	if message.Role != "assistant" {
		return ErrFeedbackTargetNotAssistant
	}

	chunkRefs, _, err := s.resolveFeedbackChunkRefs(ctx, tenantID, messageID, message)
	if err != nil {
		return err
	}

	if err := s.feedbackRepo.Delete(ctx, tenantID, feedback.ID); err != nil {
		return fmt.Errorf("failed to delete feedback: %w", err)
	}
	if err := s.cancelMessageFeedbackStats(ctx, tenantID, userID, message, feedback.IsPositive); err != nil {
		return err
	}

	if err := s.cancelChunksFeedbackStats(ctx, tenantID, messageID, chunkRefs, feedback.IsPositive); err != nil {
		return err
	}
	return nil
}

func (s *ChunkFeedbackService) updateChunksFeedbackStats(ctx context.Context, tenantID uint64, chunkRefs []feedbackChunkRef, feedback *types.ChunkFeedback, dislikeReason string) error {
	lockedChunks, err := s.lockFeedbackChunks(ctx, tenantID, chunkRefs)
	if err != nil {
		return err
	}
	activeRefs, err := s.excludeResetChunkRefs(ctx, tenantID, feedback.MessageID, feedbackRefsFromLockedChunks(lockedChunks))
	if err != nil {
		return err
	}
	active := feedbackChunkRefSet(activeRefs)
	for _, locked := range lockedChunks {
		if _, ok := active[feedbackChunkRefKey(locked.ref)]; !ok {
			continue
		}
		if err := s.updateLockedChunkFeedbackStats(ctx, locked.chunk, feedback, dislikeReason); err != nil {
			return fmt.Errorf("failed to update chunk %s feedback stats: %w", locked.ref.ChunkID, err)
		}
	}
	return nil
}

func (s *ChunkFeedbackService) cancelChunksFeedbackStats(
	ctx context.Context,
	tenantID uint64,
	messageID string,
	chunkRefs []feedbackChunkRef,
	wasPositive bool,
) error {
	lockedChunks, err := s.lockFeedbackChunks(ctx, tenantID, chunkRefs)
	if err != nil {
		return err
	}
	activeRefs, err := s.excludeResetChunkRefs(ctx, tenantID, messageID, feedbackRefsFromLockedChunks(lockedChunks))
	if err != nil {
		return err
	}
	active := feedbackChunkRefSet(activeRefs)
	for _, locked := range lockedChunks {
		if _, ok := active[feedbackChunkRefKey(locked.ref)]; !ok {
			continue
		}
		if err := s.cancelLockedChunkFeedbackStats(ctx, locked.chunk, wasPositive); err != nil {
			return fmt.Errorf("failed to cancel chunk %s feedback stats: %w", locked.ref.ChunkID, err)
		}
	}
	return nil
}

func (s *ChunkFeedbackService) updateMessageFeedbackStats(ctx context.Context, tenantID uint64, userID string, message *types.Message, feedback *types.ChunkFeedback) error {
	if locker, ok := s.messageRepo.(feedbackMessageLocker); ok {
		locked, err := locker.LockMessageForFeedback(ctx, tenantID, userID, message.ID)
		if err != nil {
			return fmt.Errorf("failed to lock message feedback stats: %w", err)
		}
		message = locked
	}
	state := chunkfeedback.ApplyVote(
		chunkfeedback.State{
			LikeCount:    message.LikeCount,
			DislikeCount: message.DislikeCount,
		},
		chunkfeedback.VoteChange{
			WasCreated: feedback.WasCreated,
			IsChanged:  feedback.IsChanged,
			IsPositive: feedback.IsPositive,
		},
		chunkFeedbackConfig(s.config),
	)
	if err := s.messageRepo.UpdateMessageFeedbackStats(ctx, tenantID, userID, message.ID, state.LikeCount, state.DislikeCount); err != nil {
		return fmt.Errorf("failed to update message feedback stats: %w", err)
	}
	return nil
}

func (s *ChunkFeedbackService) cancelMessageFeedbackStats(ctx context.Context, tenantID uint64, userID string, message *types.Message, wasPositive bool) error {
	if locker, ok := s.messageRepo.(feedbackMessageLocker); ok {
		locked, err := locker.LockMessageForFeedback(ctx, tenantID, userID, message.ID)
		if err != nil {
			return fmt.Errorf("failed to lock message feedback stats: %w", err)
		}
		message = locked
	}
	state := chunkfeedback.CancelVote(
		chunkfeedback.State{
			LikeCount:    message.LikeCount,
			DislikeCount: message.DislikeCount,
		},
		wasPositive,
		chunkFeedbackConfig(s.config),
	)
	if err := s.messageRepo.UpdateMessageFeedbackStats(ctx, tenantID, userID, message.ID, state.LikeCount, state.DislikeCount); err != nil {
		return fmt.Errorf("failed to update message feedback stats: %w", err)
	}
	return nil
}

func (s *ChunkFeedbackService) updateSingleChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, feedback *types.ChunkFeedback, dislikeReason string) error {
	chunk, err := s.getChunkForFeedbackUpdate(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}
	return s.updateLockedChunkFeedbackStats(ctx, chunk, feedback, dislikeReason)
}

func (s *ChunkFeedbackService) updateLockedChunkFeedbackStats(
	ctx context.Context,
	chunk *types.Chunk,
	feedback *types.ChunkFeedback,
	dislikeReason string,
) error {
	oldWeight := chunk.RecallWeight

	state := chunkfeedback.ApplyVote(
		chunkFeedbackState(chunk),
		chunkfeedback.VoteChange{
			WasCreated: feedback.WasCreated,
			IsChanged:  feedback.IsChanged,
			IsPositive: feedback.IsPositive,
		},
		chunkFeedbackConfig(s.config),
	)
	applyChunkFeedbackState(chunk, state)

	if !feedback.IsPositive && (feedback.WasCreated || feedback.IsChanged) {
		if dislikeReason != "" {
			var reasons []string
			if chunk.DislikeReasons != nil {
				_ = json.Unmarshal(chunk.DislikeReasons, &reasons)
			}
			for _, r := range reasons {
				if r == dislikeReason {
					dislikeReason = ""
					break
				}
			}
			if dislikeReason != "" {
				reasons = append(reasons, dislikeReason)
				chunk.DislikeReasons, _ = json.Marshal(reasons)
			}
		}
	}

	if err := s.chunkRepo.UpdateChunkFeedbackStats(ctx, chunk.TenantID, chunk.ID, chunk.LikeCount, chunk.DislikeCount,
		chunk.PositiveRate, chunk.RecallWeight, chunk.QualityStatus); err != nil {
		return fmt.Errorf("failed to update chunk stats: %w", err)
	}

	if err := s.chunkRepo.UpdateChunkLastFeedbackAt(ctx, chunk.TenantID, chunk.ID); err != nil {
		return fmt.Errorf("failed to update chunk last feedback time: %w", err)
	}

	if oldWeight != chunk.RecallWeight {
		triggerType := types.FeedbackTriggerUserLike
		if !feedback.IsPositive {
			triggerType = types.FeedbackTriggerUserDislike
		}
		if err := s.recordWeightChange(ctx, chunk.ID, chunk.TenantID, "adjust_weight", oldWeight, chunk.RecallWeight, triggerType, weightTriggerDetail(feedback.MessageID, chunk), ""); err != nil {
			return fmt.Errorf("failed to record chunk weight change: %w", err)
		}
	}

	return nil
}

func (s *ChunkFeedbackService) cancelSingleChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, wasPositive bool) error {
	chunk, err := s.getChunkForFeedbackUpdate(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}
	return s.cancelLockedChunkFeedbackStats(ctx, chunk, wasPositive)
}

func (s *ChunkFeedbackService) cancelLockedChunkFeedbackStats(ctx context.Context, chunk *types.Chunk, wasPositive bool) error {
	oldWeight := chunk.RecallWeight
	state := chunkfeedback.CancelVote(chunkFeedbackState(chunk), wasPositive, chunkFeedbackConfig(s.config))
	applyChunkFeedbackState(chunk, state)

	if err := s.chunkRepo.UpdateChunkFeedbackStats(ctx, chunk.TenantID, chunk.ID, chunk.LikeCount, chunk.DislikeCount,
		chunk.PositiveRate, chunk.RecallWeight, chunk.QualityStatus); err != nil {
		return fmt.Errorf("failed to update chunk stats: %w", err)
	}
	if err := s.chunkRepo.UpdateChunkLastFeedbackAt(ctx, chunk.TenantID, chunk.ID); err != nil {
		return fmt.Errorf("failed to update chunk last feedback time: %w", err)
	}

	if oldWeight != chunk.RecallWeight {
		if err := s.recordWeightChange(ctx, chunk.ID, chunk.TenantID, "adjust_weight", oldWeight, chunk.RecallWeight, types.FeedbackTriggerUserCancel, "", ""); err != nil {
			return fmt.Errorf("failed to record chunk weight change: %w", err)
		}
	}
	return nil
}

func (s *ChunkFeedbackService) getChunkForFeedbackUpdate(ctx context.Context, tenantID uint64, chunkID string) (*types.Chunk, error) {
	var (
		chunk *types.Chunk
		err   error
	)
	if locker, ok := s.chunkRepo.(feedbackChunkLocker); ok {
		chunk, err = locker.LockChunkForFeedback(ctx, tenantID, chunkID)
	} else {
		chunk, err = s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	}
	if err != nil {
		return nil, err
	}
	if chunk != nil {
		if chunk.ID == "" {
			chunk.ID = chunkID
		}
		if chunk.TenantID == 0 {
			chunk.TenantID = tenantID
		}
	}
	return chunk, nil
}

type lockedFeedbackChunk struct {
	ref   feedbackChunkRef
	chunk *types.Chunk
}

func (s *ChunkFeedbackService) lockFeedbackChunks(
	ctx context.Context,
	defaultTenantID uint64,
	refs []feedbackChunkRef,
) ([]lockedFeedbackChunk, error) {
	normalizeAndSortFeedbackChunkRefs(defaultTenantID, refs)
	locked := make([]lockedFeedbackChunk, 0, len(refs))
	for _, ref := range refs {
		chunk, err := s.getChunkForFeedbackUpdate(ctx, ref.ChunkTenantID, ref.ChunkID)
		if err != nil {
			if errors.Is(err, ErrChunkNotFound) {
				logger.Warnf(ctx, "Skipping deleted chunk %s while updating feedback stats", ref.ChunkID)
				continue
			}
			return nil, fmt.Errorf("failed to lock chunk %s feedback stats: %w", ref.ChunkID, err)
		}
		locked = append(locked, lockedFeedbackChunk{ref: ref, chunk: chunk})
	}
	return locked, nil
}

func feedbackRefsFromLockedChunks(chunks []lockedFeedbackChunk) []feedbackChunkRef {
	refs := make([]feedbackChunkRef, 0, len(chunks))
	for _, chunk := range chunks {
		refs = append(refs, chunk.ref)
	}
	return refs
}

func feedbackChunkRefSet(refs []feedbackChunkRef) map[string]struct{} {
	result := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		result[feedbackChunkRefKey(ref)] = struct{}{}
	}
	return result
}

func chunkFeedbackState(chunk *types.Chunk) chunkfeedback.State {
	return chunkfeedback.State{
		LikeCount:     chunk.LikeCount,
		DislikeCount:  chunk.DislikeCount,
		PositiveRate:  chunk.PositiveRate,
		RecallWeight:  chunk.RecallWeight,
		QualityStatus: string(chunk.QualityStatus),
	}
}

func applyChunkFeedbackState(chunk *types.Chunk, state chunkfeedback.State) {
	chunk.LikeCount = state.LikeCount
	chunk.DislikeCount = state.DislikeCount
	chunk.PositiveRate = state.PositiveRate
	chunk.RecallWeight = state.RecallWeight
	chunk.QualityStatus = types.ChunkQualityStatus(state.QualityStatus)
}

func chunkFeedbackConfig(config *types.ChunkFeedbackConfig) chunkfeedback.Config {
	return chunkfeedback.Config{
		HighQualityThreshold: config.HighQualityThreshold,
		LowQualityThreshold:  config.LowQualityThreshold,
		WeightBoostFactor:    config.WeightBoostFactor,
		WeightPenaltyFactor:  config.WeightPenaltyFactor,
		AutoMarkThreshold:    config.AutoMarkThreshold,
		AutoMarkMinFeedbacks: config.AutoMarkMinFeedbacks,
		MinWeight:            config.MinWeight,
		MaxWeight:            config.MaxWeight,
	}
}

func (s *ChunkFeedbackService) recordWeightChange(ctx context.Context, chunkID string, tenantID uint64, action string, oldWeight, newWeight float64, triggerType types.FeedbackTriggerType, triggerDetail, operator string) error {
	log := &types.ChunkWeightLog{
		ChunkID:       chunkID,
		TenantID:      tenantID,
		Action:        action,
		OldWeight:     oldWeight,
		NewWeight:     newWeight,
		TriggerType:   triggerType,
		TriggerDetail: triggerDetail,
		Operator:      operator,
	}
	return s.weightLogRepo.Create(ctx, log)
}

// GetChunkStats 获取片段统计
func (s *ChunkFeedbackService) GetChunkStats(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkStatsResponse, error) {
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}
	stats := &types.ChunkStatsResponse{
		ChunkID:        chunk.ID,
		LikeCount:      chunk.LikeCount,
		DislikeCount:   chunk.DislikeCount,
		PositiveRate:   chunk.PositiveRate,
		RecallWeight:   chunk.RecallWeight,
		QualityStatus:  string(chunk.QualityStatus),
		LastFeedbackAt: chunk.LastFeedbackAt,
	}
	sessionCount, err := s.qaRefRepo.CountSessionsByChunkID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, fmt.Errorf("failed to count related sessions: %w", err)
	}
	stats.RelatedSessionCount = int(sessionCount)
	reasonMap, err := s.feedbackRepo.GetDislikeReasonsByChunkIDs(ctx, tenantID, []string{chunkID})
	if err != nil {
		return nil, fmt.Errorf("failed to get dislike reasons: %w", err)
	}
	if reasons, ok := reasonMap[chunkID]; ok {
		stats.DislikeReasons = reasons
		stats.DislikeReasonStats = aggregateDislikeReasons(reasons)
	}
	return stats, nil
}

// ListLowQualityChunks 列出低质量片段
func (s *ChunkFeedbackService) ListLowQualityChunks(ctx context.Context, tenantID uint64, knowledgeBaseID string, maxRate float64, limit, offset int) ([]*types.ChunkQualityStats, error) {
	chunks, err := s.chunkRepo.ListLowQualityChunks(ctx, tenantID, knowledgeBaseID, maxRate, limit, offset)
	if err != nil {
		return nil, err
	}
	stats := make([]*types.ChunkQualityStats, len(chunks))
	for i, chunk := range chunks {
		stats[i] = &types.ChunkQualityStats{
			ChunkID:       chunk.ID,
			KnowledgeID:   chunk.KnowledgeID,
			Content:       truncateContent(chunk.Content, 100),
			LikeCount:     chunk.LikeCount,
			DislikeCount:  chunk.DislikeCount,
			PositiveRate:  chunk.PositiveRate,
			RecallWeight:  chunk.RecallWeight,
			QualityStatus: string(chunk.QualityStatus),
			UpdatedAt:     chunk.UpdatedAt,
		}
	}
	return stats, nil
}

// CountLowQualityChunks 统计低质量片段数量
func (s *ChunkFeedbackService) CountLowQualityChunks(ctx context.Context, tenantID uint64, knowledgeBaseID string, maxRate float64) (int64, error) {
	return s.chunkRepo.CountLowQualityChunks(ctx, tenantID, knowledgeBaseID, maxRate)
}

// GetFeedbackOverview 获取片段反馈聚合概览
func (s *ChunkFeedbackService) GetFeedbackOverview(ctx context.Context, tenantID uint64, knowledgeBaseID string) (*types.ChunkFeedbackOverviewResponse, error) {
	config := chunkFeedbackConfig(s.config)
	return s.chunkRepo.GetChunkFeedbackOverview(ctx, tenantID, knowledgeBaseID, config.HighQualityThreshold, config.LowQualityThreshold)
}

func truncateContent(content string, maxLen int) string {
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "..."
}

// ResetChunkFeedback 重置片段反馈数据
func (s *ChunkFeedbackService) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkID, operator string) error {
	return s.withFeedbackTransaction(ctx, func(txCtx context.Context, txSvc *ChunkFeedbackService) error {
		return txSvc.resetChunkFeedback(txCtx, tenantID, chunkID, operator)
	})
}

func (s *ChunkFeedbackService) resetChunkFeedback(ctx context.Context, tenantID uint64, chunkID, operator string) error {
	chunk, err := s.getChunkForFeedbackUpdate(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}
	refs, err := s.qaRefRepo.GetByChunkID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk feedback refs: %w", err)
	}
	if err := s.qaRefRepo.CreateResetTombstones(ctx, refs, operator); err != nil {
		return fmt.Errorf("failed to create chunk feedback reset tombstones: %w", err)
	}
	if err := s.chunkRepo.ResetChunkFeedback(ctx, tenantID, chunkID); err != nil {
		return fmt.Errorf("failed to reset chunk feedback: %w", err)
	}
	if err := s.recordWeightChange(ctx, chunkID, tenantID, "reset", chunk.RecallWeight, 1.0, types.FeedbackTriggerAdminReset, "", operator); err != nil {
		return fmt.Errorf("failed to record reset weight change: %w", err)
	}
	logger.Infof(ctx, "Chunk feedback reset successfully: chunkID=%s, operator=%s", chunkID, operator)
	return nil
}

// GetWeightLogs 获取权重变更日志
func (s *ChunkFeedbackService) GetWeightLogs(ctx context.Context, tenantID uint64, chunkID string, limit int) (*types.WeightLogResponse, error) {
	logs, err := s.weightLogRepo.GetByChunkID(ctx, tenantID, chunkID, limit)
	if err != nil {
		return nil, err
	}
	total, err := s.weightLogRepo.CountByChunkID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, fmt.Errorf("failed to count weight logs: %w", err)
	}
	return &types.WeightLogResponse{Logs: logs, Total: total}, nil
}

// SaveQAReplyChunkRefs 保存问答回复与片段的关联关系
func (s *ChunkFeedbackService) SaveQAReplyChunkRefs(ctx context.Context, tenantID uint64, messageID string, chunkIDs []string) error {
	return s.saveQAReplyChunkRefs(ctx, tenantID, messageID, newFeedbackChunkRefs(tenantID, chunkIDs))
}

// SaveQAReplySearchResultRefs persists every trackable chunk represented by the
// reply references, including chunks absorbed into a merged result. Chunk
// ownership is resolved from the source rows so shared knowledge bases retain
// the session tenant on the feedback record without losing the owner tenant
// needed to update chunk statistics.
func (s *ChunkFeedbackService) SaveQAReplySearchResultRefs(
	ctx context.Context,
	tenantID uint64,
	messageID string,
	results []*types.SearchResult,
) error {
	chunkIDs := types.CollectSearchResultChunkIDs(results)
	chunkRefs, err := s.existingFeedbackChunkRefsForIDs(ctx, chunkIDs)
	if err != nil {
		return err
	}
	return s.saveQAReplyChunkRefs(ctx, tenantID, messageID, chunkRefs)
}

// PersistCompletedReply is the common completion boundary for assistant
// messages. Keeping the message update and reply-chunk associations in the same
// unit of work prevents Agent and IM replies from depending on lazy backfill at
// the time of the first feedback request.
func (s *ChunkFeedbackService) PersistCompletedReply(
	ctx context.Context,
	tenantID uint64,
	message *types.Message,
) error {
	return s.withFeedbackTransaction(ctx, func(ctx context.Context, txSvc *ChunkFeedbackService) error {
		if err := txSvc.messageRepo.UpdateMessage(ctx, message); err != nil {
			return fmt.Errorf("failed to update completed assistant message: %w", err)
		}
		if err := txSvc.SaveQAReplySearchResultRefs(
			ctx,
			tenantID,
			message.ID,
			[]*types.SearchResult(message.KnowledgeReferences),
		); err != nil {
			return fmt.Errorf("failed to persist completed reply chunk refs: %w", err)
		}
		return nil
	})
}

func (s *ChunkFeedbackService) saveQAReplyChunkRefs(ctx context.Context, tenantID uint64, messageID string, chunkRefs []feedbackChunkRef) error {
	chunkRefs = mergeFeedbackChunkRefs(nil, chunkRefs)
	refs := make([]*types.QAReplyChunkRef, len(chunkRefs))
	for i, chunkRef := range chunkRefs {
		refs[i] = &types.QAReplyChunkRef{
			MessageID:     messageID,
			ChunkID:       chunkRef.ChunkID,
			TenantID:      tenantID,
			ChunkTenantID: chunkRef.ChunkTenantID,
		}
	}
	return s.qaRefRepo.CreateBatch(ctx, refs)
}

// existingFeedbackChunkRefsForIDs is intentionally stricter than the legacy
// feedback-time backfill resolver. At message completion there is no safe
// tenant fallback for a missing shared chunk: unresolved IDs may have been
// deleted, and assigning the session tenant would either violate the chunk FK
// or attribute feedback to the wrong owner. Existing rows keep their source
// tenant; missing rows are skipped and lookup failures abort the transaction.
func (s *ChunkFeedbackService) existingFeedbackChunkRefsForIDs(
	ctx context.Context,
	chunkIDs []string,
) ([]feedbackChunkRef, error) {
	chunkIDs = mergeChunkIDs(nil, chunkIDs)
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	if s.chunkRepo == nil {
		return nil, errors.New("chunk repository is required to resolve completed reply refs")
	}
	chunks, err := s.chunkRepo.ListChunksByIDOnly(ctx, chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve completed reply chunk tenants: %w", err)
	}
	chunkByID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil || strings.TrimSpace(chunk.ID) == "" || chunk.TenantID == 0 {
			continue
		}
		chunkByID[chunk.ID] = chunk
	}
	refs := make([]feedbackChunkRef, 0, len(chunkByID))
	for _, chunkID := range chunkIDs {
		chunk := chunkByID[chunkID]
		if chunk == nil {
			logger.Warnf(ctx, "Skipping missing chunk %s while persisting completed reply refs", chunkID)
			continue
		}
		refs = append(refs, feedbackChunkRef{
			ChunkID:       chunk.ID,
			ChunkTenantID: chunk.TenantID,
		})
	}
	return refs, nil
}

func (s *ChunkFeedbackService) resolveFeedbackChunkRefs(ctx context.Context, tenantID uint64, messageID string, message *types.Message) ([]feedbackChunkRef, []feedbackChunkRef, error) {
	refs, err := s.qaRefRepo.GetByMessageID(ctx, tenantID, messageID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get chunk refs: %w", err)
	}
	allChunkRefs := collectQAFeedbackRefs(tenantID, refs)
	normalizeAndSortFeedbackChunkRefs(tenantID, allChunkRefs)
	chunkRefs, err := s.excludeResetChunkRefs(ctx, tenantID, messageID, allChunkRefs)
	if err != nil {
		return nil, nil, err
	}
	if message == nil {
		return chunkRefs, nil, nil
	}

	referenceChunkIDs := types.CollectSearchResultChunkIDs([]*types.SearchResult(message.KnowledgeReferences))
	chunkIDs := feedbackRefIDs(allChunkRefs)
	backfilledChunkIDs := missingChunkIDs(chunkIDs, referenceChunkIDs)
	backfilledChunkRefs := s.feedbackChunkRefsForIDs(ctx, tenantID, backfilledChunkIDs)
	backfilledChunkRefs, err = s.excludeResetChunkRefs(ctx, tenantID, messageID, backfilledChunkRefs)
	if err != nil {
		return nil, nil, err
	}
	if len(backfilledChunkIDs) > 0 {
		if err := s.saveQAReplyChunkRefs(ctx, tenantID, messageID, backfilledChunkRefs); err != nil {
			logger.Warnf(ctx, "Failed to backfill QA chunk refs for message %s: %v", messageID, err)
		}
		chunkRefs = append(chunkRefs, backfilledChunkRefs...)
	}
	normalizeAndSortFeedbackChunkRefs(tenantID, chunkRefs)
	return chunkRefs, backfilledChunkRefs, nil
}

func (s *ChunkFeedbackService) excludeResetChunkRefs(ctx context.Context, tenantID uint64, messageID string, refs []feedbackChunkRef) ([]feedbackChunkRef, error) {
	if len(refs) == 0 || s.qaRefRepo == nil {
		return refs, nil
	}
	tombstones, err := s.qaRefRepo.GetResetTombstonesByMessageID(ctx, tenantID, messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get reset chunk refs: %w", err)
	}
	if len(tombstones) == 0 {
		return refs, nil
	}
	reset := make(map[string]struct{}, len(tombstones))
	for _, tombstone := range tombstones {
		if tombstone == nil {
			continue
		}
		chunkTenantID := tombstone.ChunkTenantID
		if chunkTenantID == 0 {
			chunkTenantID = tombstone.TenantID
		}
		reset[feedbackChunkRefKey(feedbackChunkRef{
			ChunkID:       tombstone.ChunkID,
			ChunkTenantID: chunkTenantID,
		})] = struct{}{}
	}
	filtered := refs[:0]
	for _, ref := range refs {
		if _, ok := reset[feedbackChunkRefKey(ref)]; ok {
			continue
		}
		filtered = append(filtered, ref)
	}
	return filtered, nil
}

func (s *ChunkFeedbackService) feedbackChunkRefsForIDs(ctx context.Context, defaultTenantID uint64, chunkIDs []string) []feedbackChunkRef {
	chunkIDs = mergeChunkIDs(nil, chunkIDs)
	tenantByChunkID := make(map[string]uint64, len(chunkIDs))
	if len(chunkIDs) > 0 && s.chunkRepo != nil {
		chunks, err := s.chunkRepo.ListChunksByIDOnly(ctx, chunkIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to resolve chunk tenants for feedback refs: %v", err)
		} else {
			for _, chunk := range chunks {
				if chunk != nil && chunk.ID != "" && chunk.TenantID != 0 {
					tenantByChunkID[chunk.ID] = chunk.TenantID
				}
			}
		}
	}
	refs := make([]feedbackChunkRef, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		chunkTenantID := tenantByChunkID[id]
		if chunkTenantID == 0 {
			chunkTenantID = defaultTenantID
		}
		refs = append(refs, feedbackChunkRef{ChunkID: id, ChunkTenantID: chunkTenantID})
	}
	return refs
}

// GetDislikeReasonOptions 获取点踩原因选项
func (s *ChunkFeedbackService) GetDislikeReasonOptions() []string {
	return types.GetDislikeReasons()
}

func normalizeFeedbackRequest(req *types.SubmitFeedbackRequest) error {
	if req == nil {
		return ErrInvalidFeedbackRequest
	}
	req.MessageID = strings.TrimSpace(req.MessageID)
	req.DislikeReason = strings.TrimSpace(req.DislikeReason)
	if req.IsPositive {
		req.DislikeReason = ""
		return nil
	}
	if req.DislikeReason == "" {
		return ErrDislikeReasonRequired
	}
	if len([]rune(req.DislikeReason)) > 255 {
		return ErrDislikeReasonTooLong
	}
	return nil
}

func collectQAFeedbackRefs(defaultTenantID uint64, refs []*types.QAReplyChunkRef) []feedbackChunkRef {
	seen := make(map[string]struct{}, len(refs))
	result := make([]feedbackChunkRef, 0, len(refs))
	for _, ref := range refs {
		if ref == nil || strings.TrimSpace(ref.ChunkID) == "" {
			continue
		}
		chunkTenantID := ref.ChunkTenantID
		if chunkTenantID == 0 {
			chunkTenantID = defaultTenantID
		}
		key := fmt.Sprintf("%d:%s", chunkTenantID, strings.TrimSpace(ref.ChunkID))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, feedbackChunkRef{ChunkID: strings.TrimSpace(ref.ChunkID), ChunkTenantID: chunkTenantID})
	}
	return result
}

func newFeedbackChunkRefs(tenantID uint64, ids []string) []feedbackChunkRef {
	ids = mergeChunkIDs(nil, ids)
	refs := make([]feedbackChunkRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, feedbackChunkRef{ChunkID: id, ChunkTenantID: tenantID})
	}
	return refs
}

func mergeFeedbackChunkRefs(base []feedbackChunkRef, extra []feedbackChunkRef) []feedbackChunkRef {
	seen := make(map[string]struct{}, len(base)+len(extra))
	merged := make([]feedbackChunkRef, 0, len(base)+len(extra))
	add := func(ref feedbackChunkRef) {
		ref.ChunkID = strings.TrimSpace(ref.ChunkID)
		if ref.ChunkID == "" {
			return
		}
		key := feedbackChunkRefKey(ref)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		merged = append(merged, ref)
	}
	for _, ref := range base {
		add(ref)
	}
	for _, ref := range extra {
		add(ref)
	}
	return merged
}

func feedbackChunkRefKey(ref feedbackChunkRef) string {
	return fmt.Sprintf("%d:%s", ref.ChunkTenantID, strings.TrimSpace(ref.ChunkID))
}

// normalizeAndSortFeedbackChunkRefs keeps row locks deterministic across
// feedback transactions. Normalizing zero tenant IDs before sorting prevents
// implicit and explicit references to the same tenant from producing different
// physical lock orders.
func normalizeAndSortFeedbackChunkRefs(defaultTenantID uint64, refs []feedbackChunkRef) {
	for i := range refs {
		refs[i].ChunkID = strings.TrimSpace(refs[i].ChunkID)
		if refs[i].ChunkTenantID == 0 {
			refs[i].ChunkTenantID = defaultTenantID
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ChunkTenantID == refs[j].ChunkTenantID {
			return refs[i].ChunkID < refs[j].ChunkID
		}
		return refs[i].ChunkTenantID < refs[j].ChunkTenantID
	})
}

func feedbackRefIDs(refs []feedbackChunkRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ChunkID)
	}
	return mergeChunkIDs(nil, ids)
}

func mergeChunkIDs(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	merged := make([]string, 0, len(base)+len(extra))
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		merged = append(merged, id)
	}
	for _, id := range base {
		add(id)
	}
	for _, id := range extra {
		add(id)
	}
	return merged
}

func missingChunkIDs(existing []string, candidates []string) []string {
	existing = mergeChunkIDs(nil, existing)
	candidates = mergeChunkIDs(nil, candidates)
	seen := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		seen[id] = struct{}{}
	}
	missing := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func aggregateDislikeReasons(reasons []string) []types.DislikeReasonStat {
	counts := make(map[string]int)
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason != "" {
			counts[reason]++
		}
	}
	stats := make([]types.DislikeReasonStat, 0, len(counts))
	for reason, count := range counts {
		stats = append(stats, types.DislikeReasonStat{Reason: reason, Count: count})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Count == stats[j].Count {
			return stats[i].Reason < stats[j].Reason
		}
		return stats[i].Count > stats[j].Count
	})
	return stats
}

func weightTriggerDetail(messageID string, chunk *types.Chunk) string {
	detail := map[string]interface{}{
		"message_id":     messageID,
		"like_count":     chunk.LikeCount,
		"dislike_count":  chunk.DislikeCount,
		"positive_rate":  chunk.PositiveRate,
		"quality_status": chunk.QualityStatus,
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return ""
	}
	return string(b)
}

// SetConfig 设置配置
func (s *ChunkFeedbackService) SetConfig(config *types.ChunkFeedbackConfig) {
	s.config = config
}

// GetUserFeedback 获取用户对指定消息的反馈状态
func (s *ChunkFeedbackService) GetUserFeedback(ctx context.Context, tenantID uint64, messageID, userID string) (*types.UserFeedbackResponse, error) {
	feedback, err := s.feedbackRepo.GetByMessageAndUser(ctx, tenantID, messageID, userID)
	if err != nil {
		return nil, err
	}
	if feedback == nil {
		return nil, nil
	}
	return &types.UserFeedbackResponse{
		MessageID:     feedback.MessageID,
		IsPositive:    &feedback.IsPositive,
		DislikeReason: feedback.DislikeReason,
		CreatedAt:     feedback.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}
