package milvus

import (
	"context"
	"slices"
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
	// Finish the paginated read before changing its predicate. Mutating each
	// page while increasing offset would skip source rows on later pages.
	var rows []*MilvusVectorEmbedding
	size := 100
	for offset := 0; ; {
		found, count, err := m.searchByFilter(ctx, collection, filter, &size, &offset)
		if err != nil {
			return err
		}
		for _, row := range found {
			row.KnowledgeBaseID = targetKB
			row.TagID = ""
			rows = append(rows, &row.MilvusVectorEmbedding)
		}
		if count < size {
			break
		}
		offset += count
	}
	for batch := range slices.Chunk(rows, size) {
		if _, err := m.client.Upsert(ctx, createUpsert(collection, batch)); err != nil {
			return err
		}
	}
	return nil
}
