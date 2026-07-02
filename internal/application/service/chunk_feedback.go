package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkFeedbackService 片段反馈服务
type ChunkFeedbackService struct {
	qaRefRepo     QAReplyChunkRefRepository
	feedbackRepo  ChunkFeedbackRepository
	chunkRepo     ChunkRepository
	weightLogRepo ChunkWeightLogRepository
	config        *types.ChunkFeedbackConfig
}

// NewChunkFeedbackService 创建反馈服务实例
func NewChunkFeedbackService(
	qaRefRepo QAReplyChunkRefRepository,
	feedbackRepo ChunkFeedbackRepository,
	chunkRepo ChunkRepository,
	weightLogRepo ChunkWeightLogRepository,
) *ChunkFeedbackService {
	return &ChunkFeedbackService{
		qaRefRepo:     qaRefRepo,
		feedbackRepo:  feedbackRepo,
		chunkRepo:     chunkRepo,
		weightLogRepo: weightLogRepo,
		config:        types.DefaultChunkFeedbackConfig(),
	}
}

// SubmitFeedback 处理用户提交反馈
func (s *ChunkFeedbackService) SubmitFeedback(ctx context.Context, tenantID uint64, userID, sessionID string, req *types.SubmitFeedbackRequest) error {
	logger.Infof(ctx, "Processing feedback submission: messageID=%s, isPositive=%v, tenantID=%d",
		req.MessageID, req.IsPositive, tenantID)

	refs, err := s.qaRefRepo.GetByMessageID(ctx, req.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get chunk refs: %w", err)
	}
	if len(refs) == 0 {
		logger.Warnf(ctx, "No chunk refs found for message %s", req.MessageID)
		return s.submitMessageLevelFeedback(ctx, tenantID, userID, sessionID, req)
	}

	feedback, err := s.feedbackRepo.Upsert(ctx, req.MessageID, sessionID, userID, tenantID, req.IsPositive, req.DislikeReason)
	if err != nil {
		return fmt.Errorf("failed to upsert feedback: %w", err)
	}

	if !feedback.IsChanged {
		logger.Infof(ctx, "Feedback unchanged for message %s, skipping chunk updates", req.MessageID)
		return nil
	}

	chunkIDs := make([]string, len(refs))
	for i, ref := range refs {
		chunkIDs[i] = ref.ChunkID
	}

	if err := s.updateChunksFeedbackStats(ctx, tenantID, chunkIDs, feedback.IsPositive, req.DislikeReason); err != nil {
		logger.Errorf(ctx, "Failed to update chunks feedback stats: %v", err)
		return err
	}

	logger.Infof(ctx, "Feedback processed successfully for message %s, %d chunks affected", req.MessageID, len(refs))
	return nil
}

func (s *ChunkFeedbackService) submitMessageLevelFeedback(ctx context.Context, tenantID uint64, userID, sessionID string, req *types.SubmitFeedbackRequest) error {
	_, err := s.feedbackRepo.Upsert(ctx, req.MessageID, sessionID, userID, tenantID, req.IsPositive, req.DislikeReason)
	return err
}

func (s *ChunkFeedbackService) updateChunksFeedbackStats(ctx context.Context, tenantID uint64, chunkIDs []string, isPositive bool, dislikeReason string) error {
	for _, chunkID := range chunkIDs {
		if err := s.updateSingleChunkFeedbackStats(ctx, tenantID, chunkID, isPositive, dislikeReason); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s feedback stats: %v", chunkID, err)
		}
	}
	return nil
}

func (s *ChunkFeedbackService) updateSingleChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string, isPositive bool, dislikeReason string) error {
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return fmt.Errorf("failed to get chunk: %w", err)
	}

	oldWeight := chunk.RecallWeight
	oldLikeCount := chunk.LikeCount
	oldDislikeCount := chunk.DislikeCount

	if isPositive {
		if oldDislikeCount > 0 && chunk.DislikeCount > 0 {
			chunk.DislikeCount--
		}
		chunk.LikeCount++
	} else {
		if oldLikeCount > 0 && chunk.LikeCount > 0 {
			chunk.LikeCount--
		}
		chunk.DislikeCount++
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

	total := chunk.LikeCount + chunk.DislikeCount
	if total > 0 {
		chunk.PositiveRate = math.Round(float64(chunk.LikeCount)*100/float64(total)) / 100
	} else {
		chunk.PositiveRate = 0
	}

	chunk.RecallWeight = s.calculateWeight(chunk.PositiveRate)

	if chunk.PositiveRate <= s.config.AutoMarkThreshold && total >= 5 {
		chunk.QualityStatus = types.ChunkQualityStatusPendingOpt
	}

	if err := s.chunkRepo.UpdateChunkFeedbackStats(ctx, chunkID, chunk.LikeCount, chunk.DislikeCount,
		chunk.PositiveRate, chunk.RecallWeight, chunk.QualityStatus); err != nil {
		return fmt.Errorf("failed to update chunk stats: %w", err)
	}

	s.chunkRepo.UpdateChunkLastFeedbackAt(ctx, chunkID)

	if oldWeight != chunk.RecallWeight {
		triggerType := types.FeedbackTriggerUserLike
		if !isPositive {
			triggerType = types.FeedbackTriggerUserDislike
		}
		s.recordWeightChange(ctx, chunkID, tenantID, "adjust_weight", oldWeight, chunk.RecallWeight, triggerType, "", "")
	}

	return nil
}

func (s *ChunkFeedbackService) calculateWeight(positiveRate float64) float64 {
	if positiveRate >= s.config.HighQualityThreshold {
		return s.config.WeightBoostFactor
	} else if positiveRate < s.config.LowQualityThreshold {
		return s.config.WeightPenaltyFactor
	}
	return 1.0
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
	stats, err := s.chunkRepo.GetChunkStats(ctx, chunkID)
	if err != nil {
		return nil, err
	}
	sessionCount, _ := s.qaRefRepo.CountByChunkID(ctx, chunkID)
	stats.RelatedSessionCount = int(sessionCount)
	reasonMap, _ := s.feedbackRepo.GetDislikeReasonsByChunkIDs(ctx, []string{chunkID})
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
			ChunkID:      chunk.ID,
			KnowledgeID:  chunk.KnowledgeID,
			Content:      truncateContent(chunk.Content, 100),
			LikeCount:    chunk.LikeCount,
			DislikeCount: chunk.DislikeCount,
			PositiveRate: chunk.PositiveRate,
			RecallWeight: chunk.RecallWeight,
			QualityStatus: string(chunk.QualityStatus),
			UpdatedAt:    chunk.UpdatedAt,
		}
	}
	return stats, nil
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
	if err := s.chunkRepo.ResetChunkFeedback(ctx, chunkID); err != nil {
		return fmt.Errorf("failed to reset chunk feedback: %w", err)
	}
	s.recordWeightChange(ctx, chunkID, tenantID, "reset", chunk.RecallWeight, 1.0, types.FeedbackTriggerAdminReset, "", operator)
	logger.Infof(ctx, "Chunk feedback reset successfully: chunkID=%s, operator=%s", chunkID, operator)
	return nil
}

// GetWeightLogs 获取权重变更日志
func (s *ChunkFeedbackService) GetWeightLogs(ctx context.Context, chunkID string, limit int) (*types.WeightLogResponse, error) {
	logs, err := s.weightLogRepo.GetByChunkID(ctx, chunkID, limit)
	if err != nil {
		return nil, err
	}
	total, _ := s.weightLogRepo.CountByChunkID(ctx, chunkID)
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
