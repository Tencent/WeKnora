package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// feedbackRepository implements interfaces.FeedbackRepository.
type feedbackRepository struct {
	db *gorm.DB
}

// NewFeedbackRepository creates a new FeedbackRepository.
func NewFeedbackRepository(db *gorm.DB) interfaces.FeedbackRepository {
	return &feedbackRepository{db: db}
}

func (r *feedbackRepository) GetFeedback(ctx context.Context, userID, messageID string) (*types.MessageFeedback, error) {
	var fb types.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND message_id = ?", userID, messageID).
		First(&fb).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &fb, nil
}

func (r *feedbackRepository) UpsertFeedback(ctx context.Context, fb *types.MessageFeedback) error {
	// Upsert on (user_id, message_id) unique index.
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"feedback_type", "reason", "reason_detail", "updated_at",
		}),
	}).Create(fb).Error
}

func (r *feedbackRepository) DeleteFeedback(ctx context.Context, userID, messageID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND message_id = ?", userID, messageID).
		Delete(&types.MessageFeedback{}).Error
}

// EnsureChunkRefs populates message_chunk_refs from the message's
// KnowledgeReferences JSON. Idempotent — skips chunks already linked.
func (r *feedbackRepository) EnsureChunkRefs(ctx context.Context, msg *types.Message) error {
	if msg == nil || len(msg.KnowledgeReferences) == 0 {
		return nil
	}

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return fmt.Errorf("EnsureChunkRefs: tenant ID not found in context")
	}

	// Collect chunk IDs from KnowledgeReferences (each *SearchResult has an ID
	// field that is the chunk ID).
	chunkIDs := make([]string, 0, len(msg.KnowledgeReferences))
	chunkMeta := make(map[string]*types.SearchResult, len(msg.KnowledgeReferences))
	for _, ref := range msg.KnowledgeReferences {
		if ref == nil || ref.ID == "" {
			continue
		}
		chunkIDs = append(chunkIDs, ref.ID)
		chunkMeta[ref.ID] = ref
	}
	if len(chunkIDs) == 0 {
		return nil
	}

	// Find which chunk IDs are already linked to avoid duplicates.
	var existing []string
	err := r.db.WithContext(ctx).
		Model(&types.MessageChunkRef{}).
		Where("message_id = ? AND chunk_id IN ?", msg.ID, chunkIDs).
		Pluck("chunk_id", &existing).Error
	if err != nil {
		return fmt.Errorf("querying existing chunk refs: %w", err)
	}
	existingSet := make(map[string]bool, len(existing))
	for _, id := range existing {
		existingSet[id] = true
	}

	refs := make([]*types.MessageChunkRef, 0, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		if existingSet[chunkID] {
			continue
		}
		ref := chunkMeta[chunkID]
		refs = append(refs, &types.MessageChunkRef{
			TenantID:    tenantID,
			MessageID:   msg.ID,
			SessionID:   msg.SessionID,
			ChunkID:     chunkID,
			KnowledgeID: ref.KnowledgeID,
			KBID:        ref.KnowledgeBaseID,
		})
	}
	if len(refs) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).CreateInBatches(refs, 100).Error
}

func (r *feedbackRepository) ListChunkIDsByMessage(ctx context.Context, messageID string) ([]string, error) {
	var chunkIDs []string
	err := r.db.WithContext(ctx).
		Model(&types.MessageChunkRef{}).
		Where("message_id = ?", messageID).
		Pluck("chunk_id", &chunkIDs).Error
	return chunkIDs, err
}

func (r *feedbackRepository) CountDistinctSessionsByChunk(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.MessageChunkRef{}).
		Where("tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Distinct("session_id").
		Count(&count).Error
	return count, err
}

func (r *feedbackRepository) AggregateDislikeReasonsByChunk(ctx context.Context, tenantID uint64, chunkID string) ([]types.DislikeReasonCount, error) {
	var results []types.DislikeReasonCount
	// JOIN message_feedback through message_chunk_refs to get dislike reasons
	// attributed to this chunk.
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT mf.reason AS reason, COUNT(*) AS count
			FROM message_feedback mf
			INNER JOIN message_chunk_refs mcr
				ON mf.message_id = mcr.message_id
			WHERE mcr.tenant_id = ? AND mcr.chunk_id = ?
			  AND mf.feedback_type = 'dislike'
			  AND mf.reason <> ''
			GROUP BY mf.reason
			ORDER BY count DESC
		`, tenantID, chunkID).
		Scan(&results).Error
	return results, err
}

func (r *feedbackRepository) CountFeedbackByChunk(ctx context.Context, tenantID uint64, chunkID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT COUNT(*)
			FROM message_feedback mf
			INNER JOIN message_chunk_refs mcr
				ON mf.message_id = mcr.message_id
			WHERE mcr.tenant_id = ? AND mcr.chunk_id = ?
			  AND mf.feedback_type IN ('like', 'dislike')
		`, tenantID, chunkID).
		Count(&count).Error
	return count, err
}
