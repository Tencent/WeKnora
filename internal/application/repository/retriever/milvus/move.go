package milvus

import (
	"context"
	"fmt"
)

func (m *milvusRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	collection := m.getCollectionName(dimension)
	filter := &universalFilterCondition{Operator: operatorAnd, Value: []*universalFilterCondition{
		{Field: fieldKnowledgeBaseID, Operator: operatorEqual, Value: sourceKB},
		{Field: fieldKnowledgeID, Operator: operatorEqual, Value: knowledgeID},
	}}
	size, offset := 100, 0
	return drainMoveRows(ctx, targetKB, func(ctx context.Context) ([]*MilvusVectorEmbeddingWithScore, error) {
		rows, _, err := m.searchByFilter(ctx, collection, filter, &size, &offset)
		return rows, err
	}, func(ctx context.Context, rows []*MilvusVectorEmbedding) error {
		_, err := m.client.Upsert(ctx, createUpsert(collection, rows))
		return err
	})
}

// Drain the source predicate without offset windows. A repeated ID means the
// backend has not made the acknowledged update visible; fail for retry instead
// of advancing the document checkpoint or silently omitting remaining vectors.
func drainMoveRows(ctx context.Context, targetKB string,
	read func(context.Context) ([]*MilvusVectorEmbeddingWithScore, error),
	write func(context.Context, []*MilvusVectorEmbedding) error,
) error {
	seen := map[string]bool{}
	for {
		rows, err := read(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		batch := make([]*MilvusVectorEmbedding, 0, len(rows))
		for _, row := range rows {
			if row == nil || row.ID == "" || seen[row.ID] {
				return fmt.Errorf("invalid or repeated move index")
			}
			seen[row.ID] = true
			copyOfRow := row.MilvusVectorEmbedding
			copyOfRow.KnowledgeBaseID = targetKB
			copyOfRow.TagID = ""
			batch = append(batch, &copyOfRow)
		}
		if err := write(ctx, batch); err != nil {
			return err
		}
	}
}
