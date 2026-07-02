package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type messageFeedbackRepository struct {
	db    *gorm.DB
	rules feedbackWeightRules
}

type feedbackWeightRules struct {
	MinFeedbackCount               int64
	BoostPositiveRateThreshold     float64
	NeutralPositiveRateThreshold   float64
	NeedsOptimizationRateThreshold float64
	BoostRecallWeight              float64
	NeutralRecallWeight            float64
	PenaltyRecallWeight            float64
}

func NewMessageFeedbackRepository(db *gorm.DB, cfg *config.Config) interfaces.MessageFeedbackRepository {
	return &messageFeedbackRepository{db: db, rules: feedbackWeightRulesFromConfig(cfg)}
}

func feedbackWeightRulesFromConfig(cfg *config.Config) feedbackWeightRules {
	rules := feedbackWeightRules{
		MinFeedbackCount:               3,
		BoostPositiveRateThreshold:     0.8,
		NeutralPositiveRateThreshold:   0.5,
		NeedsOptimizationRateThreshold: 0.3,
		BoostRecallWeight:              1.2,
		NeutralRecallWeight:            1.0,
		PenaltyRecallWeight:            0.8,
	}
	if cfg == nil || cfg.MessageFeedback == nil {
		return rules
	}
	mf := cfg.MessageFeedback
	if mf.MinFeedbackCount > 0 {
		rules.MinFeedbackCount = int64(mf.MinFeedbackCount)
	}
	if mf.BoostPositiveRateThreshold > 0 {
		rules.BoostPositiveRateThreshold = mf.BoostPositiveRateThreshold
	}
	if mf.NeutralPositiveRateThreshold > 0 {
		rules.NeutralPositiveRateThreshold = mf.NeutralPositiveRateThreshold
	}
	if mf.NeedsOptimizationRateThreshold > 0 {
		rules.NeedsOptimizationRateThreshold = mf.NeedsOptimizationRateThreshold
	}
	if mf.BoostRecallWeight > 0 {
		rules.BoostRecallWeight = mf.BoostRecallWeight
	}
	if mf.NeutralRecallWeight > 0 {
		rules.NeutralRecallWeight = mf.NeutralRecallWeight
	}
	if mf.PenaltyRecallWeight > 0 {
		rules.PenaltyRecallWeight = mf.PenaltyRecallWeight
	}
	return rules
}

func (r *messageFeedbackRepository) WithTransaction(
	ctx context.Context,
	fn func(repo interfaces.MessageFeedbackRepository) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&messageFeedbackRepository{db: tx, rules: r.rules})
	})
}

func (r *messageFeedbackRepository) SaveMessageChunkRefs(ctx context.Context, refs []*types.MessageChunkRef) error {
	if len(refs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, ref := range refs {
		if ref.ID == "" {
			ref.ID = uuid.New().String()
		}
		if ref.CreatedAt.IsZero() {
			ref.CreatedAt = now
		}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "session_tenant_id"},
			{Name: "message_id"},
			{Name: "chunk_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"chunk_tenant_id",
			"session_id",
			"knowledge_base_id",
			"knowledge_id",
			"chunk_index",
			"chunk_type",
			"match_type",
			"score",
		}),
	}).Create(&refs).Error
}

func (r *messageFeedbackRepository) GetMessageChunkRefs(
	ctx context.Context,
	sessionTenantID uint64,
	messageID string,
) ([]*types.MessageChunkRef, error) {
	var refs []*types.MessageChunkRef
	err := r.db.WithContext(ctx).
		Where("session_tenant_id = ? AND message_id = ?", sessionTenantID, messageID).
		Find(&refs).Error
	return refs, err
}

func (r *messageFeedbackRepository) GetFeedbacksByMessageIDs(
	ctx context.Context,
	sessionTenantID uint64,
	userID string,
	messageIDs []string,
) ([]*types.MessageFeedback, error) {
	if len(messageIDs) == 0 || userID == "" {
		return nil, nil
	}
	var feedbacks []*types.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("session_tenant_id = ? AND user_id = ? AND message_id IN ?", sessionTenantID, userID, messageIDs).
		Find(&feedbacks).Error
	return feedbacks, err
}

func (r *messageFeedbackRepository) UpsertMessageFeedback(ctx context.Context, feedback *types.MessageFeedback) error {
	now := time.Now().UTC()
	if feedback.ID == "" {
		feedback.ID = uuid.New().String()
	}
	if feedback.FeedbackAt.IsZero() {
		feedback.FeedbackAt = now
	}
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = now
	}
	feedback.UpdatedAt = now
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "session_tenant_id"},
			{Name: "user_id"},
			{Name: "message_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"session_id",
			"feedback_type",
			"reason_code",
			"reason_text",
			"feedback_at",
			"updated_at",
		}),
	}).Create(feedback).Error; err != nil {
		return err
	}
	var saved types.MessageFeedback
	if err := r.db.WithContext(ctx).
		Where("session_tenant_id = ? AND user_id = ? AND message_id = ?",
			feedback.SessionTenantID, feedback.UserID, feedback.MessageID).
		First(&saved).Error; err != nil {
		return err
	}
	*feedback = saved
	return nil
}

func (r *messageFeedbackRepository) RecalculateChunkFeedback(
	ctx context.Context,
	chunkTenantID uint64,
	chunkID string,
) (*types.ChunkFeedbackStats, error) {
	stats, err := r.computeChunkFeedbackStats(ctx, chunkTenantID, chunkID)
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{
		"like_count":         stats.LikeCount,
		"dislike_count":      stats.DislikeCount,
		"positive_rate":      stats.PositiveRate,
		"recall_weight":      stats.RecallWeight,
		"needs_optimization": stats.NeedsOptimization,
		"updated_at":         time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND id = ?", chunkTenantID, chunkID).
		Updates(updates).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

func (r *messageFeedbackRepository) CreateChunkWeightLog(ctx context.Context, log *types.ChunkWeightLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *messageFeedbackRepository) GetChunkFeedbackStats(
	ctx context.Context,
	chunkTenantID uint64,
	chunkID string,
) (*types.ChunkFeedbackStats, error) {
	return r.computeChunkFeedbackStats(ctx, chunkTenantID, chunkID)
}

func (r *messageFeedbackRepository) GetChunkWeightLogs(
	ctx context.Context,
	chunkTenantID uint64,
	chunkID string,
	limit int,
) ([]*types.ChunkWeightLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var logs []*types.ChunkWeightLog
	err := r.db.WithContext(ctx).
		Where("chunk_tenant_id = ? AND chunk_id = ?", chunkTenantID, chunkID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

func (r *messageFeedbackRepository) ResetChunkFeedback(
	ctx context.Context,
	chunkTenantID uint64,
	chunkID string,
	resetAt time.Time,
) error {
	return r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND id = ?", chunkTenantID, chunkID).
		Updates(map[string]interface{}{
			"like_count":         0,
			"dislike_count":      0,
			"positive_rate":      nil,
			"recall_weight":      1.0,
			"needs_optimization": false,
			"feedback_reset_at":  resetAt,
			"updated_at":         resetAt,
		}).Error
}

func (r *messageFeedbackRepository) computeChunkFeedbackStats(
	ctx context.Context,
	chunkTenantID uint64,
	chunkID string,
) (*types.ChunkFeedbackStats, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", chunkTenantID, chunkID).
		First(&chunk).Error; err != nil {
		return nil, err
	}

	base := r.db.WithContext(ctx).
		Table("message_feedbacks AS mf").
		Joins("INNER JOIN message_chunk_refs AS mcr ON mcr.session_tenant_id = mf.session_tenant_id AND mcr.message_id = mf.message_id").
		Where("mcr.chunk_tenant_id = ? AND mcr.chunk_id = ?", chunkTenantID, chunkID).
		Where("mf.feedback_type IN ?", []string{types.FeedbackTypeLike, types.FeedbackTypeDislike})
	if chunk.FeedbackResetAt != nil {
		base = base.Where("mf.feedback_at > ?", *chunk.FeedbackResetAt)
	}

	var likeCount int64
	if err := base.Session(&gorm.Session{}).Where("mf.feedback_type = ?", types.FeedbackTypeLike).Count(&likeCount).Error; err != nil {
		return nil, err
	}
	var dislikeCount int64
	if err := base.Session(&gorm.Session{}).Where("mf.feedback_type = ?", types.FeedbackTypeDislike).Count(&dislikeCount).Error; err != nil {
		return nil, err
	}

	reasonQuery := base.Session(&gorm.Session{}).
		Select("mf.reason_code AS reason_code, COUNT(*) AS count").
		Where("mf.feedback_type = ? AND mf.reason_code <> ?", types.FeedbackTypeDislike, "").
		Group("mf.reason_code").
		Order("count DESC")
	var reasonStats []types.FeedbackReasonStat
	if err := reasonQuery.Scan(&reasonStats).Error; err != nil {
		return nil, err
	}

	var associatedSessionCount int64
	if err := r.db.WithContext(ctx).Table("message_chunk_refs").
		Where("chunk_tenant_id = ? AND chunk_id = ?", chunkTenantID, chunkID).
		Distinct("session_id").
		Count(&associatedSessionCount).Error; err != nil {
		return nil, err
	}

	total := likeCount + dislikeCount
	var positiveRate *float64
	recallWeight := 1.0
	needsOptimization := false
	if total > 0 {
		rate := float64(likeCount) / float64(total)
		positiveRate = &rate
		if total >= r.rules.MinFeedbackCount {
			switch {
			case rate >= r.rules.BoostPositiveRateThreshold:
				recallWeight = r.rules.BoostRecallWeight
			case rate >= r.rules.NeutralPositiveRateThreshold:
				recallWeight = r.rules.NeutralRecallWeight
			default:
				recallWeight = r.rules.PenaltyRecallWeight
			}
			needsOptimization = rate < r.rules.NeedsOptimizationRateThreshold
		}
	}

	return &types.ChunkFeedbackStats{
		ChunkID:                chunkID,
		ChunkTenantID:          chunkTenantID,
		LikeCount:              likeCount,
		DislikeCount:           dislikeCount,
		PositiveRate:           positiveRate,
		RecallWeight:           recallWeight,
		NeedsOptimization:      needsOptimization,
		AssociatedSessionCount: associatedSessionCount,
		ReasonStats:            reasonStats,
		FeedbackResetAt:        chunk.FeedbackResetAt,
	}, nil
}
