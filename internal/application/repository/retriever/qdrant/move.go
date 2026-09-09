// Package qdrant implements vector retrieval and metadata updates in Qdrant.
package qdrant

import (
	"context"

	"github.com/qdrant/go-client/qdrant"
)

func (q *qdrantRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	wait := true
	_, err := q.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: q.getCollectionName(dimension), Wait: &wait,
		Payload: newQdrantValueMap(map[string]any{fieldKnowledgeBaseID: targetKB, fieldTagID: ""}),
		PointsSelector: qdrant.NewPointsSelectorFilter(&qdrant.Filter{Must: []*qdrant.Condition{
			qdrant.NewMatch(fieldKnowledgeBaseID, sourceKB), qdrant.NewMatch(fieldKnowledgeID, knowledgeID),
		}}),
	})
	return err
}
