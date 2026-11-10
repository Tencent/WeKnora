package doris

import (
	"context"
	"fmt"
)

func (r *dorisRepository) ValidateKnowledgeIndexMove(ctx context.Context) error {
	mode, err := r.resolveCompatMode(ctx)
	if err != nil {
		return err
	}
	if mode.usesRewriteChunkUpdates() {
		// ANN DUPLICATE KEY tables implement replacement as DELETE + INSERT.
		// A failed insert would lose the only vector copy; changing physical IDs
		// would break the stable source-ID identity used by subsequent edits.
		return fmt.Errorf("reuse_vectors move is not supported by Doris ANN tables; use reparse mode")
	}
	return nil
}

func (r *dorisRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	if err := r.ValidateKnowledgeIndexMove(ctx); err != nil {
		return err
	}
	if dimension <= 0 {
		return fmt.Errorf("invalid embedding dimension")
	}
	query := fmt.Sprintf(
		"UPDATE `%s` SET knowledge_base_id = ?, tag_id = '' WHERE knowledge_base_id = ? AND knowledge_id = ?",
		r.getTableName(dimension),
	)
	_, err := r.db.ExecContext(ctx, query, targetKB, sourceKB, knowledgeID)
	return err
}
