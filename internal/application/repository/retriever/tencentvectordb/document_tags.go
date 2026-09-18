package tencentvectordb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb/api"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb/api/index"
)

var _ interfaces.DocumentTagIndexer = (*repository)(nil)

func documentTagFilterIndex() tcvectordb.FilterIndex {
	return tcvectordb.FilterIndex{FieldName: fieldTagIDs, FieldType: tcvectordb.Array, ElemType: tcvectordb.String, IndexType: tcvectordb.FILTER}
}

func (r *repository) PrepareDocumentTagIndex(ctx context.Context) error {
	return r.checkDocumentTagIndex(ctx, true)
}

func (r *repository) checkDocumentTagIndex(ctx context.Context, create bool) error {
	collections, err := r.client.Database(r.databaseName).ListCollection(ctx)
	if err != nil {
		return err
	}
	for _, collection := range collections.Collections {
		if !r.matchesCollection(collection.CollectionName) {
			continue
		}
		description, err := r.client.Database(r.databaseName).DescribeCollection(ctx, collection.CollectionName)
		if err != nil {
			return err
		}
		found := false
		for _, field := range description.Indexes.FilterIndex {
			if field.FieldName != fieldTagIDs {
				continue
			}
			if field.FieldType != tcvectordb.Array || field.ElemType != tcvectordb.String || field.IndexType != tcvectordb.FILTER {
				return fmt.Errorf("collection %s has an incompatible %s index", collection.CollectionName, fieldTagIDs)
			}
			found = true
		}
		if !found {
			if !create {
				return fmt.Errorf("collection %s has no document tag index", collection.CollectionName)
			}
			build := true
			// SDK v1.8.4's AddIndex helper drops ElemType. Use its typed HTTP
			// request so an existing collection gets a string-array index too.
			err := r.client.Request(ctx, &index.AddReq{
				Database:         r.databaseName,
				Collection:       collection.CollectionName,
				Indexes:          []*api.IndexColumn{{FieldName: fieldTagIDs, FieldType: string(tcvectordb.Array), FieldElementType: string(tcvectordb.String), IndexType: string(tcvectordb.FILTER)}},
				BuildExistedData: &build,
			}, &index.AddRes{})
			if err != nil {
				return err
			}
			// Adding an index can be asynchronous. Recheck on the next background retry;
			// never declare existing documents ready merely because it exists.
			return fmt.Errorf("collection %s document tag index is being built", collection.CollectionName)
		}
		if description.IndexStatus.Status != "ready" {
			return fmt.Errorf("collection %s index status: %s", collection.CollectionName, description.IndexStatus.Status)
		}
	}
	return nil
}

func (r *repository) UpdateDocumentTags(ctx context.Context, kbID string, tags map[string][]string) error {
	collections, err := r.client.Database(r.databaseName).ListCollection(ctx)
	if err != nil {
		return err
	}
	// One metadata update can cover many documents with the same tag set.
	// Keep each filter bounded, including when callers supply a large batch.
	type tagGroup struct {
		tags      []string
		documents []string
	}
	groups := make(map[string]*tagGroup)
	for id, ids := range tags {
		values := append([]string{}, ids...)
		sort.Strings(values)
		key, _ := json.Marshal(values) // []string is always JSON-encodable.
		group := groups[string(key)]
		if group == nil {
			group = &tagGroup{tags: values}
			groups[string(key)] = group
		}
		group.documents = append(group.documents, id)
	}
	for _, collection := range collections.Collections {
		if !r.matchesCollection(collection.CollectionName) {
			continue
		}
		for _, group := range groups {
			if err := r.updateDocumentTagGroup(ctx, collection.CollectionName, kbID, group.documents, group.tags); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *repository) updateDocumentTagGroup(ctx context.Context, collection, kbID string, ids, tags []string) error {
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		filter := tcvectordb.In(fieldKnowledgeBaseID, []string{kbID}) + " and " + tcvectordb.In(fieldKnowledgeID, batch)
		_, err := r.client.Database(r.databaseName).Collection(collection).Update(ctx, tcvectordb.UpdateDocumentParams{
			QueryFilter:  tcvectordb.NewFilter(filter),
			UpdateFields: map[string]tcvectordb.Field{fieldTagIDs: {Val: tags}},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// The SDK Include helper does not quote string contents. Tags come from an
// HTTP request, so escape each value before composing the filter expression.
func documentTagCondition(ids []string) string {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.Quote(id)
	}
	return fieldTagIDs + " include (" + strings.Join(values, ",") + ")"
}
