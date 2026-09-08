// Package sqlite implements local retrieval with SQLite FTS and vectors.
package sqlite

import "context"

func (r *sqliteRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	_ int,
	_ string,
) error {
	// FTS and vector rows keep their IDs. Queries join them to this metadata row.
	return r.db.WithContext(ctx).Model(&sqliteEmbedding{}).
		Where("knowledge_base_id = ? AND knowledge_id = ?", sourceKB, knowledgeID).
		Updates(map[string]any{"knowledge_base_id": targetKB, "tag_id": ""}).Error
}
