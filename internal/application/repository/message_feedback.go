package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type messageFeedbackRepository struct {
	db *gorm.DB
}

func NewMessageFeedbackRepository(db *gorm.DB) interfaces.MessageFeedbackRepository {
	return &messageFeedbackRepository{db: db}
}

func (r *messageFeedbackRepository) UpsertFeedback(
	ctx context.Context,
	feedback *types.MessageFeedback,
) (*types.MessageFeedback, *types.MessageFeedback, error) {
	return r.upsertFeedback(ctx, r.db.WithContext(ctx), feedback)
}

func (r *messageFeedbackRepository) UpsertFeedbackAndRefreshChunks(
	ctx context.Context,
	feedback *types.MessageFeedback,
	chunkIDs []string,
	cfg types.ChunkFeedbackConfig,
) (*types.MessageFeedback, error) {
	var saved *types.MessageFeedback
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, previous, err := r.upsertFeedback(ctx, tx, feedback)
		if err != nil {
			return err
		}
		saved = current
		likeDelta, dislikeDelta := feedbackDelta(previous, current)
		return r.refreshChunkFeedbackStats(tx, feedback.TenantID, chunkIDs, likeDelta, dislikeDelta, feedback.MessageID, cfg)
	})
	return saved, err
}

func (r *messageFeedbackRepository) upsertFeedback(
	ctx context.Context,
	db *gorm.DB,
	feedback *types.MessageFeedback,
) (*types.MessageFeedback, *types.MessageFeedback, error) {
	var previous types.MessageFeedback
	previousErr := db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND message_id = ? AND user_id = ?",
			feedback.TenantID, feedback.SessionID, feedback.MessageID, feedback.UserID).
		First(&previous).Error
	if previousErr != nil && !errors.Is(previousErr, gorm.ErrRecordNotFound) {
		return nil, nil, previousErr
	}

	now := time.Now()
	feedback.UpdatedAt = now
	if err := db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "message_id"},
				{Name: "user_id"},
			},
			TargetWhere: clause.Where{Exprs: []clause.Expression{
				clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil},
			}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"action":     feedback.Action,
				"reason":     feedback.Reason,
				"session_id": feedback.SessionID,
				"updated_at": now,
			}),
		}).
		Create(feedback).Error; err != nil {
		return nil, nil, err
	}

	var current types.MessageFeedback
	if err := db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND message_id = ? AND user_id = ?",
			feedback.TenantID, feedback.SessionID, feedback.MessageID, feedback.UserID).
		First(&current).Error; err != nil {
		return nil, nil, err
	}

	if errors.Is(previousErr, gorm.ErrRecordNotFound) {
		return &current, nil, nil
	}
	return &current, &previous, nil
}

func (r *messageFeedbackRepository) DeleteFeedback(
	ctx context.Context,
	tenantID uint64,
	sessionID, messageID, userID string,
) (*types.MessageFeedback, error) {
	var feedback types.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND message_id = ? AND user_id = ?",
			tenantID, sessionID, messageID, userID).
		First(&feedback).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	if err := r.db.WithContext(ctx).Delete(&feedback).Error; err != nil {
		return nil, err
	}
	return &feedback, nil
}

func (r *messageFeedbackRepository) DeleteFeedbackAndRefreshChunks(
	ctx context.Context,
	tenantID uint64,
	sessionID, messageID, userID string,
	chunkIDs []string,
	cfg types.ChunkFeedbackConfig,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var feedback types.MessageFeedback
		err := tx.WithContext(ctx).
			Where("tenant_id = ? AND session_id = ? AND message_id = ? AND user_id = ?",
				tenantID, sessionID, messageID, userID).
			First(&feedback).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.WithContext(ctx).Delete(&feedback).Error; err != nil {
			return err
		}
		likeDelta, dislikeDelta := feedbackDelta(&feedback, nil)
		return r.refreshChunkFeedbackStats(tx, tenantID, chunkIDs, likeDelta, dislikeDelta, messageID, cfg)
	})
}

func (r *messageFeedbackRepository) ListFeedbacksByMessageIDs(
	ctx context.Context,
	tenantID uint64,
	sessionID, userID string,
	messageIDs []string,
) ([]*types.MessageFeedback, error) {
	if len(messageIDs) == 0 {
		return []*types.MessageFeedback{}, nil
	}
	var feedbacks []*types.MessageFeedback
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND user_id = ? AND message_id IN ?",
			tenantID, sessionID, userID, messageIDs).
		Find(&feedbacks).Error; err != nil {
		return nil, err
	}
	return feedbacks, nil
}

func (r *messageFeedbackRepository) CreateMessageChunkReferences(
	ctx context.Context,
	refs []*types.MessageChunkReference,
) error {
	if len(refs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(refs, 100).Error
}

func (r *messageFeedbackRepository) ListMessageChunkReferences(
	ctx context.Context,
	tenantID uint64,
	sessionID, messageID string,
) ([]*types.MessageChunkReference, error) {
	var refs []*types.MessageChunkReference
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND message_id = ?", tenantID, sessionID, messageID).
		Find(&refs).Error; err != nil {
		return nil, err
	}
	return refs, nil
}

func (r *messageFeedbackRepository) ApplyChunkFeedbackDelta(
	ctx context.Context,
	tenantID uint64,
	chunkIDs []string,
	likeDelta, dislikeDelta int64,
	messageID string,
	cfg types.ChunkFeedbackConfig,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.refreshChunkFeedbackStats(tx, tenantID, chunkIDs, likeDelta, dislikeDelta, messageID, cfg)
	})
}

func (r *messageFeedbackRepository) refreshChunkFeedbackStats(
	tx *gorm.DB,
	tenantID uint64,
	chunkIDs []string,
	likeDelta, dislikeDelta int64,
	messageID string,
	cfg types.ChunkFeedbackConfig,
) error {
	if len(chunkIDs) == 0 || (likeDelta == 0 && dislikeDelta == 0) {
		return nil
	}
	type chunkFeedbackAggregate struct {
		LikeCount    int64
		DislikeCount int64
	}

	var chunks []*types.Chunk
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id IN ?", tenantID, chunkIDs).
		Find(&chunks).Error; err != nil {
		return err
	}

	for _, chunk := range chunks {
		var aggregate chunkFeedbackAggregate
		query := tx.Table("message_chunk_references AS refs").
			Select(`
					COALESCE(SUM(CASE WHEN feedbacks.action = ? THEN 1 ELSE 0 END), 0) AS like_count,
					COALESCE(SUM(CASE WHEN feedbacks.action = ? THEN 1 ELSE 0 END), 0) AS dislike_count
				`, types.FeedbackActionLike, types.FeedbackActionDislike).
			Joins(`
					JOIN message_feedbacks AS feedbacks
						ON feedbacks.tenant_id = refs.tenant_id
						AND feedbacks.session_id = refs.session_id
						AND feedbacks.message_id = refs.message_id
						AND feedbacks.deleted_at IS NULL
				`).
			Where("refs.tenant_id = ? AND refs.chunk_id = ? AND refs.deleted_at IS NULL", tenantID, chunk.ID)
		if chunk.FeedbackResetAt != nil {
			query = query.Where("feedbacks.updated_at > ?", *chunk.FeedbackResetAt)
		}
		if err := query.Scan(&aggregate).Error; err != nil {
			return err
		}
		var lastFeedback types.MessageFeedback
		lastFeedbackQuery := tx.Table("message_chunk_references AS refs").
			Select("feedbacks.updated_at").
			Joins(`
					JOIN message_feedbacks AS feedbacks
						ON feedbacks.tenant_id = refs.tenant_id
						AND feedbacks.session_id = refs.session_id
						AND feedbacks.message_id = refs.message_id
						AND feedbacks.deleted_at IS NULL
				`).
			Where("refs.tenant_id = ? AND refs.chunk_id = ? AND refs.deleted_at IS NULL", tenantID, chunk.ID).
			Order("feedbacks.updated_at DESC").
			Limit(1)
		if chunk.FeedbackResetAt != nil {
			lastFeedbackQuery = lastFeedbackQuery.Where("feedbacks.updated_at > ?", *chunk.FeedbackResetAt)
		}
		var lastFeedbackAt *time.Time
		if err := lastFeedbackQuery.Scan(&lastFeedback).Error; err != nil {
			return err
		}
		if !lastFeedback.UpdatedAt.IsZero() {
			lastFeedbackAt = &lastFeedback.UpdatedAt
		}

		likeCount := aggregate.LikeCount
		dislikeCount := aggregate.DislikeCount
		total := likeCount + dislikeCount
		var positiveRate *float64
		if total > 0 {
			rate := float64(likeCount) / float64(total)
			positiveRate = &rate
		}
		recallWeight := cfg.RecallWeight(positiveRate)
		needsOptimization := cfg.NeedsOptimization(positiveRate)

		updates := map[string]interface{}{
			"like_count":         likeCount,
			"dislike_count":      dislikeCount,
			"positive_rate":      positiveRate,
			"recall_weight":      recallWeight,
			"last_feedback_at":   lastFeedbackAt,
			"needs_optimization": needsOptimization,
		}
		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND id = ?", tenantID, chunk.ID).
			Updates(updates).Error; err != nil {
			return err
		}
		if chunk.RecallWeight != recallWeight {
			log := &types.ChunkFeedbackWeightLog{
				TenantID:        tenantID,
				ChunkID:         chunk.ID,
				KnowledgeID:     chunk.KnowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				OldWeight:       chunk.RecallWeight,
				NewWeight:       recallWeight,
				OldPositiveRate: chunk.PositiveRate,
				NewPositiveRate: positiveRate,
				TriggerSource:   types.ChunkFeedbackSourceUserFeedback,
				MessageID:       messageID,
			}
			if err := tx.Create(log).Error; err != nil {
				return err
			}
		}
	}
	return nil
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
