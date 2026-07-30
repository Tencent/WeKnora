package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// FeedbackRepository handles message_chunk_links and chunk_weight_logs persistence.
type FeedbackRepository struct {
	db *gorm.DB
}

// NewFeedbackRepository creates a new FeedbackRepository.
func NewFeedbackRepository(db *gorm.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

// CreateMessageChunkLinks bulk-inserts message-chunk links.
func (r *FeedbackRepository) CreateMessageChunkLinks(ctx context.Context, links []*types.MessageChunkLink) error {
	if len(links) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&links).Error
}

// GetMessageChunkLinks retrieves chunk links for a given message.
func (r *FeedbackRepository) GetMessageChunkLinks(ctx context.Context, messageID string) ([]*types.MessageChunkLink, error) {
	var links []*types.MessageChunkLink
	err := r.db.WithContext(ctx).Where("message_id = ?", messageID).Find(&links).Error
	return links, err
}

// CreateWeightLog records a weight change entry.
func (r *FeedbackRepository) CreateWeightLog(ctx context.Context, log *types.ChunkWeightLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

// ListWeightLogsByChunk returns weight change history for a chunk (paginated).
func (r *FeedbackRepository) ListWeightLogsByChunk(ctx context.Context, tenantID uint64, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	var logs []*types.ChunkWeightLog
	var total int64
	query := r.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// ListWeightLogs returns paginated weight change logs for a tenant.
func (r *FeedbackRepository) ListWeightLogs(ctx context.Context, tenantID uint64, page, pageSize int) ([]*types.ChunkWeightLog, int64, error) {
	var logs []*types.ChunkWeightLog
	var total int64
	query := r.db.WithContext(ctx).Model(&types.ChunkWeightLog{}).Where("tenant_id = ?", tenantID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// ListChunkFeedbackStats returns all chunks with their feedback stats for a tenant, paginated.
func (r *FeedbackRepository) ListChunkFeedbackStats(ctx context.Context, tenantID uint64, kbID string, page, pageSize int) ([]*types.ChunkFeedbackStats, int64, error) {
	var chunks []*types.ChunkFeedbackStats
	var total int64
	query := r.db.WithContext(ctx).Table("chunks").
		Select("chunks.id as chunk_id, chunks.like_count, chunks.dislike_count, chunks.like_rate, chunks.recall_weight, chunks.content, chunks.knowledge_id, knowledges.title as knowledge_title").
		Joins("LEFT JOIN knowledges ON chunks.knowledge_id = knowledges.id").
		Where("chunks.tenant_id = ?", tenantID)
	if kbID != "" {
		query = query.Where("chunks.knowledge_base_id = ?", kbID)
	}
	query = query.Where("(chunks.like_count > 0 OR chunks.dislike_count > 0)")
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := query.Order("chunks.like_count DESC").Offset(offset).Limit(pageSize).Find(&chunks).Error; err != nil {
		return nil, 0, err
	}
	return chunks, total, nil
}

// ResetChunkFeedback resets feedback data and weight for a chunk.
func (r *FeedbackRepository) ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkID string) error {
	return r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("id = ? AND tenant_id = ?", chunkID, tenantID).
		Updates(map[string]interface{}{
			"like_count":    0,
			"dislike_count": 0,
			"like_rate":     0.0,
			"recall_weight": 1.0,
		}).Error
}
