package repository

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// chunkFeedbackStatsRepository implements interfaces.ChunkFeedbackStatsRepository.
type chunkFeedbackStatsRepository struct {
	db *gorm.DB
}

// NewChunkFeedbackStatsRepository creates a new ChunkFeedbackStatsRepository.
func NewChunkFeedbackStatsRepository(db *gorm.DB) interfaces.ChunkFeedbackStatsRepository {
	return &chunkFeedbackStatsRepository{db: db}
}

// contentPreviewLength is the max characters of chunk content included in stats.
const contentPreviewLength = 200

func (r *chunkFeedbackStatsRepository) GetChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackStats, error) {
	var chunk types.Chunk
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, chunkID).
		First(&chunk).Error
	if err != nil {
		return nil, fmt.Errorf("chunk not found: %w", err)
	}

	stats := &types.ChunkFeedbackStats{
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		ChunkIndex:      chunk.ChunkIndex,
		ChunkType:       chunk.ChunkType,
		ContentPreview:  truncateContent(chunk.Content, contentPreviewLength),
		LikeCount:       chunk.LikeCount,
		DislikeCount:    chunk.DislikeCount,
		ApprovalRate:    chunk.ApprovalRate,
		RecallWeight:    chunk.RecallWeight,
		NeedsOptimization: chunk.NeedsOptimization,
	}
	return stats, nil
}

func (r *chunkFeedbackStatsRepository) ListChunkFeedbackStats(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	page, pageSize int,
	minApproval, maxApproval float64,
	needsOptimizationOnly bool,
) ([]*types.ChunkFeedbackStats, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := r.db.WithContext(ctx).
		Model(&types.Chunk{}).
		Where("tenant_id = ?", tenantID)

	if kbID != "" {
		query = query.Where("knowledge_base_id = ?", kbID)
	}
	if minApproval >= 0 {
		query = query.Where("approval_rate >= ?", minApproval)
	}
	if maxApproval > 0 {
		query = query.Where("approval_rate <= ?", maxApproval)
	}
	if needsOptimizationOnly {
		query = query.Where("needs_optimization = true")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var chunks []types.Chunk
	if err := query.
		Order("needs_optimization DESC, approval_rate ASC, updated_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&chunks).Error; err != nil {
		return nil, 0, err
	}

	results := make([]*types.ChunkFeedbackStats, 0, len(chunks))
	for i := range chunks {
		ch := &chunks[i]
		results = append(results, &types.ChunkFeedbackStats{
			ChunkID:           ch.ID,
			KnowledgeID:       ch.KnowledgeID,
			KnowledgeBaseID:   ch.KnowledgeBaseID,
			ChunkIndex:        ch.ChunkIndex,
			ChunkType:         ch.ChunkType,
			ContentPreview:    truncateContent(ch.Content, contentPreviewLength),
			LikeCount:         ch.LikeCount,
			DislikeCount:      ch.DislikeCount,
			ApprovalRate:      ch.ApprovalRate,
			RecallWeight:      ch.RecallWeight,
			NeedsOptimization: ch.NeedsOptimization,
		})
	}
	return results, total, nil
}

func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}
