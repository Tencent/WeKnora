package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm/clause"
)

// Methods on chunkRepository implementing the embedding-resume progress store.
// They live in this file to keep the resume feature self-contained.

// MarkChunksEmbedded records committed chunk vectors. Idempotent: duplicate
// (knowledge_id, chunk_id) pairs are ignored so retries can re-mark safely.
func (r *chunkRepository) MarkChunksEmbedded(ctx context.Context, knowledgeID string, chunkIDs []string) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	rows := make([]*types.EmbedProgress, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		rows = append(rows, &types.EmbedProgress{KnowledgeID: knowledgeID, ChunkID: id})
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(rows, 200).Error
}

// ListEmbeddedChunkIDs returns the chunk IDs whose vectors have been committed
// for the given knowledge.
func (r *chunkRepository) ListEmbeddedChunkIDs(ctx context.Context, knowledgeID string) (map[string]struct{}, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&types.EmbedProgress{}).
		Where("knowledge_id = ?", knowledgeID).
		Pluck("chunk_id", &ids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

// DeleteEmbedProgressByKnowledgeID clears all resume progress for a knowledge.
func (r *chunkRepository) DeleteEmbedProgressByKnowledgeID(ctx context.Context, knowledgeID string) error {
	return r.db.WithContext(ctx).
		Where("knowledge_id = ?", knowledgeID).
		Delete(&types.EmbedProgress{}).Error
}

// Ensure the interface stays satisfied if gorm signatures change elsewhere.
var _ interface {
	MarkChunksEmbedded(context.Context, string, []string) error
	ListEmbeddedChunkIDs(context.Context, string) (map[string]struct{}, error)
	DeleteEmbedProgressByKnowledgeID(context.Context, string) error
} = (*chunkRepository)(nil)
