package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// QAReplyChunkRefRepository 问答回复与片段关联 Repository
type QAReplyChunkRefRepository interface {
	Create(ctx context.Context, ref *types.QAReplyChunkRef) error
	CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error
	GetByMessageID(ctx context.Context, messageID string) ([]*types.QAReplyChunkRef, error)
	GetByChunkID(ctx context.Context, chunkID string) ([]*types.QAReplyChunkRef, error)
	DeleteByMessageID(ctx context.Context, messageID string) error
	CountByChunkID(ctx context.Context, chunkID string) (int64, error)
}

type qaReplyChunkRefRepository struct {
	db *gorm.DB
}

// NewQAReplyChunkRefRepository 创建 QAReplyChunkRefRepository 实例
func NewQAReplyChunkRefRepository(db *gorm.DB) QAReplyChunkRefRepository {
	return &qaReplyChunkRefRepository{db: db}
}

func (r *qaReplyChunkRefRepository) Create(ctx context.Context, ref *types.QAReplyChunkRef) error {
	return r.db.WithContext(ctx).Create(ref).Error
}

func (r *qaReplyChunkRefRepository) CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error {
	if len(refs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(refs, 100).Error
}

func (r *qaReplyChunkRefRepository) GetByMessageID(ctx context.Context, messageID string) ([]*types.QAReplyChunkRef, error) {
	var refs []*types.QAReplyChunkRef
	err := r.db.WithContext(ctx).
		Where("message_id = ?", messageID).
		Find(&refs).Error
	return refs, err
}

func (r *qaReplyChunkRefRepository) GetByChunkID(ctx context.Context, chunkID string) ([]*types.QAReplyChunkRef, error) {
	var refs []*types.QAReplyChunkRef
	err := r.db.WithContext(ctx).
		Where("chunk_id = ?", chunkID).
		Find(&refs).Error
	return refs, err
}

func (r *qaReplyChunkRefRepository) DeleteByMessageID(ctx context.Context, messageID string) error {
	return r.db.WithContext(ctx).
		Where("message_id = ?", messageID).
		Delete(&types.QAReplyChunkRef{}).Error
}

func (r *qaReplyChunkRefRepository) CountByChunkID(ctx context.Context, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.QAReplyChunkRef{}).
		Where("chunk_id = ?", chunkID).
		Count(&count).Error
	return count, err
}

// ChunkFeedbackRepository 用户评价 Repository
type ChunkFeedbackRepository interface {
	Create(ctx context.Context, feedback *types.ChunkFeedback) error
	Update(ctx context.Context, feedback *types.ChunkFeedback) error
	Upsert(ctx context.Context, messageID, sessionID, userID string, tenantID uint64, isPositive bool, dislikeReason string) (*types.ChunkFeedback, error)
	GetByMessageID(ctx context.Context, messageID string) (*types.ChunkFeedback, error)
	GetByMessageAndUser(ctx context.Context, messageID, userID string) (*types.ChunkFeedback, error)
	Delete(ctx context.Context, id string) error
	GetDislikeReasonsByChunkIDs(ctx context.Context, chunkIDs []string) (map[string][]string, error)
}

type chunkFeedbackRepository struct {
	db *gorm.DB
}

// NewChunkFeedbackRepository 创建 ChunkFeedbackRepository 实例
func NewChunkFeedbackRepository(db *gorm.DB) ChunkFeedbackRepository {
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
	var feedback types.ChunkFeedback
	
	query := r.db.WithContext(ctx).Where("message_id = ? AND user_id = ?", messageID, userID)
	
	// 如果没有 userID，使用 messageID 作为唯一标识
	if userID == "" {
		query = r.db.WithContext(ctx).Where("message_id = ?", messageID)
	}
	
	err := query.First(&feedback).Error
	if err == nil {
		// 记录之前的评价状态
		wasPositive := feedback.IsPositive
		
		// 更新现有记录
		feedback.IsPositive = isPositive
		feedback.DislikeReason = dislikeReason
		feedback.UpdatedAt = time.Now()
		
		if err := r.db.WithContext(ctx).Save(&feedback).Error; err != nil {
			return nil, err
		}
		
		// 返回是否从点赞变为点踩（或相反）
		feedback.IsChanged = (wasPositive != isPositive)
		return &feedback, nil
	}
	
	if err == gorm.ErrRecordNotFound {
		// 创建新记录
		feedback = types.ChunkFeedback{
			MessageID:     messageID,
			SessionID:     sessionID,
			TenantID:      tenantID,
			UserID:        userID,
			IsPositive:    isPositive,
			DislikeReason: dislikeReason,
		}
		feedback.IsChanged = true // 新记录视为变化
		if err := r.db.WithContext(ctx).Create(&feedback).Error; err != nil {
			return nil, err
		}
		return &feedback, nil
	}
	
	return nil, err
}

func (r *chunkFeedbackRepository) GetByMessageID(ctx context.Context, messageID string) (*types.ChunkFeedback, error) {
	var feedback types.ChunkFeedback
	err := r.db.WithContext(ctx).
		Where("message_id = ?", messageID).
		First(&feedback).Error
	if err != nil {
		return nil, err
	}
	return &feedback, nil
}

func (r *chunkFeedbackRepository) GetByMessageAndUser(ctx context.Context, messageID, userID string) (*types.ChunkFeedback, error) {
	var feedback types.ChunkFeedback
	query := r.db.WithContext(ctx).Where("message_id = ?", messageID)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	err := query.First(&feedback).Error
	if err != nil {
		return nil, err
	}
	return &feedback, nil
}

func (r *chunkFeedbackRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&types.ChunkFeedback{}, "id = ?", id).Error
}

func (r *chunkFeedbackRepository) GetDislikeReasonsByChunkIDs(ctx context.Context, chunkIDs []string) (map[string][]string, error) {
	// 获取所有与这些 chunk 关联的消息的点踩原因
	type MessageChunkResult struct {
		ChunkID string
		Reason  string
	}
	
	var results []MessageChunkResult
	err := r.db.WithContext(ctx).
		Table("chunk_feedbacks cf").
		Select("qrcr.chunk_id as chunk_id, cf.dislike_reason as reason").
		Joins("JOIN qa_reply_chunk_refs qrcr ON cf.message_id = qrcr.message_id").
		Where("qrcr.chunk_id IN ? AND cf.is_positive = ? AND cf.dislike_reason IS NOT NULL AND cf.dislike_reason != ''", chunkIDs, false).
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

// ChunkWeightLogRepository 权重变更日志 Repository
type ChunkWeightLogRepository interface {
	Create(ctx context.Context, log *types.ChunkWeightLog) error
	GetByChunkID(ctx context.Context, chunkID string, limit int) ([]*types.ChunkWeightLog, error)
	CountByChunkID(ctx context.Context, chunkID string) (int64, error)
}

type chunkWeightLogRepository struct {
	db *gorm.DB
}

// NewChunkWeightLogRepository 创建 ChunkWeightLogRepository 实例
func NewChunkWeightLogRepository(db *gorm.DB) ChunkWeightLogRepository {
	return &chunkWeightLogRepository{db: db}
}

func (r *chunkWeightLogRepository) Create(ctx context.Context, log *types.ChunkWeightLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *chunkWeightLogRepository) GetByChunkID(ctx context.Context, chunkID string, limit int) ([]*types.ChunkWeightLog, error) {
	var logs []*types.ChunkWeightLog
	query := r.db.WithContext(ctx).
		Where("chunk_id = ?", chunkID).
		Order("created_at DESC")
	
	if limit > 0 {
		query = query.Limit(limit)
	}
	
	err := query.Find(&logs).Error
	return logs, err
}

func (r *chunkWeightLogRepository) CountByChunkID(ctx context.Context, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.ChunkWeightLog{}).
		Where("chunk_id = ?", chunkID).
		Count(&count).Error
	return count, err
}
