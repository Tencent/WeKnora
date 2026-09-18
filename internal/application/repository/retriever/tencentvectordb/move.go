// Package tencentvectordb implements retrieval against its configured vector backend.
package tencentvectordb

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

func (r *repository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	dimension int,
	knowledgeType string,
) error {
	fields := map[string]tcvectordb.Field{fieldKnowledgeBaseID: {Val: targetKB}, fieldTagID: {Val: ""}}
	if knowledgeType != types.KnowledgeTypeFAQ {
		if err := r.PrepareDocumentTagIndex(ctx); err != nil {
			return err
		}
		// Relations are cleared after moving the index, while SQL still points
		// at the source KB. Clear KB-scoped tags in this same metadata update.
		fields[fieldTagIDs] = tcvectordb.Field{Val: []string{}}
	}
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
			UpdateFields: fields,
		})
	return err
}
