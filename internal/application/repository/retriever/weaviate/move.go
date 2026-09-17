// Package weaviate implements retrieval and metadata updates in Weaviate.
package weaviate

import (
	"context"
	"fmt"

	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
)

func (w *weaviateRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	collection := w.getCollectionName(dimension)
	where := filters.Where().WithOperator(filters.And).WithOperands([]*filters.WhereBuilder{
		filters.Where().WithPath([]string{fieldKnowledgeBaseID}).WithOperator(filters.Equal).WithValueString(sourceKB),
		filters.Where().WithPath([]string{fieldKnowledgeID}).WithOperator(filters.Equal).WithValueString(knowledgeID),
	})
	seen := make(map[string]bool)
	for {
		result, err := w.client.GraphQL().Get().WithClassName(collection).WithWhere(where).WithLimit(100).
			WithFields(graphql.Field{Name: "_additional", Fields: []graphql.Field{{Name: "id"}}}).
			Do(ctx)
		if err != nil {
			return err
		}
		if result == nil || len(result.Errors) != 0 {
			return fmt.Errorf("failed to list move indices")
		}
		get, ok := result.Data["Get"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid move index response")
		}
		rows, ok := get[collection].([]interface{})
		if !ok {
			return fmt.Errorf("invalid move index collection")
		}
		if len(rows) == 0 {
			return nil
		}
		var ids []string
		for _, row := range rows {
			data, ok := row.(map[string]interface{})
			if !ok {
				return fmt.Errorf("invalid move index row")
			}
			additional, ok := data["_additional"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("missing move index ID")
			}
			id, ok := additional["id"].(string)
			if !ok || id == "" {
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
			if err := w.client.Data().Updater().WithClassName(collection).WithID(id).WithMerge().
				WithProperties(map[string]interface{}{fieldKnowledgeBaseID: targetKB, fieldTagID: ""}).
				Do(ctx); err != nil {
				return err
			}
		}
	}
}
