package weaviate

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate/entities/models"
)

const identityCollection = "Identity_3"

// identityStore implements the REST contract through the real Weaviate SDK.
// Batch writes replace objects with the same UUID; a single create rejects an
// existing UUID. These are the server's semantics, not a count of request rows.
type identityStore struct {
	mu         sync.Mutex
	objects    map[string]models.Object
	failBatch  bool
	failMerge  bool
	queryError string
	queryLimit int
}

func (s *identityStore) repository(t *testing.T) *weaviateRepository {
	t.Helper()
	s.objects = make(map[string]models.Object)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/meta":
			_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/schema":
			_ = json.NewEncoder(w).Encode(map[string]any{"classes": []any{map[string]any{"class": identityCollection}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/schema/"+identityCollection:
			_ = json.NewEncoder(w).Encode(map[string]any{"class": identityCollection})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/objects":
			var object models.Object
			require.NoError(t, json.NewDecoder(r.Body).Decode(&object))
			if _, exists := s.objects[object.ID.String()]; exists {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(weaviateErrorBody("id already exists")))
				return
			}
			if !s.put(t, object) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			_ = json.NewEncoder(w).Encode(object)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batch/objects":
			var body struct {
				Objects []models.Object `json:"objects"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			results := make([]any, 0, len(body.Objects))
			for _, object := range body.Objects {
				if !s.put(t, object) {
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				if s.failBatch {
					s.failBatch = false // one object persisted before the request failed
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				results = append(results, map[string]any{
					"id": object.ID, "result": map[string]any{"status": "SUCCESS"},
				})
			}
			_ = json.NewEncoder(w).Encode(results)
		case r.Method == http.MethodPatch || r.Method == http.MethodPut:
			if s.failMerge {
				s.failMerge = false
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			id := path.Base(r.URL.Path)
			object, exists := s.objects[id]
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var update models.Object
			require.NoError(t, json.NewDecoder(r.Body).Decode(&update))
			if r.Method == http.MethodPut {
				object = update // a replacement must not masquerade as a merge
			} else {
				maps.Copy(object.Properties.(map[string]any), update.Properties.(map[string]any))
			}
			s.objects[id] = object
			if r.Method == http.MethodPut {
				_ = json.NewEncoder(w).Encode(object)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/batch/objects":
			var body struct {
				Match struct {
					Where struct {
						Path      []string `json:"path"`
						ValueText []string `json:"valueTextArray"`
					} `json:"where"`
				} `json:"match"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Len(t, body.Match.Where.Path, 1)
			for id, object := range s.objects {
				value, _ := object.Properties.(map[string]any)[body.Match.Where.Path[0]].(string)
				if slices.Contains(body.Match.Where.ValueText, value) {
					delete(s.objects, id)
				}
			}
			_, _ = w.Write([]byte(`{"results":{"successful":1,"failed":0}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
			var body struct {
				Query string `json:"query"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if s.queryError != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": s.queryError}}})
				return
			}
			limit := 10000
			if match := regexp.MustCompile(`limit:\s*(\d+)`).FindStringSubmatch(body.Query); match != nil {
				_, _ = fmt.Sscanf(match[1], "%d", &limit)
			}
			if s.queryLimit > 0 && limit > s.queryLimit {
				message := fmt.Sprintf("query exceeds QUERY_MAXIMUM_RESULTS '%d'", s.queryLimit)
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": message}}})
				return
			}
			rows := make([]any, 0)
			for _, id := range slices.Sorted(maps.Keys(s.objects)) {
				object := s.objects[id]
				props := object.Properties.(map[string]any)
				if strings.Contains(body.Query, `path: ["chunk_id"]`) &&
					!strings.Contains(body.Query, props[fieldChunkID].(string)) {
					continue
				}
				if strings.Contains(body.Query, "nearVector:") {
					vectorJSON, _ := json.Marshal(object.Vector)
					if props[fieldIsEnabled] != true ||
						!strings.Contains(strings.ReplaceAll(body.Query, " ", ""), string(vectorJSON)) {
						continue
					}
				}
				row := maps.Clone(props)
				row["_additional"] = map[string]any{"id": id, "certainty": 1.0}
				rows = append(rows, row)
				if len(rows) == limit {
					break
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"Get": map[string]any{identityCollection: rows}},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	return &weaviateRepository{client: client, collectionBaseName: "Identity"}
}

func (s *identityStore) put(t *testing.T, object models.Object) bool {
	t.Helper()
	if _, err := uuid.Parse(object.ID.String()); !assert.NoError(t, err, "Weaviate requires UUID object IDs") {
		return false
	}
	s.objects[object.ID.String()] = object
	return true
}

func (s *identityStore) snapshot() map[string]models.Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := maps.Clone(s.objects)
	for id, object := range objects {
		object.Properties = maps.Clone(object.Properties.(map[string]any))
		objects[id] = object
	}
	return objects
}

func identityInputs() ([]*types.IndexInfo, map[string]any) {
	chunkID := "b69b5884-742c-4b23-a417-7cebc92b8bd4"
	rows := []*types.IndexInfo{
		{SourceID: chunkID, Content: "The document body"},
		{
			SourceID: types.GeneratedQuestionSourceID(chunkID, "q1730000000000000000"),
			Content:  "How does indexing work?",
		},
		{
			SourceID: types.GeneratedQuestionSourceID(chunkID, "a030d6c4-c96b-4ae7-b9f5-378afc572b72"),
			Content:  "How do I retrieve a document?",
		},
	}
	embeddings := make(map[string][]float32)
	for i, row := range rows {
		row.ChunkID, row.KnowledgeID, row.KnowledgeBaseID = chunkID, "knowledge-1", "kb-1"
		row.SourceType, row.IsEnabled = types.ChunkSourceType, true
		vector := make([]float32, 3)
		vector[i] = 1
		embeddings[row.SourceID] = vector
	}
	return rows, map[string]any{fieldEmbedding: embeddings}
}

func TestBatchSavePreservesBodyAndGeneratedQuestionVectors(t *testing.T) {
	store := &identityStore{}
	repo := store.repository(t)
	rows, params := identityInputs()
	require.NoError(t, repo.BatchSave(context.Background(), rows, params))
	require.Len(t, store.snapshot(), 3, "one chunk has a body and two independent retrieval sources")
	require.Contains(t, store.snapshot(), rows[0].ChunkID, "keep existing body IDs")
	ids := slices.Sorted(maps.Keys(store.snapshot()))
	for _, row := range rows {
		vector := params[fieldEmbedding].(map[string][]float32)[row.SourceID]
		results, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
			Embedding: vector, TopK: 1, KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		require.Len(t, results[0].Results, 1, "each source vector must remain searchable")
		assert.Equal(t, row.SourceID, results[0].Results[0].SourceID)
		assert.Equal(t, row.Content, results[0].Results[0].Content)
		assert.Equal(t, row.ChunkID, results[0].Results[0].ChunkID)
	}
	// A retry after an interrupted write targets the same UUIDs.
	store.mu.Lock()
	store.failBatch = true
	store.mu.Unlock()
	require.Error(t, repo.BatchSave(context.Background(), rows, params))
	require.NoError(t, repo.BatchSave(context.Background(), rows, params))
	assert.Equal(t, ids, slices.Sorted(maps.Keys(store.snapshot())), "batch retries reuse the original UUIDs")
}

func TestSavePreservesGeneratedQuestionsAndRejectsDuplicateSource(t *testing.T) {
	store := &identityStore{}
	repo := store.repository(t)
	rows, params := identityInputs()
	for _, row := range rows {
		require.NoError(t, repo.Save(context.Background(), row, params))
	}
	require.Len(t, store.snapshot(), 3)
	require.Error(t, repo.Save(context.Background(), rows[1], params), "a retried create must not add a duplicate")
	require.Len(t, store.snapshot(), 3)
}

func TestSaveKeepsLegacyBodyObjectIDs(t *testing.T) {
	for _, sourceID := range []string{"", "b69b5884-742c-4b23-a417-7cebc92b8bd4"} {
		t.Run(sourceID, func(t *testing.T) {
			store := &identityStore{}
			repo := store.repository(t)
			rows, _ := identityInputs()
			rows[0].SourceID = sourceID
			params := map[string]any{fieldEmbedding: map[string][]float32{sourceID: {1, 0, 0}}}
			require.NoError(t, repo.Save(context.Background(), rows[0], params))
			require.Contains(t, store.snapshot(), rows[0].ChunkID)
		})
	}
}

func TestBatchSaveScopesSourceIdentityToItsChunk(t *testing.T) {
	store := &identityStore{}
	repo := store.repository(t)
	rows, _ := identityInputs()
	first, second := *rows[0], *rows[0]
	first.SourceID, second.SourceID = "external-record-1", "external-record-1"
	second.ChunkID = "64038c31-8124-4dc9-8ed6-934c0ce640d0"
	params := map[string]any{fieldEmbedding: map[string][]float32{first.SourceID: {1, 0, 0}}}
	require.NoError(t, repo.BatchSave(context.Background(), []*types.IndexInfo{&first, &second}, params))
	objects := store.snapshot()
	require.Len(t, objects, 2, "an external source ID must not overwrite a different chunk")
	chunks := make([]string, 0, 2)
	for _, object := range objects {
		props := object.Properties.(map[string]any)
		chunks = append(chunks, props[fieldChunkID].(string))
	}
	assert.ElementsMatch(t, []string{first.ChunkID, second.ChunkID}, chunks)
}

func TestChunkMetadataAndDeletionApplyToEveryRetrievalSource(t *testing.T) {
	store := &identityStore{}
	repo := store.repository(t)
	rows, params := identityInputs()
	ctx := context.Background()
	other := *rows[0]
	other.ChunkID = "64038c31-8124-4dc9-8ed6-934c0ce640d0"
	other.SourceID, other.Content = other.ChunkID, "An unrelated chunk"
	params[fieldEmbedding].(map[string][]float32)[other.SourceID] = []float32{0.5, 0.5, 0.5}
	require.NoError(t, repo.BatchSave(ctx, append(slices.Clone(rows), &other), params))
	require.NoError(t, repo.BatchUpdateChunkTagID(ctx, map[string]string{rows[0].ChunkID: "tag-new"}))
	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{rows[0].ChunkID: false}))
	objects := store.snapshot()
	require.Len(t, objects, 4)
	for _, row := range rows {
		var object models.Object
		for _, candidate := range objects {
			if candidate.Properties.(map[string]any)[fieldSourceID] == row.SourceID {
				object = candidate
				break
			}
		}
		require.NotNil(t, object.Properties, "each retrieval source must still exist")
		props := object.Properties.(map[string]any)
		assert.Equal(t, false, props[fieldIsEnabled])
		assert.Equal(t, "tag-new", props[fieldTagID])
		assert.Equal(t, row.Content, props[fieldContent])
		assert.Equal(t, row.KnowledgeBaseID, props[fieldKnowledgeBaseID])
		assert.Equal(t, params[fieldEmbedding].(map[string][]float32)[row.SourceID], []float32(object.Vector))
		results, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding: []float32(object.Vector), TopK: 1, KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		assert.Empty(t, results[0].Results, "disabled questions must no longer recall the chunk")
	}
	assert.Equal(t, true, objects[other.ChunkID].Properties.(map[string]any)[fieldIsEnabled])
	assert.Equal(t, "", objects[other.ChunkID].Properties.(map[string]any)[fieldTagID])
	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{rows[0].ChunkID: true}))
	require.NoError(t, repo.DeleteBySourceIDList(ctx, []string{rows[1].SourceID}, 3, ""))
	require.Len(t, store.snapshot(), 3, "deleting one question preserves its body and sibling")
	require.Contains(t, store.snapshot(), rows[0].ChunkID)
	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{rows[0].ChunkID}, 3, ""))
	require.Len(t, store.snapshot(), 1, "deleting the chunk removes every retrieval source")
	require.Contains(t, store.snapshot(), other.ChunkID, "deleting a chunk preserves unrelated chunks")
}

func TestChunkMetadataUpdateFailuresAreReturned(t *testing.T) {
	for _, failure := range []string{"query", "merge", "truncated"} {
		t.Run(failure, func(t *testing.T) {
			store := &identityStore{}
			repo := store.repository(t)
			rows, params := identityInputs()
			require.NoError(t, repo.BatchSave(context.Background(), rows, params))
			store.mu.Lock()
			switch failure {
			case "query":
				store.queryError = "index unavailable"
			case "merge":
				store.failMerge = true
			case "truncated":
				store.queryLimit = 2
			}
			store.mu.Unlock()
			err := repo.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{rows[0].ChunkID: false})
			require.Error(t, err)
			store.mu.Lock()
			store.queryError, store.queryLimit = "", 0
			store.mu.Unlock()
			require.NoError(t, repo.BatchUpdateChunkEnabledStatus(
				context.Background(), map[string]bool{rows[0].ChunkID: false},
			))
			for _, object := range store.snapshot() {
				assert.Equal(t, false, object.Properties.(map[string]any)[fieldIsEnabled])
			}
		})
	}
}
