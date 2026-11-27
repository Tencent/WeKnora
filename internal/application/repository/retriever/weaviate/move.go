// Package weaviate implements retrieval and metadata updates in Weaviate.
package weaviate

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v6/data"
	"github.com/weaviate/weaviate-go-client/v6/query"
	"github.com/weaviate/weaviate-go-client/v6/query/filter"
)

func (w *weaviateRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	collection := w.client.Collections.Use(w.getCollectionName(dimension))
	where := filter.And{
		filter.Cond{Target: fieldKnowledgeBaseID, Operator: filter.Equal, Value: sourceKB},
		filter.Cond{Target: fieldKnowledgeID, Operator: filter.Equal, Value: knowledgeID},
	}
	seen := make(map[uuid.UUID]bool)
	for {
		result, err := collection.Query.OverAll(ctx, query.OverAll{
			Filter: where,
			Limit:  moveQueryLimit,
			// IDs only: an explicitly empty list returns no properties.
			ReturnProperties: []string{},
		})
		if err != nil {
			return err
		}
		if result == nil {
			return fmt.Errorf("failed to list move indices")
		}
		if len(result.Objects) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(result.Objects))
		for _, obj := range result.Objects {
			id := obj.UUID
			if id == uuid.Nil {
				return fmt.Errorf("invalid move index ID")
			}
			if seen[id] {
				return fmt.Errorf("move indices made no progress for %s", id)
			}
			seen[id] = true
			ids = append(ids, id)
		}
		// Drain the first filtered page; after+where is unsupported by Weaviate.
		// Successful updates remove these IDs from the predicate. Only an empty
		// follow-up query proves completion, including after partial-page updates.
		for _, id := range ids {
			if err := collection.Data.Update(ctx, data.Object{
				UUID:       &id,
				Properties: map[string]any{fieldKnowledgeBaseID: targetKB, fieldTagID: ""},
			}); err != nil {
				return err
			}
		}
	}
}
