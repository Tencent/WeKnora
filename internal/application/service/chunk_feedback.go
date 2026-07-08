package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/service/chunkfeedback"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ChunkFeedbackService 片段反馈服务
type ChunkFeedbackService struct {
	qaRefRepo     interfaces.QAReplyChunkRefRepository
	feedbackRepo  interfaces.ChunkFeedbackRepository
	messageRepo   interfaces.MessageRepository
	chunkRepo     interfaces.ChunkRepository
	weightLogRepo interfaces.ChunkWeightLogRepository
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

// SubmitFeedback 处理用户提交反馈
func (s *ChunkFeedbackService) SubmitFeedback(ctx context.Context, tenantID uint64, userID string, req *types.SubmitFeedbackRequest) error {
	logger.Infof(ctx, "Processing feedback submission: messageID=%s, isPositive=%v, tenantID=%d",
		req.MessageID, req.IsPositive, tenantID)

	message, err := s.messageRepo.GetMessageByID(ctx, tenantID, userID, req.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %w", err)
	}

	refs, err := s.qaRefRepo.GetByMessageID(ctx, tenantID, req.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get chunk refs: %w", err)
	}

	feedback, err := s.feedbackRepo.Upsert(ctx, req.MessageID, message.SessionID, userID, tenantID, req.IsPositive, req.DislikeReason)
	if err != nil {
		return fmt.Errorf("failed to upsert feedback: %w", err)
	}
	if err := s.updateMessageFeedbackStats(ctx, tenantID, userID, message, feedback); err != nil {
		return err
	}

	if len(refs) == 0 {
		logger.Warnf(ctx, "No chunk refs found for message %s", req.MessageID)
		return nil
	}

	if !feedback.IsChanged {
		logger.Infof(ctx, "Feedback unchanged for message %s, skipping chunk updates", req.MessageID)
		return nil
	}

	chunkIDs := make([]string, len(refs))
	for i, ref := range refs {
		chunkIDs[i] = ref.ChunkID
	}

	if err := s.updateChunksFeedbackStats(ctx, tenantID, chunkIDs, feedback, req.DislikeReason); err != nil {
		logger.Errorf(ctx, "Failed to update chunks feedback stats: %v", err)
		return err
	}

	logger.Infof(ctx, "Feedback processed successfully for message %s, %d chunks affected", req.MessageID, len(refs))
	return nil
}

// CancelFeedback 取消用户对某条回答的反馈，并回退关联片段上的累计计数。
func (s *ChunkFeedbackService) CancelFeedback(ctx context.Context, tenantID uint64, userID, messageID string) error {
	feedback, err := s.feedbackRepo.GetByMessageAndUser(ctx, tenantID, messageID, userID)
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

	refs, err := s.qaRefRepo.GetByMessageID(ctx, tenantID, messageID)
	if err != nil {
		return fmt.Errorf("failed to get chunk refs: %w", err)
	}

	if err := s.feedbackRepo.Delete(ctx, tenantID, feedback.ID); err != nil {
		return fmt.Errorf("failed to delete feedback: %w", err)
	}
	if err := s.cancelMessageFeedbackStats(ctx, tenantID, userID, message, feedback.IsPositive); err != nil {
		return err
	}

	for _, ref := range refs {
		if err := s.cancelSingleChunkFeedbackStats(ctx, tenantID, ref.ChunkID, feedback.IsPositive); err != nil {
			logger.Warnf(ctx, "Failed to cancel chunk %s feedback stats: %v", ref.ChunkID, err)
		}
	}
	return nil
}

func (s *ChunkFeedbackService) updateChunksFeedbackStats(ctx context.Context, tenantID uint64, chunkIDs []string, feedback *types.ChunkFeedback, dislikeReason string) error {
	for _, chunkID := range chunkIDs {
		if err := s.updateSingleChunkFeedbackStats(ctx, tenantID, chunkID, feedback, dislikeReason); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s feedback stats: %v", chunkID, err)
		}
	}
	return nil
}

func (s *ChunkFeedbackService) updateMessageFeedbackStats(ctx context.Context, tenantID uint64, userID string, message *types.Message, feedback *types.ChunkFeedback) error {
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
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}

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

	if err := s.chunkRepo.UpdateChunkFeedbackStats(ctx, tenantID, chunkID, chunk.LikeCount, chunk.DislikeCount,
		chunk.PositiveRate, chunk.RecallWeight, chunk.QualityStatus); err != nil {
		return fmt.Errorf("failed to update chunk stats: %w", err)
	}

	s.chunkRepo.UpdateChunkLastFeedbackAt(ctx, tenantID, chunkID)

	if oldWeight != chunk.RecallWeight {
		triggerType := types.FeedbackTriggerUserLike
		if !feedback.IsPositive {
			triggerType = types.FeedbackTriggerUserDislike
		}
		s.recordWeightChange(ctx, chunkID, tenantID, "adjust_weight", oldWeight, chunk.RecallWeight, triggerType, "", "")
	}

	return nil
}

func (s *ChunkFeedbackService) cancelSingleChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, wasPositive bool) error {
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}

	oldWeight := chunk.RecallWeight
	state := chunkfeedback.CancelVote(chunkFeedbackState(chunk), wasPositive, chunkFeedbackConfig(s.config))
	applyChunkFeedbackState(chunk, state)

	if err := s.chunkRepo.UpdateChunkFeedbackStats(ctx, tenantID, chunkID, chunk.LikeCount, chunk.DislikeCount,
		chunk.PositiveRate, chunk.RecallWeight, chunk.QualityStatus); err != nil {
		return fmt.Errorf("failed to update chunk stats: %w", err)
	}
	s.chunkRepo.UpdateChunkLastFeedbackAt(ctx, tenantID, chunkID)

	if oldWeight != chunk.RecallWeight {
		s.recordWeightChange(ctx, chunkID, tenantID, "adjust_weight", oldWeight, chunk.RecallWeight, types.FeedbackTriggerUserCancel, "", "")
	}
	return nil
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
	sessionCount, _ := s.qaRefRepo.CountByChunkID(ctx, tenantID, chunkID)
	stats.RelatedSessionCount = int(sessionCount)
	reasonMap, _ := s.feedbackRepo.GetDislikeReasonsByChunkIDs(ctx, tenantID, []string{chunkID})
	if reasons, ok := reasonMap[chunkID]; ok {
		stats.DislikeReasons = reasons
	}
	return stats, nil
}

// ListLowQualityChunks 列出低质量片段
func (s *ChunkFeedbackService) ListLowQualityChunks(ctx context.Context, tenantID uint64, maxRate float64, limit, offset int) ([]*types.ChunkQualityStats, error) {
	chunks, err := s.chunkRepo.ListLowQualityChunks(ctx, tenantID, maxRate, limit, offset)
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
func (s *ChunkFeedbackService) CountLowQualityChunks(ctx context.Context, tenantID uint64, maxRate float64) (int64, error) {
	return s.chunkRepo.CountLowQualityChunks(ctx, tenantID, maxRate)
}

// GetFeedbackOverview 获取片段反馈聚合概览
func (s *ChunkFeedbackService) GetFeedbackOverview(ctx context.Context, tenantID uint64) (*types.ChunkFeedbackOverviewResponse, error) {
	return s.chunkRepo.GetChunkFeedbackOverview(ctx, tenantID)
}

func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

// ResetChunkFeedback 重置片段反馈数据
func (s *ChunkFeedbackService) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkID, operator string) error {
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}
	if err := s.chunkRepo.ResetChunkFeedback(ctx, tenantID, chunkID); err != nil {
		return fmt.Errorf("failed to reset chunk feedback: %w", err)
	}
	s.recordWeightChange(ctx, chunkID, tenantID, "reset", chunk.RecallWeight, 1.0, types.FeedbackTriggerAdminReset, "", operator)
	logger.Infof(ctx, "Chunk feedback reset successfully: chunkID=%s, operator=%s", chunkID, operator)
	return nil
}

// GetWeightLogs 获取权重变更日志
func (s *ChunkFeedbackService) GetWeightLogs(ctx context.Context, tenantID uint64, chunkID string, limit int) (*types.WeightLogResponse, error) {
	logs, err := s.weightLogRepo.GetByChunkID(ctx, tenantID, chunkID, limit)
	if err != nil {
		return nil, err
	}
	total, _ := s.weightLogRepo.CountByChunkID(ctx, tenantID, chunkID)
	return &types.WeightLogResponse{Logs: logs, Total: total}, nil
}

// SaveQAReplyChunkRefs 保存问答回复与片段的关联关系
func (s *ChunkFeedbackService) SaveQAReplyChunkRefs(ctx context.Context, tenantID uint64, messageID string, chunkIDs []string) error {
	refs := make([]*types.QAReplyChunkRef, len(chunkIDs))
	for i, chunkID := range chunkIDs {
		refs[i] = &types.QAReplyChunkRef{
			MessageID: messageID,
			ChunkID:   chunkID,
			TenantID:  tenantID,
		}
	}
	return s.qaRefRepo.CreateBatch(ctx, refs)
}

// GetDislikeReasonOptions 获取点踩原因选项
func (s *ChunkFeedbackService) GetDislikeReasonOptions() []string {
	return types.GetDislikeReasons()
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
