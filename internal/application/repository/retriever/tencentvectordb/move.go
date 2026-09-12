// Package tencentvectordb implements retrieval against its configured vector backend.
package tencentvectordb

import (
	"context"

	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

func (r *repository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	_ string,
) error {
	filter := tcvectordb.NewFilter(
		tcvectordb.In(
			fieldKnowledgeBaseID,
			[]string{sourceKB},
		) + " and " + tcvectordb.In(
			fieldKnowledgeID,
			[]string{knowledgeID},
		),
	)
	_, err := r.client.Database(r.databaseName).
		Collection(r.collectionName(dimension)).
		Update(ctx, tcvectordb.UpdateDocumentParams{
			QueryFilter:  filter,
			UpdateFields: map[string]tcvectordb.Field{fieldKnowledgeBaseID: {Val: targetKB}, fieldTagID: {Val: ""}},
		})
	return err
}
