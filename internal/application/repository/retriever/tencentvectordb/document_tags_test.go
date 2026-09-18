package tencentvectordb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb/api/index"
)

type tagClient struct {
	tcvectordb.DatabaseInterface
	collections *tagCollections
	add         *index.AddReq
	addErr      error
}

func (c *tagClient) Database(string) *tcvectordb.Database {
	return &tcvectordb.Database{CollectionInterface: c.collections}
}
func (c *tagClient) Request(_ context.Context, request, _ interface{}) error {
	c.add = request.(*index.AddReq)
	return c.addErr
}

type tagCollections struct {
	tcvectordb.CollectionInterface
	names       []string
	description tcvectordb.DescribeCollectionResult
	documents   *tagDocuments
}

func (c *tagCollections) ListCollection(context.Context) (*tcvectordb.ListCollectionResult, error) {
	result := &tcvectordb.ListCollectionResult{}
	for _, name := range c.names {
		result.Collections = append(result.Collections, &tcvectordb.Collection{CollectionName: name})
	}
	return result, nil
}
func (c *tagCollections) DescribeCollection(context.Context, string) (*tcvectordb.DescribeCollectionResult, error) {
	return &c.description, nil
}
func (c *tagCollections) Collection(name string) *tcvectordb.Collection {
	c.documents.names = append(c.documents.names, name)
	return &tcvectordb.Collection{DocumentInterface: c.documents}
}

type tagDocuments struct {
	tcvectordb.DocumentInterface
	updates []tcvectordb.UpdateDocumentParams
	names   []string
	err     error
}

func (d *tagDocuments) Update(_ context.Context, params tcvectordb.UpdateDocumentParams) (*tcvectordb.UpdateDocumentResult, error) {
	d.updates = append(d.updates, params)
	return &tcvectordb.UpdateDocumentResult{}, d.err
}

func newTagTestRepository() (*repository, *tagClient) {
	c := &tagClient{collections: &tagCollections{names: []string{"embeddings_3", "unrelated"}, documents: &tagDocuments{}}}
	c.collections.description.Indexes.FilterIndex = []tcvectordb.FilterIndex{documentTagFilterIndex()}
	c.collections.description.IndexStatus.Status = "ready"
	return &repository{client: c, databaseName: "db", collectionBaseName: "embeddings", useDimensionSuffix: true}, c
}

func TestDocumentTagsPreserveFAQAndFilterBothRetrievalModes(t *testing.T) {
	r, _ := newTagTestRepository()
	for _, mode := range []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType} {
		params := types.RetrieveParams{KnowledgeBaseIDs: []string{"kb"}, KnowledgeIDs: []string{"doc"}, TagIDs: []string{"alpha", "beta"}, RetrieverType: mode}
		condition := r.baseFilter(params).Cond()
		assert.Contains(t, condition, `tag_ids include ("alpha","beta")`)
		assert.Contains(t, condition, `knowledge_base_id in ("kb")`)
		assert.Contains(t, condition, `knowledge_id in ("doc")`)
		params.KnowledgeType = types.KnowledgeTypeFAQ
		assert.Contains(t, r.baseFilter(params).Cond(), `tag_id in ("alpha","beta")`)
		assert.NotContains(t, r.baseFilter(params).Cond(), "tag_ids")
	}
	assert.Equal(t, `tag_ids include ("a\" or 1=1","b\\c")`, documentTagCondition([]string{`a" or 1=1`, `b\c`}))
}

func TestDocumentTagsAreWrittenAsAnArrayIncludingEmpty(t *testing.T) {
	for _, tags := range [][]string{nil, {"alpha", "beta"}} {
		info := &types.IndexInfo{TagIDs: tags}
		doc := toDocument(toVectorEmbedding(info, nil))
		array, ok := doc.Fields[fieldTagIDs].Val.([]string)
		require.True(t, ok)
		require.NotNil(t, array, "empty tags must serialize as [] rather than null")
		assert.ElementsMatch(t, tags, array)
	}
}

func TestPrepareDocumentTagIndexRequiresUsableSchema(t *testing.T) {
	r, c := newTagTestRepository()
	require.NoError(t, r.PrepareDocumentTagIndex(context.Background()))
	assert.Nil(t, c.add)
	c.collections.description.Indexes.FilterIndex = nil
	require.Error(t, r.PrepareDocumentTagIndex(context.Background()))
	require.NotNil(t, c.add)
	assert.Equal(t, "embeddings_3", c.add.Collection)
	require.Len(t, c.add.Indexes, 1)
	assert.Equal(t, string(tcvectordb.Array), c.add.Indexes[0].FieldType)
	assert.Equal(t, string(tcvectordb.String), c.add.Indexes[0].FieldElementType)
	assert.True(t, *c.add.BuildExistedData)
	c.addErr = errors.New("server does not support array filter")
	require.Error(t, r.PrepareDocumentTagIndex(context.Background()))
	c.collections.description.Indexes.FilterIndex = []tcvectordb.FilterIndex{documentTagFilterIndex()}
	c.collections.description.IndexStatus.Status = "building"
	require.Error(t, r.PrepareDocumentTagIndex(context.Background()))
	c.collections.description.IndexStatus.Status = "ready"
	c.collections.description.Indexes.FilterIndex[0].FieldType = tcvectordb.String
	require.Error(t, r.PrepareDocumentTagIndex(context.Background()))
}

func TestDocumentTagQueryNeverCreatesSchema(t *testing.T) {
	r, c := newTagTestRepository()
	c.collections.description.Indexes.FilterIndex = nil
	_, err := r.Retrieve(context.Background(), types.RetrieveParams{TagIDs: []string{"tag"}, RetrieverType: types.VectorRetrieverType})
	require.Error(t, err)
	assert.Nil(t, c.add, "queries only validate; schema creation belongs to writes/workers")
	_, err = r.Retrieve(context.Background(), types.RetrieveParams{KnowledgeType: types.KnowledgeTypeFAQ, TagIDs: []string{"tag"}, RetrieverType: types.VectorRetrieverType})
	require.NoError(t, err, "FAQ does not require the document tag array schema")
}

func TestDocumentTagMetadataUpdateIsScopedAndDoesNotEmbed(t *testing.T) {
	r, c := newTagTestRepository()
	require.NoError(t, r.UpdateDocumentTags(context.Background(), "kb", map[string][]string{"doc-a": {"a", "b"}, "doc-b": {}}))
	require.Len(t, c.collections.documents.updates, 2)
	for _, update := range c.collections.documents.updates {
		assert.Contains(t, update.QueryFilter.Cond(), `knowledge_base_id in ("kb")`)
		assert.Contains(t, update.QueryFilter.Cond(), "knowledge_id in")
		assert.Empty(t, update.UpdateVector)
		assert.Empty(t, update.UpdateSparseVec)
		fields := update.UpdateFields.(map[string]tcvectordb.Field)
		assert.Len(t, fields, 1)
		assert.Contains(t, fields, fieldTagIDs)
		assert.NotNil(t, fields[fieldTagIDs].Val)
	}
	assert.Equal(t, []string{"embeddings_3", "embeddings_3"}, c.collections.documents.names)
	c.collections.documents.err = errors.New("partial update")
	require.Error(t, r.UpdateDocumentTags(context.Background(), "kb", map[string][]string{"doc": {"a"}}))
}

func TestDocumentTagMetadataUpdateBoundsFilterSize(t *testing.T) {
	r, c := newTagTestRepository()
	tags := make(map[string][]string)
	for i := range 205 {
		tags[fmt.Sprintf("doc-%03d", i)] = []string{"shared"}
	}
	require.NoError(t, r.UpdateDocumentTags(context.Background(), "kb", tags))
	require.Len(t, c.collections.documents.updates, 3)
	for _, update := range c.collections.documents.updates {
		assert.LessOrEqual(t, strings.Count(update.QueryFilter.Cond(), "doc-"), 100)
	}
}

func TestMoveClearsDocumentTagsAlongWithTheSourceKB(t *testing.T) {
	r, c := newTagTestRepository()
	require.NoError(t, r.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, types.KnowledgeBaseTypeDocument))
	require.Len(t, c.collections.documents.updates, 1)
	update := c.collections.documents.updates[0]
	assert.Contains(t, update.QueryFilter.Cond(), `knowledge_base_id in ("source")`)
	assert.Contains(t, update.QueryFilter.Cond(), `knowledge_id in ("doc")`)
	fields := update.UpdateFields.(map[string]tcvectordb.Field)
	assert.Equal(t, "target", fields[fieldKnowledgeBaseID].Val)
	assert.Equal(t, []string{}, fields[fieldTagIDs].Val, "source tags must not survive relocation")
	assert.Empty(t, update.UpdateVector)

	c.collections.description.Indexes.FilterIndex = nil
	require.Error(t, r.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, types.KnowledgeBaseTypeDocument))
	assert.Len(t, c.collections.documents.updates, 1, "do not relocate before the tag schema is usable")
	require.NoError(t, r.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, types.KnowledgeTypeFAQ))
	require.Len(t, c.collections.documents.updates, 2)
	assert.NotContains(t, c.collections.documents.updates[1].UpdateFields, fieldTagIDs, "FAQ retains its scalar-tag path")
}
