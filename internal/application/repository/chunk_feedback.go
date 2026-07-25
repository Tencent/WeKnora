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

type qaReplyChunkRefRepository struct {
	db *gorm.DB
}

// NewQAReplyChunkRefRepository 创建 QAReplyChunkRefRepository 实例
func NewQAReplyChunkRefRepository(db *gorm.DB) interfaces.QAReplyChunkRefRepository {
	return &qaReplyChunkRefRepository{db: db}
}

type chunkFeedbackUnitOfWork struct {
	db *gorm.DB
}

func NewChunkFeedbackUnitOfWork(db *gorm.DB) interfaces.ChunkFeedbackUnitOfWork {
	return &chunkFeedbackUnitOfWork{db: db}
}

func (u *chunkFeedbackUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, repos interfaces.ChunkFeedbackRepositories) error) error {
	return u.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(ctx, interfaces.ChunkFeedbackRepositories{
			QARefRepo:     &qaReplyChunkRefRepository{db: tx},
			FeedbackRepo:  &chunkFeedbackRepository{db: tx},
			MessageRepo:   &messageRepository{db: tx},
			ChunkRepo:     &chunkRepository{db: tx},
			WeightLogRepo: &chunkWeightLogRepository{db: tx},
		})
	})
}

func (r *qaReplyChunkRefRepository) Create(ctx context.Context, ref *types.QAReplyChunkRef) error {
	if ref.ChunkTenantID == 0 {
		ref.ChunkTenantID = ref.TenantID
	}
	return r.db.WithContext(ctx).Create(ref).Error
}

func (r *qaReplyChunkRefRepository) CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error {
	if len(refs) == 0 {
		return nil
	}
	for _, ref := range refs {
		if ref != nil && ref.ChunkTenantID == 0 {
			ref.ChunkTenantID = ref.TenantID
		}
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(refs, 100).Error
}

func (r *qaReplyChunkRefRepository) GetByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRef, error) {
	var refs []*types.QAReplyChunkRef
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
		Find(&refs).Error
	return refs, err
}

func (r *qaReplyChunkRefRepository) GetByChunkID(ctx context.Context, tenantID uint64, chunkID string) ([]*types.QAReplyChunkRef, error) {
	var refs []*types.QAReplyChunkRef
	err := r.db.WithContext(ctx).
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Find(&refs).Error
	return refs, err
}

func (r *qaReplyChunkRefRepository) CreateResetTombstones(ctx context.Context, refs []*types.QAReplyChunkRef, operator string) error {
	if len(refs) == 0 {
		return nil
	}
	tombstones := make([]*types.QAReplyChunkRefTombstone, 0, len(refs))
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		chunkTenantID := ref.ChunkTenantID
		if chunkTenantID == 0 {
			chunkTenantID = ref.TenantID
		}
		tombstones = append(tombstones, &types.QAReplyChunkRefTombstone{
			MessageID:     ref.MessageID,
			ChunkID:       ref.ChunkID,
			TenantID:      ref.TenantID,
			ChunkTenantID: chunkTenantID,
			Operator:      operator,
		})
	}
	if len(tombstones) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(tombstones, 100).Error
}

func (r *qaReplyChunkRefRepository) GetResetTombstonesByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRefTombstone, error) {
	var tombstones []*types.QAReplyChunkRefTombstone
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
		Find(&tombstones).Error
	return tombstones, err
}

func (r *qaReplyChunkRefRepository) DeleteByMessageID(ctx context.Context, tenantID uint64, messageID string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
		Delete(&types.QAReplyChunkRef{}).Error
}

func (r *qaReplyChunkRefRepository) DeleteByChunkID(ctx context.Context, chunkTenantID uint64, chunkID string) error {
	return r.db.WithContext(ctx).
		Where("chunk_tenant_id = ? AND chunk_id = ?", chunkTenantID, chunkID).
		Delete(&types.QAReplyChunkRef{}).Error
}

func (r *qaReplyChunkRefRepository) CountByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.QAReplyChunkRef{}).
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Count(&count).Error
	return count, err
}

func (r *qaReplyChunkRefRepository) CountSessionsByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("qa_reply_chunk_refs qrcr").
		Joins("JOIN messages m ON m.id = qrcr.message_id AND m.deleted_at IS NULL").
		Joins("JOIN sessions s ON s.id = m.session_id AND s.tenant_id = qrcr.tenant_id AND s.deleted_at IS NULL").
		Where("qrcr.chunk_tenant_id = ? AND qrcr.chunk_id = ?", tenantID, chunkID).
		Distinct("s.id").
		Count(&count).Error
	return count, err
}

type chunkFeedbackRepository struct {
	db *gorm.DB
}

// NewChunkFeedbackRepository 创建 ChunkFeedbackRepository 实例
func NewChunkFeedbackRepository(db *gorm.DB) interfaces.ChunkFeedbackRepository {
	return &chunkFeedbackRepository{db: db}
}

func (r *chunkFeedbackRepository) Create(ctx context.Context, feedback *types.ChunkFeedback) error {
	return r.db.WithContext(ctx).Create(feedback).Error
}

func (r *chunkFeedbackRepository) Update(ctx context.Context, feedback *types.ChunkFeedback) error {
	feedback.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(feedback).Error
}

func (r *chunkFeedbackRepository) Upsert(ctx context.Context, messageID, sessionID, userID string, tenantID uint64, isPositive bool, dislikeReason string) (*types.ChunkFeedback, error) {
	feedback, err := r.findFeedbackForUpdate(ctx, tenantID, messageID, userID)
	if err == nil {
		return r.updateExistingFeedback(ctx, feedback, isPositive, dislikeReason)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	feedback = &types.ChunkFeedback{
		MessageID:     messageID,
		SessionID:     sessionID,
		TenantID:      tenantID,
		UserID:        userID,
		IsPositive:    isPositive,
		DislikeReason: dislikeReason,
	}
	tx := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "message_id"},
				{Name: "user_id"},
			},
			DoNothing: true,
		}).
		Create(feedback)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected > 0 {
		feedback.WasCreated = true
		feedback.IsChanged = true
		return feedback, nil
	}

	feedback, err = r.findFeedbackForUpdate(ctx, tenantID, messageID, userID)
	if err != nil {
		return nil, err
	}
	return r.updateExistingFeedback(ctx, feedback, isPositive, dislikeReason)
}

func (r *chunkFeedbackRepository) findFeedbackForUpdate(ctx context.Context, tenantID uint64, messageID, userID string) (*types.ChunkFeedback, error) {
	var feedback types.ChunkFeedback
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND message_id = ? AND user_id = ?", tenantID, messageID, userID).
		First(&feedback).Error
	if err != nil {
		return nil, err
	}
	return &feedback, nil
}

func (r *chunkFeedbackRepository) updateExistingFeedback(ctx context.Context, feedback *types.ChunkFeedback, isPositive bool, dislikeReason string) (*types.ChunkFeedback, error) {
	wasPositive := feedback.IsPositive
	feedback.IsPositive = isPositive
	feedback.DislikeReason = dislikeReason
	feedback.UpdatedAt = time.Now()
	if err := r.db.WithContext(ctx).Save(feedback).Error; err != nil {
		return nil, err
	}
	feedback.WasCreated = false
	feedback.PreviousIsPositive = wasPositive
	feedback.IsChanged = wasPositive != isPositive
	return feedback, nil
}

func (r *chunkFeedbackRepository) GetByMessageID(ctx context.Context, tenantID uint64, messageID string) (*types.ChunkFeedback, error) {
	var feedback types.ChunkFeedback
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND message_id = ?", tenantID, messageID).
		First(&feedback).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &feedback, nil
}

func (r *chunkFeedbackRepository) GetByMessageAndUser(ctx context.Context, tenantID uint64, messageID, userID string) (*types.ChunkFeedback, error) {
	var feedback types.ChunkFeedback
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND message_id = ? AND user_id = ?", tenantID, messageID, userID).
		First(&feedback).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &feedback, nil
}

func (r *chunkFeedbackRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Delete(&types.ChunkFeedback{}, "tenant_id = ? AND id = ?", tenantID, id).Error
}

func (r *chunkFeedbackRepository) GetDislikeReasonsByChunkIDs(ctx context.Context, tenantID uint64, chunkIDs []string) (map[string][]string, error) {
	// 获取所有与这些 chunk 关联的消息的点踩原因
	type MessageChunkResult struct {
		ChunkID string
		Reason  string
	}
	var results []MessageChunkResult
	err := r.db.WithContext(ctx).
		Table("chunk_feedbacks cf").
		Select("qrcr.chunk_id as chunk_id, cf.dislike_reason as reason").
		Joins("JOIN qa_reply_chunk_refs qrcr ON cf.tenant_id = qrcr.tenant_id AND cf.message_id = qrcr.message_id").
		Where("qrcr.chunk_tenant_id = ? AND qrcr.chunk_id IN ? AND cf.is_positive = ? AND cf.dislike_reason IS NOT NULL AND cf.dislike_reason != ''", tenantID, chunkIDs, false).
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	reasonMap := make(map[string][]string)
	for _, r := range results {
		reasonMap[r.ChunkID] = append(reasonMap[r.ChunkID], r.Reason)
	}
	return reasonMap, nil
}

type chunkWeightLogRepository struct {
	db *gorm.DB
}

// NewChunkWeightLogRepository 创建 ChunkWeightLogRepository 实例
func NewChunkWeightLogRepository(db *gorm.DB) interfaces.ChunkWeightLogRepository {
	return &chunkWeightLogRepository{db: db}
}

func (r *chunkWeightLogRepository) Create(ctx context.Context, log *types.ChunkWeightLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *chunkWeightLogRepository) GetByChunkID(ctx context.Context, tenantID uint64, chunkID string, limit int) ([]*types.ChunkWeightLog, error) {
	var logs []*types.ChunkWeightLog
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&logs).Error
	return logs, err
}

func (r *chunkWeightLogRepository) CountByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.ChunkWeightLog{}).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Count(&count).Error
	return count, err
}
