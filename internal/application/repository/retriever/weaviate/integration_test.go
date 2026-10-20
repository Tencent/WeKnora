//go:build weaviate_integration

// Integration tests against a real Weaviate. They are opt-in:
//
//	WEAVIATE_INTEGRATION_HOST=localhost:8080 \
//	WEAVIATE_INTEGRATION_GRPC=localhost:50051 \
//	go test -tags weaviate_integration ./internal/application/repository/retriever/weaviate/
//
// The server needs ENABLE_TOKENIZER_GSE=true, as every WeKnora deployment does.
// Set WEAVIATE_INTEGRATION_DEBUG=localhost:6060 (with DEBUG_ENDPOINTS_ENABLED=true
// on the server) to also check that the integration header reached it.
package weaviate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const integrationDimension = 4

type integrationChunk struct {
	id, knowledgeID, knowledgeBaseID, tagID, content string
	vector                                           []float32
}

func integrationRepository(t *testing.T) *weaviateRepository {
	t.Helper()
	host := os.Getenv("WEAVIATE_INTEGRATION_HOST")
	if host == "" {
		t.Skip("set WEAVIATE_INTEGRATION_HOST (and WEAVIATE_INTEGRATION_GRPC) to run against a real Weaviate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewClient(ctx, ClientConfig{
		Host:        host,
		GrpcAddress: os.Getenv("WEAVIATE_INTEGRATION_GRPC"),
		APIKey:      os.Getenv("WEAVIATE_INTEGRATION_API_KEY"),
	})
	require.NoError(t, err)

	// A base name of our own, so the test never touches anyone else's data.
	base := fmt.Sprintf("WeknoraIT%08x", rand.Uint32())
	repo := NewWeaviateRetrieveEngineRepository(client, &types.IndexConfig{CollectionName: base}).(*weaviateRepository)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		names, err := repo.ListCollections(ctx)
		if err == nil {
			for _, name := range names {
				if strings.HasPrefix(name, base) {
					_ = client.Collections.Delete(ctx, name)
				}
			}
		}
		_ = client.Close()
	})
	return repo
}

func integrationSave(t *testing.T, repo *weaviateRepository, chunks ...integrationChunk) {
	t.Helper()
	infos := make([]*types.IndexInfo, 0, len(chunks))
	vectors := make(map[string][]float32, len(chunks))
	for _, c := range chunks {
		infos = append(infos, &types.IndexInfo{
			ChunkID:         c.id,
			SourceID:        c.id,
			SourceType:      types.ChunkSourceType,
			KnowledgeID:     c.knowledgeID,
			KnowledgeBaseID: c.knowledgeBaseID,
			TagID:           c.tagID,
			Content:         c.content,
			IsEnabled:       true,
		})
		vectors[c.id] = c.vector
	}
	require.NoError(t, repo.BatchSave(context.Background(), infos, map[string]any{fieldEmbedding: vectors}))
}

func vectorHits(t *testing.T, repo *weaviateRepository, params types.RetrieveParams) []*types.IndexWithScore {
	t.Helper()
	params.RetrieverType = types.VectorRetrieverType
	if params.TopK == 0 {
		params.TopK = 10
	}
	results, err := repo.Retrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0].Results
}

func keywordHits(t *testing.T, repo *weaviateRepository, params types.RetrieveParams) []*types.IndexWithScore {
	t.Helper()
	params.RetrieverType = types.KeywordsRetrieverType
	if params.TopK == 0 {
		params.TopK = 10
	}
	results, err := repo.Retrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0].Results
}

func sourceIDs(hits []*types.IndexWithScore) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.SourceID)
	}
	return ids
}

func chunkIDs(hits []*types.IndexWithScore) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ChunkID)
	}
	return ids
}

// The whole lifecycle WeKnora drives through the repository, end to end on a
// real server: write, search both ways with every filter, disable, retag,
// copy, move and delete.
func TestIntegrationRepositoryLifecycle(t *testing.T) {
	repo := integrationRepository(t)
	ctx := context.Background()

	a := integrationChunk{uuid.NewString(), "k1", "kb1", "t1", "weaviate stores vectors", []float32{1, 0, 0, 0}}
	b := integrationChunk{uuid.NewString(), "k1", "kb1", "t2", "postgres stores rows", []float32{0.8, 0.6, 0, 0}}
	c := integrationChunk{uuid.NewString(), "k2", "kb1", "t1", "vectors in another document", []float32{0, 1, 0, 0}}
	d := integrationChunk{uuid.NewString(), "k3", "kb2", "", "a different knowledge base", []float32{1, 0, 0, 0}}
	integrationSave(t, repo, a, b, c)
	// Save takes the single-object path.
	require.NoError(t, repo.Save(ctx, &types.IndexInfo{
		ChunkID: d.id, SourceID: d.id, KnowledgeID: d.knowledgeID, KnowledgeBaseID: d.knowledgeBaseID,
		Content: d.content, IsEnabled: true,
	}, map[string]any{fieldEmbedding: map[string][]float32{d.id: d.vector}}))

	query := []float32{1, 0, 0, 0}

	t.Run("vector search ranks by cosine and filters", func(t *testing.T) {
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeBaseIDs: []string{"kb1"}})
		require.Equal(t, []string{a.id, b.id, c.id}, chunkIDs(hits))
		assert.InDelta(t, 1.0, hits[0].Score, 1e-4)
		assert.InDelta(t, 0.8, hits[1].Score, 1e-4, "scores are cosine similarity")
		assert.Equal(t, a.content, hits[0].Content)
		assert.Equal(t, "t1", hits[0].TagID)
		assert.Equal(t, types.ChunkSourceType, hits[0].SourceType)

		hits = vectorHits(t, repo, types.RetrieveParams{
			Embedding: query, KnowledgeBaseIDs: []string{"kb1"}, Threshold: 0.5,
		})
		assert.Equal(t, []string{a.id, b.id}, chunkIDs(hits), "cosine 0 is below a 0.5 threshold")

		hits = vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeIDs: []string{"k2", "k3"}})
		assert.ElementsMatch(t, []string{c.id, d.id}, chunkIDs(hits))

		hits = vectorHits(t, repo, types.RetrieveParams{Embedding: query, TagIDs: []string{"t2"}})
		assert.Equal(t, []string{b.id}, chunkIDs(hits))

		hits = vectorHits(t, repo, types.RetrieveParams{
			Embedding: query, ExcludeKnowledgeIDs: []string{"k2", "k3"}, ExcludeChunkIDs: []string{b.id},
		})
		assert.Equal(t, []string{a.id}, chunkIDs(hits), "every excluded knowledge and chunk ID is left out")
	})

	// gse segments text into words its dictionaries know, so the assertions
	// only rely on the chunks that contain the query word, not on what else it
	// matches. Weaviate images from 1.34.0 on fail to load the gse dictionaries
	// (the build's -trimpath breaks gse's dictionary lookup), and 1.35/1.36
	// refuse BM25 on gse with BlockMax WAND; keyword search returns nothing or
	// an error there regardless of the client.
	t.Run("keyword search", func(t *testing.T) {
		hits := keywordHits(t, repo, types.RetrieveParams{Query: "vectors", KnowledgeBaseIDs: []string{"kb1"}})
		assert.Subset(t, chunkIDs(hits), []string{a.id, c.id})
		for _, hit := range hits {
			assert.Equal(t, types.MatchTypeKeywords, hit.MatchType)
			assert.Equal(t, "kb1", hit.KnowledgeBaseID)
		}
		hits = keywordHits(t, repo, types.RetrieveParams{Query: "vectors", ExcludeKnowledgeIDs: []string{"k1", "k3"}})
		assert.Contains(t, chunkIDs(hits), c.id)
		assert.NotContains(t, chunkIDs(hits), a.id, "excluded knowledge IDs are left out")
		assert.NotContains(t, chunkIDs(hits), d.id, "excluded knowledge IDs are left out")
	})

	t.Run("disabling a chunk hides it and keeps its data", func(t *testing.T) {
		require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{a.id: false}))
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeBaseIDs: []string{"kb1"}})
		assert.NotContains(t, chunkIDs(hits), a.id)

		require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{a.id: true}))
		hits = vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeBaseIDs: []string{"kb1"}})
		require.NotEmpty(t, hits)
		assert.Equal(t, a.id, hits[0].ChunkID, "the vector must survive the update")
		assert.InDelta(t, 1.0, hits[0].Score, 1e-4)
		assert.Equal(t, a.content, hits[0].Content, "the content must survive the update")
		assert.Equal(t, "k1", hits[0].KnowledgeID)
	})

	t.Run("retagging", func(t *testing.T) {
		require.NoError(t, repo.BatchUpdateChunkTagID(ctx, map[string]string{c.id: "t9"}))
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: query, TagIDs: []string{"t9"}})
		assert.Equal(t, []string{c.id}, chunkIDs(hits))
		assert.Equal(t, c.content, hits[0].Content)
	})

	t.Run("copy into another knowledge base", func(t *testing.T) {
		targetA, targetB := uuid.NewString(), uuid.NewString()
		require.NoError(t, repo.CopyIndices(ctx, "kb1",
			map[string]string{"k1": "k1-copy"},
			map[string]string{a.id: targetA, b.id: targetB},
			"kb-copy", integrationDimension, "doc"))
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeBaseIDs: []string{"kb-copy"}})
		assert.Equal(t, []string{targetA, targetB}, chunkIDs(hits))
		assert.Equal(t, "k1-copy", hits[0].KnowledgeID)
		assert.Equal(t, a.content, hits[0].Content)
		assert.InDelta(t, 1.0, hits[0].Score, 1e-4, "the copy carries the source vector")
		hits = vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeBaseIDs: []string{"kb1"}})
		assert.Len(t, hits, 3, "the source is left in place")
	})

	t.Run("move a document to another knowledge base", func(t *testing.T) {
		require.NoError(t, repo.MoveKnowledgeIndices(ctx, "kb1", "kb-moved", "k2", nil, integrationDimension, "doc"))
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: query, KnowledgeBaseIDs: []string{"kb-moved"}})
		require.Equal(t, []string{c.id}, chunkIDs(hits))
		assert.Empty(t, hits[0].TagID, "a moved chunk loses its tag")
		assert.Equal(t, c.content, hits[0].Content)
	})

	t.Run("deletes", func(t *testing.T) {
		require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{a.id}, integrationDimension, "doc"))
		require.NoError(t, repo.DeleteBySourceIDList(ctx, []string{d.id}, integrationDimension, "doc"))
		require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{"k1-copy"}, integrationDimension, "doc"))
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: query})
		assert.ElementsMatch(t, []string{b.id, c.id}, chunkIDs(hits))
	})

	t.Run("a dimension without a collection returns nothing", func(t *testing.T) {
		hits := vectorHits(t, repo, types.RetrieveParams{Embedding: []float32{1, 0, 0}})
		assert.Empty(t, hits)
	})
}

// Workers saving the first batches of a new dimension race to create its
// collection; every one of them has to succeed.
func TestIntegrationConcurrentCollectionCreation(t *testing.T) {
	repo := integrationRepository(t)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := uuid.NewString()
			errs <- repo.BatchSave(context.Background(), []*types.IndexInfo{{
				ChunkID: id, SourceID: id, KnowledgeID: "k", KnowledgeBaseID: "kb", Content: "x", IsEnabled: true,
			}}, map[string]any{fieldEmbedding: map[string][]float32{id: {0.1, 0.2, 0.3, 0.4, 0.5}}})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
	hits := vectorHits(t, repo, types.RetrieveParams{Embedding: []float32{0.1, 0.2, 0.3, 0.4, 0.5}})
	assert.Len(t, hits, workers)
}

// The created collection must match what the v5 client used to create, so
// existing deployments and new ones index the same way.
func TestIntegrationCollectionSchema(t *testing.T) {
	repo := integrationRepository(t)
	repo.replicationFactor = 1
	require.NoError(t, repo.ensureCollection(context.Background(), integrationDimension))

	host := os.Getenv("WEAVIATE_INTEGRATION_HOST")
	resp, err := http.Get("http://" + host + "/v1/schema/" + repo.getCollectionName(integrationDimension))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var class struct {
		VectorConfig map[string]struct {
			VectorIndexType   string         `json:"vectorIndexType"`
			VectorIndexConfig map[string]any `json:"vectorIndexConfig"`
			Vectorizer        map[string]any `json:"vectorizer"`
		} `json:"vectorConfig"`
		Properties []struct {
			Name            string   `json:"name"`
			DataType        []string `json:"dataType"`
			Tokenization    string   `json:"tokenization"`
			IndexFilterable bool     `json:"indexFilterable"`
		} `json:"properties"`
		ReplicationConfig struct {
			Factor int `json:"factor"`
		} `json:"replicationConfig"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&class))

	require.Contains(t, class.VectorConfig, fieldEmbedding)
	vc := class.VectorConfig[fieldEmbedding]
	assert.Equal(t, "hnsw", vc.VectorIndexType)
	assert.Contains(t, vc.Vectorizer, "none")
	assert.Equal(t, "cosine", vc.VectorIndexConfig["distance"])
	assert.EqualValues(t, 128, vc.VectorIndexConfig["efConstruction"])
	assert.EqualValues(t, 32, vc.VectorIndexConfig["maxConnections"])
	assert.EqualValues(t, 64, vc.VectorIndexConfig["ef"])
	assert.Greater(t, vc.VectorIndexConfig["vectorCacheMaxObjects"], float64(1e6),
		"unset index fields must keep the server defaults")
	assert.Equal(t, 1, class.ReplicationConfig.Factor)

	props := map[string]string{}
	for _, p := range class.Properties {
		props[p.Name] = p.DataType[0] + "/" + p.Tokenization
		if p.Name == fieldContent {
			assert.Equal(t, "gse", p.Tokenization)
		}
		if p.Name == fieldChunkID || p.Name == fieldIsEnabled {
			assert.True(t, p.IndexFilterable)
		}
	}
	assert.Equal(t, "int/", props[fieldSourceType])
	assert.Equal(t, "boolean/", props[fieldIsEnabled])
}

// Weaviate counts integrations by the header; it has to arrive on the server.
func TestIntegrationHeaderReachesTheServer(t *testing.T) {
	debug := os.Getenv("WEAVIATE_INTEGRATION_DEBUG")
	if debug == "" {
		t.Skip("set WEAVIATE_INTEGRATION_DEBUG to the server's debug address")
	}
	previous := IntegrationVersion
	IntegrationVersion = "integration-test"
	t.Cleanup(func() { IntegrationVersion = previous })

	repo := integrationRepository(t)
	id := uuid.NewString()
	integrationSave(t, repo, integrationChunk{id, "k", "kb", "", "header check", []float32{1, 0, 0, 0}})
	vectorHits(t, repo, types.RetrieveParams{Embedding: []float32{1, 0, 0, 0}})

	resp, err := http.Get("http://" + debug + "/debug/telemetry/clients")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var usage struct {
		ClientIntegrationUsage map[string]any `json:"clientIntegrationUsage"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&usage))
	raw, _ := json.Marshal(usage.ClientIntegrationUsage)
	assert.Contains(t, string(raw), "weknora", "telemetry: %s", raw)
}

// A chunk with generated questions is stored as several objects: its body
// under the chunk ID and each question under an ID of its own.
const (
	sourcesChunkA = "aaaaaaaa-0000-4000-8000-000000000001"
	sourcesChunkB = "bbbbbbbb-0000-4000-8000-000000000002"
)

// chunkSourceRows is chunk A with two generated questions, plus chunk B on its
// own, each source on its own axis so a search for one finds exactly it.
func chunkSourceRows() ([]*types.IndexInfo, map[string][]float32) {
	q1 := types.GeneratedQuestionSourceID(sourcesChunkA, "q1")
	q2 := types.GeneratedQuestionSourceID(sourcesChunkA, "q2")
	rows := []*types.IndexInfo{
		{ChunkID: sourcesChunkA, SourceID: sourcesChunkA, Content: "vector databases store embeddings"},
		{ChunkID: sourcesChunkA, SourceID: q1, Content: "what is a vector database"},
		{ChunkID: sourcesChunkA, SourceID: q2, Content: "how are documents retrieved"},
		{ChunkID: sourcesChunkB, SourceID: sourcesChunkB, Content: "a chunk saved on its own"},
	}
	for _, row := range rows {
		row.KnowledgeID, row.KnowledgeBaseID, row.TagID = "know-src", "kb-src", "tag-old"
		row.IsEnabled, row.SourceType = true, types.ChunkSourceType
	}
	return rows, map[string][]float32{
		sourcesChunkA: {1, 0, 0, 0}, q1: {0, 1, 0, 0}, q2: {0, 0, 1, 0}, sourcesChunkB: {0, 0, 0, 1},
	}
}

// checkChunkSources drives everything WeKnora does to stored chunks against
// the rows of chunkSourceRows, however they got into the collection.
func checkChunkSources(t *testing.T, repo *weaviateRepository) {
	t.Helper()
	ctx := context.Background()
	rows, vectors := chunkSourceRows()
	inKB := func(embedding []float32) []*types.IndexWithScore {
		return vectorHits(t, repo, types.RetrieveParams{Embedding: embedding, KnowledgeBaseIDs: []string{"kb-src"}})
	}
	all := []float32{0.5, 0.5, 0.5, 0.5}
	chunkASources := []string{rows[0].SourceID, rows[1].SourceID, rows[2].SourceID}

	for _, row := range rows {
		hits := inKB(vectors[row.SourceID])
		require.Len(t, hits, 4, "every source is stored as its own object")
		assert.Equal(t, row.SourceID, hits[0].SourceID, "each source is found by its own vector")
		assert.Equal(t, row.ChunkID, hits[0].ChunkID)
		assert.Equal(t, row.Content, hits[0].Content)
		assert.Equal(t, "tag-old", hits[0].TagID)
		assert.InDelta(t, 1.0, hits[0].Score, 1e-4)
	}

	hits := keywordHits(t, repo, types.RetrieveParams{Query: "embeddings", KnowledgeBaseIDs: []string{"kb-src"}})
	assert.Contains(t, sourceIDs(hits), sourcesChunkA, "keyword search reads the stored content")

	require.NoError(t, repo.BatchUpdateChunkTagID(ctx, map[string]string{sourcesChunkA: "tag-new"}))
	hits = vectorHits(t, repo, types.RetrieveParams{Embedding: all, TagIDs: []string{"tag-new"}})
	assert.ElementsMatch(t, chunkASources, sourceIDs(hits), "retagging a chunk retags every source of it")

	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{sourcesChunkA: false}))
	assert.Equal(t, []string{sourcesChunkB}, sourceIDs(inKB(all)), "disabling a chunk hides every source of it")
	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{sourcesChunkA: true}))
	for _, row := range rows[:3] {
		hits = inKB(vectors[row.SourceID])
		require.Len(t, hits, 4)
		assert.Equal(t, row.SourceID, hits[0].SourceID)
		assert.InDelta(t, 1.0, hits[0].Score, 1e-4, "the vector must survive the updates")
		assert.Equal(t, row.Content, hits[0].Content, "the content must survive the updates")
		assert.Equal(t, "tag-new", hits[0].TagID)
	}

	// Writing the same sources again must land on the objects already there.
	require.NoError(t, repo.BatchSave(ctx, rows, map[string]any{fieldEmbedding: vectors}))
	assert.Len(t, inKB(all), 4, "saving a source again replaces it")

	targetA := uuid.NewString()
	require.NoError(t, repo.CopyIndices(ctx, "kb-src", map[string]string{"know-src": "know-copy"},
		map[string]string{sourcesChunkA: targetA}, "kb-copy", integrationDimension, "doc"))
	hits = vectorHits(t, repo, types.RetrieveParams{Embedding: all, KnowledgeBaseIDs: []string{"kb-copy"}})
	require.Len(t, hits, 3, "a copy carries the body and every question")
	for _, hit := range hits {
		assert.Equal(t, targetA, hit.ChunkID)
	}

	require.NoError(t, repo.DeleteBySourceIDList(ctx, []string{rows[1].SourceID}, integrationDimension, "doc"))
	assert.ElementsMatch(t, []string{sourcesChunkA, rows[2].SourceID, sourcesChunkB}, sourceIDs(inKB(all)),
		"deleting one question keeps its body and sibling")
	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{sourcesChunkA}, integrationDimension, "doc"))
	assert.Equal(t, []string{sourcesChunkB}, sourceIDs(inKB(all)), "deleting a chunk removes every source of it")
	require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{"know-src"}, integrationDimension, "doc"))
	assert.Empty(t, inKB(all))
}

func TestIntegrationChunkWithGeneratedQuestions(t *testing.T) {
	repo := integrationRepository(t)
	rows, vectors := chunkSourceRows()
	params := map[string]any{fieldEmbedding: vectors}
	require.NoError(t, repo.BatchSave(context.Background(), rows[:3], params))
	require.NoError(t, repo.Save(context.Background(), rows[3], params))
	checkChunkSources(t, repo)
}

// Deployments upgrading from the v5 client keep their data, so everything has
// to work on a collection the v5 client created and filled. The requests below
// are the ones it sent: the class as it defined it, and objects carrying their
// vector in the unnamed "vector" field rather than under the name v6 reads.
func TestIntegrationReadsDataWrittenByTheV5Client(t *testing.T) {
	repo := integrationRepository(t)
	collection := repo.getCollectionName(integrationDimension)
	base := "http://" + os.Getenv("WEAVIATE_INTEGRATION_HOST")
	post := func(path string, body any) {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if key := os.Getenv("WEAVIATE_INTEGRATION_API_KEY"); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		var answer bytes.Buffer
		_, _ = answer.ReadFrom(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", path, answer.String())
		assert.NotContains(t, answer.String(), `"errors"`, "%s refused an object", path)
	}

	filterable := func(name, dataType string) map[string]any {
		return map[string]any{"name": name, "dataType": []string{dataType}, "indexFilterable": true}
	}
	post("/v1/schema", map[string]any{
		"class":       collection,
		"description": "WeKnora embeddings collection with dimension 4",
		"vectorConfig": map[string]any{fieldEmbedding: map[string]any{
			"vectorIndexType": "hnsw",
			"vectorIndexConfig": map[string]any{
				"distance": "cosine", "efConstruction": 128, "maxConnections": 32, "ef": 64,
			},
			"vectorizer": map[string]any{"none": map[string]any{}},
		}},
		"properties": []map[string]any{
			{"name": fieldContent, "dataType": []string{"text"}, "tokenization": "gse"},
			{"name": fieldSourceID, "dataType": []string{"text"}},
			{"name": fieldSourceType, "dataType": []string{"int"}},
			filterable(fieldChunkID, "text"),
			filterable(fieldKnowledgeID, "text"),
			filterable(fieldKnowledgeBaseID, "text"),
			filterable(fieldTagID, "text"),
			filterable(fieldIsEnabled, "boolean"),
		},
	})

	rows, vectors := chunkSourceRows()
	objects := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		objects = append(objects, map[string]any{
			"class":      collection,
			"id":         indexObjectID(row.SourceID, row.ChunkID),
			"properties": createPayload(toWeaviateVectorEmbedding(row, nil)),
			"vector":     vectors[row.SourceID],
		})
	}
	post("/v1/batch/objects", map[string]any{"objects": objects[:3]})
	post("/v1/objects", objects[3])

	checkChunkSources(t, repo)
}
