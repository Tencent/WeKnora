package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChunkFeedbackService implements the ChunkFeedbackServiceInterface
type ChunkFeedbackService struct {
	db *gorm.DB
}

// NewChunkFeedbackService creates a new ChunkFeedbackService instance
func NewChunkFeedbackService(db *gorm.DB) interfaces.ChunkFeedbackServiceInterface {
	return &ChunkFeedbackService{db: db}
}

// CreateMessageChunkRelations creates the relationship between a message and its referenced chunks
func (s *ChunkFeedbackService) CreateMessageChunkRelations(
	ctx context.Context,
	messageID, sessionID string,
	chunks []types.SearchResult,
	tenantID uint64,
) error {
	if len(chunks) == 0 {
		return nil
	}

	var relations []types.MessageChunkRelation
	for _, chunk := range chunks {
		rel := types.MessageChunkRelation{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			MessageID:       messageID,
			SessionID:       sessionID,
			ChunkID:         chunk.ChunkID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Score:           &chunk.Score,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}
		relations = append(relations, rel)
	}

	if err := s.db.WithContext(ctx).CreateInBatches(relations, 100).Error; err != nil {
		logger.Errorf(ctx, "[ChunkFeedback] Failed to create message-chunk relations: %v", err)
		return fmt.Errorf("failed to create message-chunk relations: %w", err)
	}

	logger.Infof(ctx, "[ChunkFeedback] Created %d message-chunk relations for message %s", len(relations), messageID)
	return nil
}

// SubmitFeedback submits a user's feedback on a chat message
func (s *ChunkFeedbackService) SubmitFeedback(
	ctx context.Context,
	userID, sessionID, messageID string,
	feedbackType types.FeedbackType,
	dislikeReason *types.DislikeReason,
	dislikeReasonDetail *string,
	tenantID uint64,
) error {
	logger.Infof(ctx, "[ChunkFeedback] Submitting feedback: user=%s, message=%s, type=%s", userID, messageID, feedbackType)

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 查询已存在的反馈记录
		var existingFeedback types.ChunkFeedback
		err := tx.Where("user_id = ? AND message_id = ?", userID, messageID).
			First(&existingFeedback).Error

		var feedbackID string
		isNewRecord := false

		if err == gorm.ErrRecordNotFound {
			// 新建反馈记录
			isNewRecord = true
			feedbackID = uuid.New().String()
			existingFeedback = types.ChunkFeedback{
				ID:           feedbackID,
				TenantID:     tenantID,
				UserID:       userID,
				SessionID:    sessionID,
				MessageID:    messageID,
				FeedbackType: feedbackType,
			}
		} else if err != nil {
			return fmt.Errorf("failed to query existing feedback: %w", err)
		}

		// 处理不同的反馈类型
		switch feedbackType {
		case types.FeedbackTypeLike:
			return s.handleLikeFeedback(ctx, tx, &existingFeedback, isNewRecord, userID, messageID, tenantID)

		case types.FeedbackTypeDislike:
			return s.handleDislikeFeedback(ctx, tx, &existingFeedback, isNewRecord, userID, messageID, tenantID, dislikeReason, dislikeReasonDetail)

		case types.FeedbackTypeUnlike:
			return s.handleUnlikeFeedback(ctx, tx, &existingFeedback, isNewRecord, userID, messageID, tenantID)

		case types.FeedbackTypeUndislike:
			return s.handleUndislikeFeedback(ctx, tx, &existingFeedback, isNewRecord, userID, messageID, tenantID)

		default:
			return fmt.Errorf("unknown feedback type: %s", feedbackType)
		}
	})
}

// handleLikeFeedback handles like feedback
func (s *ChunkFeedbackService) handleLikeFeedback(
	ctx context.Context,
	tx *gorm.DB,
	feedback *types.ChunkFeedback,
	isNewRecord bool,
	userID, messageID string,
	tenantID uint64,
) error {
	wasDislike := !isNewRecord && feedback.FeedbackType == types.FeedbackTypeDislike

	if isNewRecord {
		feedback.FeedbackType = types.FeedbackTypeLike
		if err := tx.Create(feedback).Error; err != nil {
			return fmt.Errorf("failed to create feedback: %w", err)
		}
	} else {
		if feedback.FeedbackType == types.FeedbackTypeLike {
			// 已经是点赞，不需要更新
			return nil
		}
		feedback.FeedbackType = types.FeedbackTypeLike
		if err := tx.Save(feedback).Error; err != nil {
			return fmt.Errorf("failed to update feedback: %w", err)
		}
	}

	// 更新片段统计
	if err := s.updateChunkStatsForLike(ctx, tx, messageID, tenantID, wasDislike); err != nil {
		return err
	}

	logger.Infof(ctx, "[ChunkFeedback] Like feedback submitted successfully")
	return nil
}

// handleDislikeFeedback handles dislike feedback
func (s *ChunkFeedbackService) handleDislikeFeedback(
	ctx context.Context,
	tx *gorm.DB,
	feedback *types.ChunkFeedback,
	isNewRecord bool,
	userID, messageID string,
	tenantID uint64,
	dislikeReason *types.DislikeReason,
	dislikeReasonDetail *string,
) error {
	wasLike := !isNewRecord && feedback.FeedbackType == types.FeedbackTypeLike

	if isNewRecord {
		feedback.FeedbackType = types.FeedbackTypeDislike
		feedback.DislikeReason = dislikeReason
		feedback.DislikeReasonDetail = dislikeReasonDetail
		if err := tx.Create(feedback).Error; err != nil {
			return fmt.Errorf("failed to create feedback: %w", err)
		}
	} else {
		if feedback.FeedbackType == types.FeedbackTypeDislike {
			// 已点踩，更新原因
			feedback.DislikeReason = dislikeReason
			feedback.DislikeReasonDetail = dislikeReasonDetail
			if err := tx.Save(feedback).Error; err != nil {
				return fmt.Errorf("failed to update feedback: %w", err)
			}
			return nil
		}
		feedback.FeedbackType = types.FeedbackTypeDislike
		feedback.DislikeReason = dislikeReason
		feedback.DislikeReasonDetail = dislikeReasonDetail
		if err := tx.Save(feedback).Error; err != nil {
			return fmt.Errorf("failed to update feedback: %w", err)
		}
	}

	// 更新片段统计
	if err := s.updateChunkStatsForDislike(ctx, tx, messageID, tenantID, wasLike); err != nil {
		return err
	}

	logger.Infof(ctx, "[ChunkFeedback] Dislike feedback submitted successfully")
	return nil
}

// handleUnlikeFeedback handles unlike (cancel like) feedback
func (s *ChunkFeedbackService) handleUnlikeFeedback(
	ctx context.Context,
	tx *gorm.DB,
	feedback *types.ChunkFeedback,
	isNewRecord bool,
	userID, messageID string,
	tenantID uint64,
) error {
	if isNewRecord || feedback.FeedbackType != types.FeedbackTypeLike {
		return fmt.Errorf("no like feedback to cancel")
	}

	// 删除反馈记录
	if err := tx.Delete(feedback).Error; err != nil {
		return fmt.Errorf("failed to delete feedback: %w", err)
	}

	// 更新片段统计
	if err := s.updateChunkStatsForUnlike(ctx, tx, messageID, tenantID); err != nil {
		return err
	}

	logger.Infof(ctx, "[ChunkFeedback] Unlike feedback submitted successfully")
	return nil
}

// handleUndislikeFeedback handles undislike (cancel dislike) feedback
func (s *ChunkFeedbackService) handleUndislikeFeedback(
	ctx context.Context,
	tx *gorm.DB,
	feedback *types.ChunkFeedback,
	isNewRecord bool,
	userID, messageID string,
	tenantID uint64,
) error {
	if isNewRecord || feedback.FeedbackType != types.FeedbackTypeDislike {
		return fmt.Errorf("no dislike feedback to cancel")
	}

	// 删除反馈记录
	if err := tx.Delete(feedback).Error; err != nil {
		return fmt.Errorf("failed to delete feedback: %w", err)
	}

	// 更新片段统计
	if err := s.updateChunkStatsForUndislike(ctx, tx, messageID, tenantID); err != nil {
		return err
	}

	logger.Infof(ctx, "[ChunkFeedback] Undislike feedback submitted successfully")
	return nil
}

// updateChunkStatsForLike updates chunk statistics when a like is submitted
func (s *ChunkFeedbackService) updateChunkStatsForLike(ctx context.Context, tx *gorm.DB, messageID string, tenantID uint64, wasDislike bool) error {
	chunks, err := s.getChunksByMessageID(ctx, tx, messageID, tenantID)
	if err != nil {
		return err
	}

	likeRateHighThreshold, _ := s.getConfigFloat(ctx, tx, types.ConfigKeyLikeRateHighThreshold, tenantID, types.DefaultLikeRateHighThreshold)
	minFeedbackCount, _ := s.getConfigInt(ctx, tx, types.ConfigKeyMinFeedbackCount, tenantID, types.DefaultMinFeedbackCount)

	for _, chunk := range chunks {
		chunk.LikeCount++
		chunk.CalculateLikeRate()

		// 调整权重
		if chunk.LikeCount+chunk.DislikeCount >= minFeedbackCount {
			if chunk.LikeRate >= likeRateHighThreshold {
				chunk.RecallWeight = math.Min(chunk.RecallWeight*types.DefaultWeightBoostFactor, types.MaxRecallWeight)
			}
		}

		if err := tx.Save(chunk).Error; err != nil {
			logger.Errorf(ctx, "[ChunkFeedback] Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		// 记录权重变更日志
		s.recordWeightLog(ctx, tx, chunk, types.TriggerTypeUserFeedback, "用户点赞", nil, tenantID)
	}

	return nil
}

// updateChunkStatsForDislike updates chunk statistics when a dislike is submitted
func (s *ChunkFeedbackService) updateChunkStatsForDislike(ctx context.Context, tx *gorm.DB, messageID string, tenantID uint64, wasLike bool) error {
	chunks, err := s.getChunksByMessageID(ctx, tx, messageID, tenantID)
	if err != nil {
		return err
	}

	likeRateLowThreshold, _ := s.getConfigFloat(ctx, tx, types.ConfigKeyLikeRateLowThreshold, tenantID, types.DefaultLikeRateLowThreshold)
	likeRateOptimizeThreshold, _ := s.getConfigFloat(ctx, tx, types.ConfigKeyLikeRateOptimizeThreshold, tenantID, types.DefaultLikeRateOptimizeThreshold)
	minFeedbackCount, _ := s.getConfigInt(ctx, tx, types.ConfigKeyMinFeedbackCount, tenantID, types.DefaultMinFeedbackCount)

	for _, chunk := range chunks {
		chunk.DislikeCount++
		chunk.CalculateLikeRate()

		// 调整权重
		if chunk.LikeCount+chunk.DislikeCount >= minFeedbackCount {
			if chunk.LikeRate < likeRateLowThreshold {
				chunk.RecallWeight = math.Max(chunk.RecallWeight*types.DefaultWeightPenaltyFactor, types.MinRecallWeight)
			}
		}

		// 检查是否标记待优化
		if chunk.LikeRate < likeRateOptimizeThreshold && chunk.LikeCount+chunk.DislikeCount >= minFeedbackCount {
			chunk.IsPendingOptimization = true
		}

		if err := tx.Save(chunk).Error; err != nil {
			logger.Errorf(ctx, "[ChunkFeedback] Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		// 记录权重变更日志
		s.recordWeightLog(ctx, tx, chunk, types.TriggerTypeUserFeedback, "用户点踩", nil, tenantID)
	}

	return nil
}

// updateChunkStatsForUnlike updates chunk statistics when a like is cancelled
func (s *ChunkFeedbackService) updateChunkStatsForUnlike(ctx context.Context, tx *gorm.DB, messageID string, tenantID uint64) error {
	chunks, err := s.getChunksByMessageID(ctx, tx, messageID, tenantID)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		if chunk.LikeCount > 0 {
			chunk.LikeCount--
		}
		chunk.CalculateLikeRate()
		chunk.RecallWeight = types.DefaultRecallWeight // 重置权重
		chunk.IsPendingOptimization = false

		if err := tx.Save(chunk).Error; err != nil {
			logger.Errorf(ctx, "[ChunkFeedback] Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		s.recordWeightLog(ctx, tx, chunk, types.TriggerTypeUserFeedback, "用户取消点赞", nil, tenantID)
	}

	return nil
}

// updateChunkStatsForUndislike updates chunk statistics when a dislike is cancelled
func (s *ChunkFeedbackService) updateChunkStatsForUndislike(ctx context.Context, tx *gorm.DB, messageID string, tenantID uint64) error {
	chunks, err := s.getChunksByMessageID(ctx, tx, messageID, tenantID)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		if chunk.DislikeCount > 0 {
			chunk.DislikeCount--
		}
		chunk.CalculateLikeRate()
		chunk.RecallWeight = types.DefaultRecallWeight // 重置权重
		chunk.IsPendingOptimization = false

		if err := tx.Save(chunk).Error; err != nil {
			logger.Errorf(ctx, "[ChunkFeedback] Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		s.recordWeightLog(ctx, tx, chunk, types.TriggerTypeUserFeedback, "用户取消点踩", nil, tenantID)
	}

	return nil
}

// getChunksByMessageID retrieves chunks associated with a message
func (s *ChunkFeedbackService) getChunksByMessageID(ctx context.Context, tx *gorm.DB, messageID string, tenantID uint64) ([]*types.Chunk, error) {
	var relations []types.MessageChunkRelation
	if err := tx.Where("message_id = ? AND tenant_id = ?", messageID, tenantID).Find(&relations).Error; err != nil {
		return nil, fmt.Errorf("failed to query relations: %w", err)
	}

	if len(relations) == 0 {
		return []*types.Chunk{}, nil
	}

	chunkIDs := make([]string, len(relations))
	for i, rel := range relations {
		chunkIDs[i] = rel.ChunkID
	}

	var chunks []*types.Chunk
	if err := tx.Where("id IN ?", chunkIDs).Find(&chunks).Error; err != nil {
		return nil, fmt.Errorf("failed to query chunks: %w", err)
	}

	return chunks, nil
}

// recordWeightLog records a weight change in the log
func (s *ChunkFeedbackService) recordWeightLog(ctx context.Context, tx *gorm.DB, chunk *types.Chunk, triggerType types.TriggerType, reason string, feedbackID *string, tenantID uint64) {
	// 注意：这里需要外部传入 db 而不是 tx，因为需要在事务外记录日志
}

// GetChunkStats retrieves statistics for a specific chunk
func (s *ChunkFeedbackService) GetChunkStats(ctx context.Context, chunkID string, tenantID uint64) (*types.ChunkStatsDetail, error) {
	var chunk types.Chunk
	if err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", chunkID, tenantID).First(&chunk).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("chunk not found")
		}
		return nil, fmt.Errorf("failed to query chunk: %w", err)
	}

	// 查询关联会话数
	var sessionCount int64
	s.db.WithContext(ctx).Model(&types.MessageChunkRelation{}).
		Where("chunk_id = ? AND tenant_id = ?", chunkID, tenantID).
		Distinct("session_id").
		Count(&sessionCount)

	// 查询点踩原因聚合
	var reasonStats []struct {
		Reason string
		Count  int
	}
	s.db.WithContext(ctx).Model(&types.ChunkFeedback{}).
		Select("dislike_reason as reason, count(*) as count").
		Where("session_id IN (SELECT session_id FROM message_chunk_relations WHERE chunk_id = ? AND tenant_id = ?)", chunkID, tenantID).
		Where("feedback_type = ? AND deleted_at IS NULL AND dislike_reason IS NOT NULL", types.FeedbackTypeDislike).
		Group("dislike_reason").
		Scan(&reasonStats)

	dislikeReasonStats := make(map[string]int)
	for _, stat := range reasonStats {
		dislikeReasonStats[stat.Reason] = stat.Count
	}

	return &types.ChunkStatsDetail{
		ChunkStats: types.ChunkStats{
			LikeCount:            chunk.LikeCount,
			DislikeCount:         chunk.DislikeCount,
			LikeRate:             chunk.LikeRate,
			RecallWeight:         chunk.RecallWeight,
			IsPendingOptimization: chunk.IsPendingOptimization,
			RelatedSessionCount:   int(sessionCount),
		},
		DislikeReasonStats: dislikeReasonStats,
	}, nil
}

// ListChunksByStats lists chunks filtered by statistics
func (s *ChunkFeedbackService) ListChunksByStats(ctx context.Context, kbID string, params *interfaces.ListChunksByStatsParams, tenantID uint64) ([]*types.Chunk, int64, error) {
	query := s.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("knowledge_base_id = ? AND tenant_id = ?", kbID, tenantID)

	// 应用筛选条件
	if params.MinLikeRate != nil {
		query = query.Where("like_rate >= ?", *params.MinLikeRate)
	}
	if params.MaxLikeRate != nil {
		query = query.Where("like_rate <= ?", *params.MaxLikeRate)
	}
	if params.PendingOptimization != nil {
		query = query.Where("is_pending_optimization = ?", *params.PendingOptimization)
	}
	if params.Keyword != "" {
		query = query.Where("content LIKE ?", "%"+params.Keyword+"%")
	}

	// 获取总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count chunks: %w", err)
	}

	// 应用排序
	sortField := "created_at"
	sortOrder := "desc"
	switch params.SortBy {
	case "like_count":
		sortField = "like_count"
	case "dislike_count":
		sortField = "dislike_count"
	case "like_rate":
		sortField = "like_rate"
	case "recall_weight":
		sortField = "recall_weight"
	}
	if params.SortOrder == "asc" {
		sortOrder = "asc"
	}
	query = query.Order(fmt.Sprintf("%s %s", sortField, sortOrder))

	// 应用分页
	page := params.Page
	if page < 1 {
		page = 1
	}
	pageSize := params.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	query = query.Offset(offset).Limit(pageSize)

	var chunks []*types.Chunk
	if err := query.Find(&chunks).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to query chunks: %w", err)
	}

	return chunks, total, nil
}

// GetChunkWeightLogs retrieves weight change logs for a chunk
func (s *ChunkFeedbackService) GetChunkWeightLogs(ctx context.Context, chunkID string, limit, offset int, tenantID uint64) ([]*types.ChunkWeightLog, int64, error) {
	query := s.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).
		Where("chunk_id = ? AND tenant_id = ?", chunkID, tenantID)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count logs: %w", err)
	}

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var logs []*types.ChunkWeightLog
	if err := query.Order("created_at desc").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to query logs: %w", err)
	}

	return logs, total, nil
}

// ResetChunkFeedback resets feedback data and weight for a chunk
func (s *ChunkFeedbackService) ResetChunkFeedback(ctx context.Context, chunkID string, operatorID string, tenantID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunk types.Chunk
		if err := tx.Where("id = ? AND tenant_id = ?", chunkID, tenantID).First(&chunk).Error; err != nil {
			return fmt.Errorf("chunk not found: %w", err)
		}

		// 记录重置前的值
		oldWeight := chunk.RecallWeight
		oldLikeRate := chunk.LikeRate

		// 重置统计数据
		chunk.LikeCount = 0
		chunk.DislikeCount = 0
		chunk.LikeRate = 0
		chunk.RecallWeight = types.DefaultRecallWeight
		chunk.IsPendingOptimization = false

		if err := tx.Save(&chunk).Error; err != nil {
			return fmt.Errorf("failed to reset chunk stats: %w", err)
		}

		// 记录权重变更日志
		weightLog := types.ChunkWeightLog{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			ChunkID:         chunkID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			TriggerType:     types.TriggerTypeManualReset,
			TriggerReason:   strPtr("管理员手动重置"),
			OldWeight:       oldWeight,
			NewWeight:       chunk.RecallWeight,
			OldLikeRate:     &oldLikeRate,
			NewLikeRate:     &chunk.LikeRate,
			OperatorID:      &operatorID,
			CreatedAt:       time.Now(),
		}
		if err := tx.Create(&weightLog).Error; err != nil {
			logger.Errorf(ctx, "[ChunkFeedback] Failed to record weight log: %v", err)
		}

		logger.Infof(ctx, "[ChunkFeedback] Reset feedback for chunk %s by operator %s", chunkID, operatorID)
		return nil
	})
}

// AdjustChunkWeight adjusts the weight of a chunk based on feedback
func (s *ChunkFeedbackService) AdjustChunkWeight(ctx context.Context, chunkID string, triggerType types.TriggerType, triggerReason string, feedbackID *string, tenantID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunk types.Chunk
		if err := tx.Where("id = ? AND tenant_id = ?", chunkID, tenantID).First(&chunk).Error; err != nil {
			return fmt.Errorf("chunk not found: %w", err)
		}

		oldWeight := chunk.RecallWeight
		oldLikeRate := chunk.LikeRate

		// 根据触发类型调整权重
		likeRateHighThreshold, _ := s.getConfigFloat(ctx, tx, types.ConfigKeyLikeRateHighThreshold, tenantID, types.DefaultLikeRateHighThreshold)
		likeRateLowThreshold, _ := s.getConfigFloat(ctx, tx, types.ConfigKeyLikeRateLowThreshold, tenantID, types.DefaultLikeRateLowThreshold)
		likeRateOptimizeThreshold, _ := s.getConfigFloat(ctx, tx, types.ConfigKeyLikeRateOptimizeThreshold, tenantID, types.DefaultLikeRateOptimizeThreshold)
		minFeedbackCount, _ := s.getConfigInt(ctx, tx, types.ConfigKeyMinFeedbackCount, tenantID, types.DefaultMinFeedbackCount)

		total := chunk.LikeCount + chunk.DislikeCount
		if total < minFeedbackCount {
			return nil // 评价数不足，不调整
		}

		// 根据好评率调整权重
		if chunk.LikeRate >= likeRateHighThreshold {
			chunk.RecallWeight = math.Min(chunk.RecallWeight*types.DefaultWeightBoostFactor, types.MaxRecallWeight)
		} else if chunk.LikeRate < likeRateLowThreshold {
			chunk.RecallWeight = math.Max(chunk.RecallWeight*types.DefaultWeightPenaltyFactor, types.MinRecallWeight)
		}

		// 检查待优化标记
		if chunk.LikeRate < likeRateOptimizeThreshold {
			chunk.IsPendingOptimization = true
		} else {
			chunk.IsPendingOptimization = false
		}

		if chunk.RecallWeight != oldWeight {
			if err := tx.Save(&chunk).Error; err != nil {
				return fmt.Errorf("failed to update chunk weight: %w", err)
			}

			// 记录权重变更日志
			weightLog := types.ChunkWeightLog{
				ID:              uuid.New().String(),
				TenantID:        tenantID,
				ChunkID:         chunkID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				TriggerType:     triggerType,
				TriggerReason:   &triggerReason,
				OldWeight:       oldWeight,
				NewWeight:       chunk.RecallWeight,
				OldLikeRate:     &oldLikeRate,
				NewLikeRate:     &chunk.LikeRate,
				FeedbackID:      feedbackID,
				CreatedAt:       time.Now(),
			}
			if err := tx.Create(&weightLog).Error; err != nil {
				logger.Errorf(ctx, "[ChunkFeedback] Failed to record weight log: %v", err)
			}
		}

		return nil
	})
}

// GetChunkFeedbackConfig retrieves a configuration value
func (s *ChunkFeedbackService) GetChunkFeedbackConfig(ctx context.Context, key types.ChunkFeedbackConfigKey, tenantID uint64) (string, error) {
	var config types.ChunkFeedbackConfig
	err := s.db.WithContext(ctx).Where("config_key = ? AND (tenant_id = ? OR tenant_id = 0)", string(key), tenantID).
		Order("tenant_id DESC").First(&config).Error
	if err == gorm.ErrRecordNotFound {
		return "", fmt.Errorf("config not found")
	}
	if err != nil {
		return "", fmt.Errorf("failed to query config: %w", err)
	}
	return config.ConfigValue, nil
}

// UpdateChunkFeedbackConfig updates a configuration value
func (s *ChunkFeedbackService) UpdateChunkFeedbackConfig(ctx context.Context, key types.ChunkFeedbackConfigKey, value string, tenantID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config types.ChunkFeedbackConfig
		err := tx.Where("config_key = ? AND tenant_id = ?", string(key), tenantID).First(&config).Error

		if err == gorm.ErrRecordNotFound {
			config = types.ChunkFeedbackConfig{
				ID:          uuid.New().String(),
				TenantID:    tenantID,
				ConfigKey:   string(key),
				ConfigValue: value,
				UpdatedAt:   time.Now(),
			}
			return tx.Create(&config).Error
		} else if err != nil {
			return fmt.Errorf("failed to query config: %w", err)
		}

		config.ConfigValue = value
		return tx.Save(&config).Error
	})
}

// GetFeedbackSummary retrieves aggregated feedback statistics for a knowledge base
func (s *ChunkFeedbackService) GetFeedbackSummary(ctx context.Context, kbID string, tenantID uint64) (*types.ChunkFeedbackSummary, error) {
	var chunk types.Chunk
	var summary types.ChunkFeedbackSummary

	// 获取片段总数
	if err := s.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("knowledge_base_id = ? AND tenant_id = ?", kbID, tenantID).
		Count((*int64)(&summary.TotalChunks)).Error; err != nil {
		return nil, fmt.Errorf("failed to count chunks: %w", err)
	}

	// 获取待优化片段数
	if err := s.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("knowledge_base_id = ? AND tenant_id = ? AND is_pending_optimization = ?", kbID, tenantID, true).
		Count((*int64)(&summary.PendingOptimizationCount)).Error; err != nil {
		return nil, fmt.Errorf("failed to count pending optimization chunks: %w", err)
	}

	// 获取评价汇总
	var stats struct {
		TotalFeedbacks int
		TotalLikes    int
		TotalDislikes int
		AvgLikeRate   float64
	}

	rows, err := s.db.WithContext(ctx).Raw(`
		SELECT 
			COALESCE(SUM(c.like_count + c.dislike_count), 0) as total_feedbacks,
			COALESCE(SUM(c.like_count), 0) as total_likes,
			COALESCE(SUM(c.dislike_count), 0) as total_dislikes,
			COALESCE(AVG(c.like_rate), 0) as avg_like_rate
		FROM chunks c
		WHERE c.knowledge_base_id = ? AND c.tenant_id = ?
	`, kbID, tenantID).Rows()
	if err != nil {
		return nil, fmt.Errorf("failed to query feedback summary: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		rows.Scan(&stats.TotalFeedbacks, &stats.TotalLikes, &stats.TotalDislikes, &stats.AvgLikeRate)
	}

	summary.TotalFeedbacks = stats.TotalFeedbacks
	summary.TotalLikes = stats.TotalLikes
	summary.TotalDislikes = stats.TotalDislikes
	summary.AverageLikeRate = stats.AvgLikeRate

	return &summary, nil
}

// BatchAdjustWeights adjusts weights for all chunks in a knowledge base
func (s *ChunkFeedbackService) BatchAdjustWeights(ctx context.Context, kbID string, tenantID uint64) error {
	chunks, _, err := s.ListChunksByStats(ctx, kbID, &interfaces.ListChunksByStatsParams{Page: 1, PageSize: 1000}, tenantID)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		triggerReason := "批量权重调整"
		if err := s.AdjustChunkWeight(ctx, chunk.ID, types.TriggerTypeBatchUpdate, triggerReason, nil, tenantID); err != nil {
			logger.Errorf(ctx, "[ChunkFeedback] Failed to adjust weight for chunk %s: %v", chunk.ID, err)
			continue
		}
	}

	logger.Infof(ctx, "[ChunkFeedback] Batch adjusted weights for %d chunks in KB %s", len(chunks), kbID)
	return nil
}

// Helper functions

func strPtr(s string) *string {
	return &s
}

func (s *ChunkFeedbackService) getConfigFloat(ctx context.Context, tx *gorm.DB, key types.ChunkFeedbackConfigKey, tenantID uint64, defaultVal float64) (float64, error) {
	var config types.ChunkFeedbackConfig
	err := tx.WithContext(ctx).Where("config_key = ? AND (tenant_id = ? OR tenant_id = 0)", string(key), tenantID).
		Order("tenant_id DESC").First(&config).Error
	if err == gorm.ErrRecordNotFound {
		return defaultVal, nil
	}
	if err != nil {
		return defaultVal, err
	}
	val, err := strconv.ParseFloat(config.ConfigValue, 64)
	if err != nil {
		return defaultVal, nil
	}
	return val, nil
}

func (s *ChunkFeedbackService) getConfigInt(ctx context.Context, tx *gorm.DB, key types.ChunkFeedbackConfigKey, tenantID uint64, defaultVal int) (int, error) {
	val, err := s.getConfigFloat(ctx, tx, key, tenantID, float64(defaultVal))
	return int(val), err
}
