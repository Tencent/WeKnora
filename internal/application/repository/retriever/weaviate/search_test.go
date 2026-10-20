package weaviate

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/weaviate/weaviate/grpc/generated/protocol/v1"
)

const (
	testChunkA = "11111111-1111-1111-1111-111111111111"
	testChunkB = "22222222-2222-2222-2222-222222222222"
)

// classFixture is a class as Weaviate 1.40.1 returns it from /v1/schema. The
// client decodes the full configuration when it lists collections, so the fake
// has to answer with a complete one.
var classFixture = func() []byte {
	b, err := os.ReadFile("testdata/class_v1.40.1.json")
	if err != nil {
		panic(err)
	}
	return b
}()

func fakeClass(name string) map[string]any {
	var class map[string]any
	if err := json.Unmarshal(classFixture, &class); err != nil {
		panic(err)
	}
	class["class"] = name
	return class
}

// schemaExists answers every collection lookup with "exists" and lists the
// given collections.
func schemaExists(names ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/schema" {
			classes := make([]map[string]any, 0, len(names))
			for _, name := range names {
				classes = append(classes, fakeClass(name))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"classes": classes})
			return
		}
		_ = json.NewEncoder(w).Encode(fakeClass(path.Base(r.URL.Path)))
	}
}

// Vector search goes over gRPC against the "embedding" named vector, with the
// caller's cosine threshold converted to Weaviate's certainty and every filter
// WeKnora knows about.
func TestVectorRetrieveSendsTheSearchWeaviateExpects(t *testing.T) {
	var got *pb.SearchRequest
	fake := &fakeWeaviate{
		rest: schemaExists(),
		search: func(req *pb.SearchRequest) (*pb.SearchReply, error) {
			got = req
			return searchReply(fakeRow{
				id:        testChunkA,
				certainty: float32Ptr(0.9),
				properties: map[string]any{
					fieldContent: "hello", fieldChunkID: testChunkA, fieldKnowledgeID: "k1",
					fieldKnowledgeBaseID: "kb1", fieldSourceID: testChunkA, fieldSourceType: 0, fieldTagID: "t1",
				},
			}), nil
		},
	}
	repo := fake.repository(t, "Weknora_embeddings")

	results, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding:           []float32{0.6, 0.8},
		TopK:                7,
		Threshold:           0.5,
		KnowledgeBaseIDs:    []string{"kb1", "kb2"},
		KnowledgeIDs:        []string{"k1"},
		TagIDs:              []string{"t1"},
		ExcludeKnowledgeIDs: []string{"k9", "k8"},
		ExcludeChunkIDs:     []string{testChunkB},
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, "Weknora_embeddings_2", got.GetCollection())
	assert.EqualValues(t, 7, got.GetLimit())
	require.NotNil(t, got.GetNearVector())
	assert.InDelta(t, 0.75, got.GetNearVector().GetCertainty(), 1e-9,
		"a cosine threshold of 0.5 is a certainty of 0.75")
	assert.True(t, got.GetMetadata().GetCertainty(), "the score is read from certainty")
	assert.True(t, nearVectorTargets(got.GetNearVector(), fieldEmbedding),
		"the search must target the embedding vector")

	filters := got.GetFilters()
	assert.Equal(t, pb.Filters_OPERATOR_AND, filters.GetOperator())
	enabled := filterCondition(filters, fieldIsEnabled)
	require.NotNil(t, enabled)
	assert.True(t, enabled.GetValueBoolean())
	assert.Equal(t, []string{"kb1", "kb2"}, filterTexts(filterCondition(filters, fieldKnowledgeBaseID)))
	assert.Equal(t, []string{"t1"}, filterTexts(filterCondition(filters, fieldTagID)))
	exclusions := map[string][]string{}
	for _, sub := range filters.GetFilters() {
		if sub.GetOperator() == pb.Filters_OPERATOR_CONTAINS_NONE {
			exclusions[sub.GetTarget().GetProperty()] = filterTexts(sub)
		}
	}
	assert.Equal(t, map[string][]string{
		fieldKnowledgeID: {"k9", "k8"},
		fieldChunkID:     {testChunkB},
	}, exclusions, "every excluded ID must be left out")

	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 1)
	hit := results[0].Results[0]
	assert.Equal(t, testChunkA, hit.ID)
	assert.Equal(t, "hello", hit.Content)
	assert.Equal(t, "kb1", hit.KnowledgeBaseID)
	assert.InDelta(t, 0.8, hit.Score, 1e-6, "certainty 0.9 is cosine 0.8")
	assert.Equal(t, types.MatchTypeEmbedding, hit.MatchType)
}

// A zero threshold means no cutoff, not a certainty of 0.5.
func TestVectorRetrieveWithoutThresholdSendsNoCutoff(t *testing.T) {
	var got *pb.SearchRequest
	fake := &fakeWeaviate{
		rest: schemaExists(),
		search: func(req *pb.SearchRequest) (*pb.SearchReply, error) {
			got = req
			return &pb.SearchReply{}, nil
		},
	}
	repo := fake.repository(t, "Weknora_embeddings")

	_, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{Embedding: []float32{1, 0}, TopK: 3})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.GetNearVector().Certainty)
	assert.Nil(t, got.GetNearVector().Distance)
}

func nearVectorTargets(nv *pb.NearVector, name string) bool {
	for _, target := range nv.GetVectorForTargets() {
		if target.GetName() == name {
			return true
		}
	}
	for _, target := range nv.GetTargets().GetTargetVectors() {
		if target == name {
			return true
		}
	}
	return false
}

// Keyword search runs BM25 over content in every WeKnora collection, and skips
// collections that belong to someone else.
func TestKeywordsRetrieveSearchesEveryWeKnoraCollection(t *testing.T) {
	var mu sync.Mutex
	var searched []string
	fake := &fakeWeaviate{
		rest: schemaExists("Weknora_embeddings_2", "Weknora_embeddings_3", "SomebodyElse"),
		search: func(req *pb.SearchRequest) (*pb.SearchReply, error) {
			mu.Lock()
			searched = append(searched, req.GetCollection())
			mu.Unlock()
			assert.Equal(t, "hello world", req.GetBm25Search().GetQuery())
			assert.Equal(t, []string{fieldContent}, req.GetBm25Search().GetProperties())
			assert.NotNil(t, filterCondition(req.GetFilters(), fieldIsEnabled))
			return searchReply(fakeRow{
				id:         testChunkA,
				score:      float32Ptr(3.2),
				properties: map[string]any{fieldChunkID: testChunkA, fieldContent: "hello world"},
			}), nil
		},
	}
	repo := fake.repository(t, "Weknora_embeddings")

	results, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{Query: "hello world", TopK: 5})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Weknora_embeddings_2", "Weknora_embeddings_3"}, searched)
	require.Len(t, results[0].Results, 2)
	assert.Equal(t, 1.0, results[0].Results[0].Score)
	assert.Equal(t, types.MatchTypeKeywords, results[0].Results[0].MatchType)
}

// Objects the server refuses come back next to a successful call; a batch that
// drops some of them must not be reported as saved.
func TestBatchSaveReportsRefusedObjects(t *testing.T) {
	var got *pb.BatchObjectsRequest
	fake := &fakeWeaviate{
		rest: schemaExists(),
		batchObjects: func(req *pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error) {
			got = req
			return &pb.BatchObjectsReply{Errors: []*pb.BatchObjectsReply_BatchError{
				{Index: 1, Error: "id already exists"},
			}}, nil
		},
	}
	repo := fake.repository(t, "Weknora_embeddings")

	embeddings := map[string][]float32{testChunkA: {1, 0}, testChunkB: {0, 1}}
	err := repo.BatchSave(context.Background(), []*types.IndexInfo{
		{ChunkID: testChunkA, SourceID: testChunkA, Content: "a", IsEnabled: true},
		{ChunkID: testChunkB, SourceID: testChunkB, Content: "b", IsEnabled: true},
	}, map[string]any{fieldEmbedding: embeddings})

	require.ErrorContains(t, err, "id already exists")
	require.NotNil(t, got)
	require.Len(t, got.GetObjects(), 2)
	for _, obj := range got.GetObjects() {
		assert.Equal(t, "Weknora_embeddings_2", obj.GetCollection())
		require.Len(t, obj.GetVectors(), 1)
		assert.Equal(t, fieldEmbedding, obj.GetVectors()[0].GetName())
	}
}

// A chunk ID is the object's UUID; one that is not a UUID cannot be stored.
func TestSaveRejectsANonUUIDChunkID(t *testing.T) {
	fake := &fakeWeaviate{rest: schemaExists()}
	repo := fake.repository(t, "Weknora_embeddings")

	err := repo.Save(context.Background(), &types.IndexInfo{ChunkID: "chunk-1", SourceID: "chunk-1"},
		map[string]any{fieldEmbedding: map[string][]float32{"chunk-1": {1, 0}}})
	require.ErrorContains(t, err, "not a UUID")
}

// Deletes go over gRPC as a single ContainsAny filter on the named field.
func TestDeleteByKnowledgeIDListDeletesByFilter(t *testing.T) {
	var got *pb.BatchDeleteRequest
	fake := &fakeWeaviate{batchDelete: func(req *pb.BatchDeleteRequest) (*pb.BatchDeleteReply, error) {
		got = req
		return &pb.BatchDeleteReply{Matches: 2, Successful: 2}, nil
	}}
	repo := fake.repository(t, "Weknora_embeddings")

	require.NoError(t, repo.DeleteByKnowledgeIDList(context.Background(), []string{"k1", "k2"}, 3, "doc"))
	require.NotNil(t, got)
	assert.Equal(t, "Weknora_embeddings_3", got.GetCollection())
	assert.Equal(t, pb.Filters_OPERATOR_CONTAINS_ANY, got.GetFilters().GetOperator())
	assert.Equal(t, fieldKnowledgeID, got.GetFilters().GetTarget().GetProperty())
	assert.Equal(t, []string{"k1", "k2"}, filterTexts(got.GetFilters()))
	assert.False(t, got.GetDryRun())
}
