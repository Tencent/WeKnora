package weaviate

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"path"
	"slices"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/weaviate/weaviate/grpc/generated/protocol/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const identityCollection = "Identity_3"

type identityObject struct {
	properties map[string]any
	vector     []float32
}

// identityStore is a Weaviate that keeps what it is sent, driven through the
// real SDK. A batch write replaces an object with the same UUID, a merge
// changes only the properties it names, and a search answers from what is
// stored. These are the server's semantics, not a count of request rows.
type identityStore struct {
	mu         sync.Mutex
	objects    map[string]identityObject
	failBatch  bool
	failMerge  bool
	queryError string
	queryLimit int
}

func (s *identityStore) repository(t *testing.T) *weaviateRepository {
	t.Helper()
	s.objects = make(map[string]identityObject)
	schema := schemaExists(identityCollection)
	fake := &fakeWeaviate{
		rest: func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				schema(w, r)
				return
			}
			s.merge(t, w, r)
		},
		search:       s.search,
		batchObjects: func(req *pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error) { return s.write(t, req) },
		batchDelete:  s.remove,
	}
	return fake.repository(t, "Identity")
}

// write stores a batch. With failBatch set, the request fails after its first
// object was persisted, the way an interrupted write leaves things.
func (s *identityStore) write(t *testing.T, req *pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, obj := range req.GetObjects() {
		if _, err := uuid.Parse(obj.GetUuid()); !assert.NoError(t, err, "Weaviate requires UUID object IDs") {
			return nil, status.Error(codes.InvalidArgument, "invalid object id")
		}
		assert.Equal(t, identityCollection, obj.GetCollection())
		var vector []float32
		for _, v := range obj.GetVectors() {
			if v.GetName() == fieldEmbedding {
				vector = bytesFloat32(v.GetVectorBytes())
			}
		}
		s.objects[obj.GetUuid()] = identityObject{
			properties: obj.GetProperties().GetNonRefProperties().AsMap(),
			vector:     vector,
		}
		if s.failBatch {
			s.failBatch = false
			return nil, status.Error(codes.Unavailable, "node went away")
		}
	}
	return &pb.BatchObjectsReply{}, nil
}

// merge serves the single-object update. Anything but a PATCH would replace
// the object, so it is refused rather than passed off as a merge.
func (s *identityStore) merge(t *testing.T, w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !assert.Equal(t, http.MethodPatch, r.Method, "an update must merge, not replace") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
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
	var update struct {
		Properties map[string]any `json:"properties"`
		Vectors    map[string]any `json:"vectors"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&update))
	assert.Empty(t, update.Vectors, "the stored vector must be left alone")
	maps.Copy(object.properties, update.Properties)
	w.WriteHeader(http.StatusNoContent)
}

func (s *identityStore) remove(req *pb.BatchDeleteRequest) (*pb.BatchDeleteReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	field := req.GetFilters().GetTarget().GetProperty()
	values := filterTexts(req.GetFilters())
	var removed int64
	for id, object := range s.objects {
		if value, _ := object.properties[field].(string); slices.Contains(values, value) {
			delete(s.objects, id)
			removed++
		}
	}
	return &pb.BatchDeleteReply{Matches: removed, Successful: removed}, nil
}

func (s *identityStore) search(req *pb.SearchRequest) (*pb.SearchReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queryError != "" {
		return nil, status.Error(codes.Unknown, s.queryError)
	}
	limit := int(req.GetLimit())
	if s.queryLimit > 0 && limit > s.queryLimit {
		return nil, status.Error(codes.Unknown, fmt.Sprintf(
			"query maximum results exceeded: the total limit calculated from the provided offset '0' "+
				"and limit '%d' exceeds the configured value for QUERY_MAXIMUM_RESULTS '%d'",
			limit, s.queryLimit))
	}
	chunkIDs := filterTexts(filterCondition(req.GetFilters(), fieldChunkID))
	enabledOnly := filterCondition(req.GetFilters(), fieldIsEnabled).GetValueBoolean()
	var rows []fakeRow
	for _, id := range slices.Sorted(maps.Keys(s.objects)) {
		object := s.objects[id]
		if chunkIDs != nil && !slices.Contains(chunkIDs, object.properties[fieldChunkID].(string)) {
			continue
		}
		if enabledOnly && object.properties[fieldIsEnabled] != true {
			continue
		}
		if nv := req.GetNearVector(); nv != nil && !nearVectorIs(nv, object.vector) {
			continue
		}
		properties := make(map[string]any, len(object.properties))
		for name, value := range object.properties {
			if number, ok := value.(float64); ok {
				value = int(number)
			}
			properties[name] = value
		}
		rows = append(rows, fakeRow{id: id, properties: properties, certainty: float32Ptr(1)})
		if limit > 0 && len(rows) == limit {
			break
		}
	}
	return searchReply(rows...), nil
}

// nearVectorIs reports whether a search asks for exactly vector, wherever the
// client put it in the request.
func nearVectorIs(nv *pb.NearVector, vector []float32) bool {
	want := float32Bytes(vector)
	var candidates [][]byte
	for _, v := range nv.GetVectors() {
		candidates = append(candidates, v.GetVectorBytes())
	}
	for _, target := range nv.GetVectorForTargets() {
		for _, v := range target.GetVectors() {
			candidates = append(candidates, v.GetVectorBytes())
		}
	}
	for _, candidate := range candidates {
		if len(candidate) > 0 && bytes.Equal(candidate, want) {
			return true
		}
	}
	return false
}

func bytesFloat32(b []byte) []float32 {
	vector := make([]float32, len(b)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return vector
}

func (s *identityStore) snapshot() map[string]identityObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := maps.Clone(s.objects)
	for id, object := range objects {
		object.properties = maps.Clone(object.properties)
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

// Save goes through the same batch insert as BatchSave, so saving a source
// again replaces its object instead of failing or adding a second one.
func TestSavePreservesGeneratedQuestionsAndDoesNotDuplicateARetriedSource(t *testing.T) {
	store := &identityStore{}
	repo := store.repository(t)
	rows, params := identityInputs()
	for _, row := range rows {
		require.NoError(t, repo.Save(context.Background(), row, params))
	}
	require.Len(t, store.snapshot(), 3)
	require.NoError(t, repo.Save(context.Background(), rows[1], params))
	require.Len(t, store.snapshot(), 3, "a retried save must not add a duplicate")
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
		chunks = append(chunks, object.properties[fieldChunkID].(string))
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
		var object identityObject
		for _, candidate := range objects {
			if candidate.properties[fieldSourceID] == row.SourceID {
				object = candidate
				break
			}
		}
		require.NotNil(t, object.properties, "each retrieval source must still exist")
		assert.Equal(t, false, object.properties[fieldIsEnabled])
		assert.Equal(t, "tag-new", object.properties[fieldTagID])
		assert.Equal(t, row.Content, object.properties[fieldContent])
		assert.Equal(t, row.KnowledgeBaseID, object.properties[fieldKnowledgeBaseID])
		assert.Equal(t, params[fieldEmbedding].(map[string][]float32)[row.SourceID], object.vector)
		results, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
			Embedding: object.vector, TopK: 1, KnowledgeBaseIDs: []string{"kb-1"},
		})
		require.NoError(t, err)
		assert.Empty(t, results[0].Results, "disabled questions must no longer recall the chunk")
	}
	assert.Equal(t, true, objects[other.ChunkID].properties[fieldIsEnabled])
	assert.Equal(t, "", objects[other.ChunkID].properties[fieldTagID])
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
				assert.Equal(t, false, object.properties[fieldIsEnabled])
			}
		})
	}
}
