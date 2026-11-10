package milvus

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	"github.com/milvus-io/milvus/client/v2/entity"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	copyTestDim       = 3
	copyTestSourceKB  = "kb-source"
	copyTestTargetKB  = "kb-target"
	copyTestKnowledge = "doc-1"
	copyTestTargetDoc = "doc-2"
)

// copyTestRow is one source point. The tests keep a single row per chunk, so
// "the chunk is missing from the target" and "its vector is missing" are the
// same event and can be asserted on the upserted chunk IDs.
type copyTestRow struct {
	id              string
	chunkID         string
	knowledgeBaseID string
}

// copyTestRead records one Query the copy issued, so a test can see whether the
// traversal depended on an offset cursor.
type copyTestRead struct {
	expr   string
	offset int
	limit  int
}

// fakeMilvusCopyService is an in-process Milvus endpoint for CopyIndices. The
// repository holds a concrete *client.Client, so a gRPC server is the only seam
// that exercises the real query and upsert path instead of a hand-written stub.
//
// It answers the handful of RPCs CopyIndices needs (describe/load/query/upsert)
// and evaluates the predicates this repository emits: an equality on
// knowledge_base_id and an optional "in" list on chunk_id.
type fakeMilvusCopyService struct {
	milvuspb.UnimplementedMilvusServiceServer

	mu sync.Mutex
	// rows is the stored collection content.
	rows []copyTestRow
	// drift rotates the reported result order, see matchingRows.
	drift int
	// reads and upserted are the observations the tests assert on.
	reads    []copyTestRead
	upserted []MilvusVectorEmbedding
}

func (s *fakeMilvusCopyService) DescribeCollection(
	_ context.Context, request *milvuspb.DescribeCollectionRequest,
) (*milvuspb.DescribeCollectionResponse, error) {
	return &milvuspb.DescribeCollectionResponse{
		Status:       &commonpb.Status{},
		CollectionID: 1,
		ShardsNum:    1,
		Schema: &schemapb.CollectionSchema{
			Name: request.GetCollectionName(),
			Fields: []*schemapb.FieldSchema{
				{FieldID: 100, Name: fieldID, DataType: schemapb.DataType_VarChar, IsPrimaryKey: true},
				{
					FieldID:  101,
					Name:     fieldEmbedding,
					DataType: schemapb.DataType_FloatVector,
					TypeParams: []*commonpb.KeyValuePair{
						{Key: "dim", Value: strconv.Itoa(copyTestDim)},
					},
				},
				{FieldID: 102, Name: fieldContent, DataType: schemapb.DataType_VarChar},
				{FieldID: 103, Name: fieldSourceID, DataType: schemapb.DataType_VarChar},
				{FieldID: 104, Name: fieldSourceType, DataType: schemapb.DataType_Int64},
				{FieldID: 105, Name: fieldChunkID, DataType: schemapb.DataType_VarChar},
				{FieldID: 106, Name: fieldKnowledgeID, DataType: schemapb.DataType_VarChar},
				{FieldID: 107, Name: fieldKnowledgeBaseID, DataType: schemapb.DataType_VarChar},
				{FieldID: 108, Name: fieldTagID, DataType: schemapb.DataType_VarChar},
				{FieldID: 109, Name: fieldIsEnabled, DataType: schemapb.DataType_Bool},
			},
		},
	}, nil
}

func (s *fakeMilvusCopyService) LoadCollection(
	_ context.Context, _ *milvuspb.LoadCollectionRequest,
) (*commonpb.Status, error) {
	return &commonpb.Status{}, nil
}

func (s *fakeMilvusCopyService) GetLoadingProgress(
	_ context.Context, _ *milvuspb.GetLoadingProgressRequest,
) (*milvuspb.GetLoadingProgressResponse, error) {
	return &milvuspb.GetLoadingProgressResponse{
		Status:          &commonpb.Status{},
		Progress:        100,
		RefreshProgress: 100,
	}, nil
}

func (s *fakeMilvusCopyService) Query(
	_ context.Context, request *milvuspb.QueryRequest,
) (*milvuspb.QueryResults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	offset, limit := 0, -1
	for _, param := range request.GetQueryParams() {
		switch param.GetKey() {
		case "offset":
			offset, _ = strconv.Atoi(param.GetValue())
		case "limit":
			limit, _ = strconv.Atoi(param.GetValue())
		}
	}
	s.reads = append(s.reads, copyTestRead{expr: request.GetExpr(), offset: offset, limit: limit})

	matched := s.matchingRowsLocked(request)
	if offset >= len(matched) {
		matched = nil
	} else {
		matched = matched[offset:]
	}
	if limit >= 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	return copyTestQueryResponse(matched), nil
}

// matchingRowsLocked evaluates the request predicate over the collection and
// returns the matching rows in the order this backend reports them: a rotation
// of the stored order that advances by one on every acknowledged write.
//
// Milvus does not make the order of a scalar query result part of its API
// contract — results are merged segment by segment, and the segment set changes
// while data is written — and CopyIndices writes into the collection it reads
// from on every page. A client that addresses pages by offset therefore gets a
// moving window even though every single request succeeds: rows can be skipped
// (the page after the cursor starts past them) or repeated. The row set below
// never changes; only the reported order does.
func (s *fakeMilvusCopyService) matchingRowsLocked(request *milvuspb.QueryRequest) []copyTestRow {
	if len(s.rows) == 0 {
		return nil
	}
	ordered := make([]copyTestRow, 0, len(s.rows))
	for i := range s.rows {
		ordered = append(ordered, s.rows[(i+s.drift)%len(s.rows)])
	}

	var knowledgeBaseID string
	var chunkIDs []string
	for name, value := range request.GetExprTemplateValues() {
		if kb := value.GetStringVal(); kb != "" && strings.HasPrefix(name, fieldKnowledgeBaseID) {
			knowledgeBaseID = kb
		}
		if array := value.GetArrayVal(); array != nil && strings.HasPrefix(name, fieldChunkID) {
			chunkIDs = array.GetStringData().GetData()
		}
	}

	matched := make([]copyTestRow, 0, len(ordered))
	for _, row := range ordered {
		if knowledgeBaseID != "" && row.knowledgeBaseID != knowledgeBaseID {
			continue
		}
		if len(chunkIDs) > 0 && !slices.Contains(chunkIDs, row.chunkID) {
			continue
		}
		matched = append(matched, row)
	}
	return matched
}

func (s *fakeMilvusCopyService) Upsert(
	_ context.Context, request *milvuspb.UpsertRequest,
) (*milvuspb.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := copyTestStringColumn(request, fieldID)
	chunks := copyTestStringColumn(request, fieldChunkID)
	knowledgeIDs := copyTestStringColumn(request, fieldKnowledgeID)
	knowledgeBaseIDs := copyTestStringColumn(request, fieldKnowledgeBaseID)
	for i, id := range ids {
		s.upserted = append(s.upserted, MilvusVectorEmbedding{
			ID:              id,
			ChunkID:         chunks[i],
			KnowledgeID:     knowledgeIDs[i],
			KnowledgeBaseID: knowledgeBaseIDs[i],
		})
	}
	// The write is what moves the reported result order: it is acknowledged
	// here, and the collection is re-read on the next page.
	s.drift++

	return &milvuspb.MutationResult{
		Status:    &commonpb.Status{},
		UpsertCnt: int64(len(ids)),
		IDs: &schemapb.IDs{IdField: &schemapb.IDs_StrId{
			StrId: &schemapb.StringArray{Data: ids},
		}},
	}, nil
}

// copiedChunks returns the sorted chunk IDs of every row written to the target.
func (s *fakeMilvusCopyService) copiedChunks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunks := make([]string, 0, len(s.upserted))
	for _, row := range s.upserted {
		chunks = append(chunks, row.ChunkID)
	}
	slices.Sort(chunks)
	return chunks
}

// readOffsets returns the offset of every read, for diagnostics.
func (s *fakeMilvusCopyService) readOffsets() []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	offsets := make([]int, 0, len(s.reads))
	for _, read := range s.reads {
		offsets = append(offsets, read.offset)
	}
	return offsets
}

func copyTestStringColumn(request *milvuspb.UpsertRequest, name string) []string {
	for _, field := range request.GetFieldsData() {
		if field.GetFieldName() != name {
			continue
		}
		return field.GetScalars().GetStringData().GetData()
	}
	return nil
}

func copyTestQueryResponse(rows []copyTestRow) *milvuspb.QueryResults {
	// A real server echoes the requested output fields; the client uses the
	// echo to expand the "*" wildcard.
	response := &milvuspb.QueryResults{Status: &commonpb.Status{}, OutputFields: []string{"*"}}
	if len(rows) == 0 {
		return response
	}

	ids := make([]string, 0, len(rows))
	chunks := make([]string, 0, len(rows))
	knowledgeIDs := make([]string, 0, len(rows))
	knowledgeBases := make([]string, 0, len(rows))
	embeddings := make([]float32, 0, len(rows)*copyTestDim)
	for i, row := range rows {
		ids = append(ids, row.id)
		chunks = append(chunks, row.chunkID)
		knowledgeIDs = append(knowledgeIDs, copyTestKnowledge)
		knowledgeBases = append(knowledgeBases, row.knowledgeBaseID)
		embeddings = append(embeddings, float32(i+1), 0, 0)
	}
	response.FieldsData = []*schemapb.FieldData{
		copyTestStringField(fieldID, ids),
		copyTestStringField(fieldChunkID, chunks),
		copyTestStringField(fieldKnowledgeID, knowledgeIDs),
		copyTestStringField(fieldKnowledgeBaseID, knowledgeBases),
		copyTestFloatVectorField(embeddings),
	}
	return response
}

func copyTestStringField(name string, values []string) *schemapb.FieldData {
	return &schemapb.FieldData{
		Type:      schemapb.DataType_VarChar,
		FieldName: name,
		Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{
			Data: &schemapb.ScalarField_StringData{
				StringData: &schemapb.StringArray{Data: values},
			},
		}},
	}
}

func copyTestFloatVectorField(values []float32) *schemapb.FieldData {
	return &schemapb.FieldData{
		Type:      schemapb.DataType_FloatVector,
		FieldName: fieldEmbedding,
		Field: &schemapb.FieldData_Vectors{Vectors: &schemapb.VectorField{
			Dim: copyTestDim,
			Data: &schemapb.VectorField_FloatVector{
				FloatVector: &schemapb.FloatArray{Data: values},
			},
		}},
	}
}

type copyMilvusHarness struct {
	repo *milvusRepository
	fake *fakeMilvusCopyService
}

func newCopyMilvusHarness(t *testing.T, rows []copyTestRow) *copyMilvusHarness {
	t.Helper()
	fake := &fakeMilvusCopyService{rows: rows}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	milvuspb.RegisterMilvusServiceServer(grpcServer, fake)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	// DisableConn skips the Connect handshake, which this fake does not serve.
	c, err := client.New(context.Background(), &client.ClientConfig{
		Address:     listener.Addr().String(),
		DisableConn: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	return &copyMilvusHarness{
		repo: &milvusRepository{
			client:             c,
			collectionBaseName: "weknora_embeddings",
			metricType:         entity.IP,
		},
		fake: fake,
	}
}

func (h *copyMilvusHarness) copy(t *testing.T, sourceToTargetChunkIDMap map[string]string) error {
	t.Helper()
	return h.repo.CopyIndices(context.Background(), copyTestSourceKB,
		map[string]string{copyTestKnowledge: copyTestTargetDoc},
		sourceToTargetChunkIDMap, copyTestTargetKB, copyTestDim, "manual",
	)
}

// CopyIndices must put every mapped source chunk into the target exactly once,
// no matter how the backend orders the rows of the queries it serves. A page
// cursor that is an offset into that order can slide between requests while
// every request still succeeds, which drops the vectors the cursor skipped and
// re-copies the ones it slid back over — the copy still reports success, so the
// loss is only visible as worse recall in the cloned knowledge base.
func TestCopyIndicesCopiesEveryMappedChunkWhenResultOrderDrifts(t *testing.T) {
	const rowCount = 200

	rows := make([]copyTestRow, 0, rowCount)
	mapping := make(map[string]string, rowCount)
	expected := make([]string, 0, rowCount)
	for i := range rowCount {
		chunkID := fmt.Sprintf("chunk-%03d", i)
		rows = append(rows, copyTestRow{
			id:              fmt.Sprintf("point-%03d", i),
			chunkID:         chunkID,
			knowledgeBaseID: copyTestSourceKB,
		})
		mapping[chunkID] = "target-" + chunkID
		expected = append(expected, mapping[chunkID])
	}
	slices.Sort(expected)

	harness := newCopyMilvusHarness(t, rows)
	err := harness.copy(t, mapping)
	require.NoError(t, err, "the copy must not report success while dropping vectors")

	copied := harness.fake.copiedChunks()
	missing, repeated := copyTestDiff(expected, copied)
	t.Logf("reads=%d offsets=%v copied=%d missing=%v repeated=%v",
		len(harness.fake.reads), harness.fake.readOffsets(), len(copied), missing, repeated)

	require.Len(t, copied, rowCount, "one target row per mapped source chunk")
	require.Empty(t, missing, "source chunks whose vectors never reached the target")
	require.Empty(t, repeated, "source chunks copied more than once")
}

// Source chunks outside the mapping are not part of the copy: the mapping names
// the chunks the caller cloned, and only those may appear in the target.
func TestCopyIndicesIgnoresSourceChunksOutsideTheMapping(t *testing.T) {
	harness := newCopyMilvusHarness(t, []copyTestRow{
		{id: "point-a", chunkID: "chunk-a", knowledgeBaseID: copyTestSourceKB},
		{id: "point-b", chunkID: "chunk-b", knowledgeBaseID: copyTestSourceKB},
		{id: "point-c", chunkID: "chunk-c", knowledgeBaseID: copyTestSourceKB},
	})

	err := harness.copy(t, map[string]string{"chunk-a": "target-a", "chunk-c": "target-c"})
	require.NoError(t, err)

	require.Equal(t, []string{"target-a", "target-c"}, harness.fake.copiedChunks())
}

// copyTestDiff returns the entries of want that are absent from got and the
// entries of got that appear more often than in want.
func copyTestDiff(want, got []string) (missing, repeated []string) {
	remaining := make(map[string]int, len(want))
	for _, value := range want {
		remaining[value]++
	}
	for _, value := range got {
		remaining[value]--
	}
	for value, count := range remaining {
		switch {
		case count > 0:
			missing = append(missing, value)
		case count < 0:
			repeated = append(repeated, value)
		}
	}
	slices.Sort(missing)
	slices.Sort(repeated)
	return missing, repeated
}
