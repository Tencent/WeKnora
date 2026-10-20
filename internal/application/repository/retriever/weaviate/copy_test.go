package weaviate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/weaviate/weaviate/grpc/generated/protocol/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const copyTestCollection = "Copy_64"

// cursorConflictMessage is what weaviate answers when a query combines a
// filter with the cursor API (entities/filters/cursor_validator.go).
const cursorConflictMessage = "where cannot be set with after and limit parameters"

// copySourceRow is one source vector, in the shape a search answers with.
func copySourceRow(id, chunkID, sourceID string) fakeRow {
	return fakeRow{
		id: id,
		properties: map[string]any{
			fieldContent:         "content of " + sourceID,
			fieldSourceID:        sourceID,
			fieldSourceType:      1,
			fieldChunkID:         chunkID,
			fieldKnowledgeID:     "know-1",
			fieldKnowledgeBaseID: "kb-src",
			fieldTagID:           "",
		},
		vector: []float32{0.5, 0.25},
	}
}

// copyWrittenObject is one object the copy sent to the batch endpoint.
type copyWrittenObject struct {
	sourceID      string
	targetChunkID string
}

// copyPageServer stands in for a Weaviate data node holding the source
// vectors. It answers a search the way the real one does - with the rows whose
// chunk_id is in the ContainsAny list the query asked for, cut off at the
// query's limit - and it validates the cursor API the way the real one does,
// so a query that combines a filter with `after` fails here exactly as it fails
// in production.
type copyPageServer struct {
	mu sync.Mutex
	// rows holds each chunk's vectors: one for the chunk content plus one per
	// generated question, all sharing the chunk_id.
	rows  map[string][]fakeRow
	order []string

	// queryLimit is the server's QUERY_MAXIMUM_RESULTS; 0 means no refusal.
	queryLimit int
	// ignoreChunkFilter answers every query with the same page, the way a
	// source whose pages repeat themselves behaves.
	ignoreChunkFilter bool
	// queryError, when set, is the error every search is answered with.
	queryError string
	// omitIDs answers with rows that carry no object ID.
	omitIDs bool

	queries   int
	limits    []int
	requested [][]string
	rejected  []string
	batches   int
	written   []copyWrittenObject
}

// copyTestStore builds a source whose chunks hold the given vectors. Each
// entry maps a chunk ID to its source IDs: the chunk's own content vector
// first, then one per generated question.
func copyTestStore(vectors map[string][]string) *copyPageServer {
	s := &copyPageServer{rows: make(map[string][]fakeRow)}
	next := 0
	for _, chunkID := range slices.Sorted(maps.Keys(vectors)) {
		for _, sourceID := range vectors[chunkID] {
			s.rows[chunkID] = append(s.rows[chunkID], copySourceRow(copyTestUUID(next), chunkID, sourceID))
			next++
		}
		s.order = append(s.order, chunkID)
	}
	return s
}

func copyTestUUID(i int) string {
	return fmt.Sprintf("00000000-0000-0000-0001-%012d", i)
}

// copyTestChunkMap maps size chunks onto target chunks.
func copyTestChunkMap(size int) map[string]string {
	m := make(map[string]string, size)
	for i := range size {
		m[fmt.Sprintf("chunk-%d", i)] = fmt.Sprintf("target-chunk-%d", i)
	}
	return m
}

// copyTestSingleVectorChunks builds a source with one vector per named chunk.
func copyTestSingleVectorChunks(chunkIDs ...string) *copyPageServer {
	vectors := make(map[string][]string, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		vectors[chunkID] = []string{chunkID}
	}
	return copyTestStore(vectors)
}

// serve starts a fake Weaviate for s and returns a repository pointed at it.
// The copy's own query limit is left at its default; tests that need the
// truncation path lower it on the returned repository.
func (s *copyPageServer) serve(t *testing.T) *weaviateRepository {
	t.Helper()
	fake := &fakeWeaviate{search: s.resolve, batchObjects: s.write}
	return fake.repository(t, "Copy")
}

func (s *copyPageServer) write(req *pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches++
	for _, obj := range req.GetObjects() {
		props := obj.GetProperties().GetNonRefProperties().GetFields()
		s.written = append(s.written, copyWrittenObject{
			sourceID:      props[fieldSourceID].GetStringValue(),
			targetChunkID: props[fieldChunkID].GetStringValue(),
		})
	}
	return &pb.BatchObjectsReply{}, nil
}

// resolve answers one search the way Weaviate does.
func (s *copyPageServer) resolve(req *pb.SearchRequest) (*pb.SearchReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++

	limit := int(req.GetLimit())
	s.limits = append(s.limits, limit)

	// The cursor API refuses a filtered query: a search that asks for rows
	// after a cursor cannot also carry a filter and a limit.
	if req.GetFilters() != nil && req.GetAfter() != "" {
		s.rejected = append(s.rejected, req.String())
		return nil, status.Error(codes.InvalidArgument,
			"cursor api: invalid 'after' parameter: "+cursorConflictMessage)
	}

	if s.queryError != "" {
		return nil, status.Error(codes.Unknown, s.queryError)
	}

	if s.queryLimit > 0 && limit > s.queryLimit {
		return nil, status.Error(codes.Unknown, fmt.Sprintf(
			"query maximum results exceeded: the total limit calculated from the provided offset '0' "+
				"and limit '%d' exceeds the configured value for QUERY_MAXIMUM_RESULTS '%d'. "+
				"If you've supplied a negative offset or limit, this may be an underflow error",
			limit, s.queryLimit))
	}

	chunkIDs := filterTexts(filterCondition(req.GetFilters(), fieldChunkID))
	s.requested = append(s.requested, chunkIDs)

	order := chunkIDs
	if s.ignoreChunkFilter {
		order = s.order
	}
	var rows []fakeRow
	for _, chunkID := range order {
		rows = append(rows, s.rows[chunkID]...)
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	reply := searchReply(rows...)
	if s.omitIDs {
		for _, result := range reply.Results {
			result.Metadata.Id, result.Metadata.IdAsBytes = "", nil
		}
	}
	return reply, nil
}

func (s *copyPageServer) queryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries
}

func (s *copyPageServer) queryLimits() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.limits)
}

func (s *copyPageServer) requestedBatches() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	batches := make([][]string, 0, len(s.requested))
	for _, batch := range s.requested {
		batches = append(batches, slices.Clone(batch))
	}
	return batches
}

func (s *copyPageServer) writtenObjects() []copyWrittenObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.written)
}

func (s *copyPageServer) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("queries=%d limits=%v batches-requested=%v objects-written=%d refused-cursor-combos=%d",
		s.queries, s.limits, s.requested, len(s.written), len(s.rejected))
}

// check states the invariants every copy test depends on: no search ever
// combined a filter with after, and every search named the chunk IDs it wanted.
func (s *copyPageServer) check(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.rejected, "a source query combined a filter with after; weaviate refuses that")
	for i, batch := range s.requested {
		assert.NotEmpty(t, batch, "query %d did not name the chunks it wanted", i)
	}
}

// copyTestRun copies mapping from the fake source and logs what the source saw.
func copyTestRun(t *testing.T, source *copyPageServer, repo *weaviateRepository, mapping map[string]string) error {
	t.Helper()
	defer func() { t.Log("copy walk: " + source.summary()) }()
	defer source.check(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return repo.CopyIndices(ctx, "kb-src", map[string]string{"know-1": "know-2"}, mapping, "kb-dst", 64, "doc")
}

// CopyIndices has to read the source by the chunk IDs the mapping names. A
// filtered query that carries `after` is refused by Weaviate ("where cannot be
// set with after and limit parameters"), which the cursor walk this replaces
// provoked on its very first page, and the fake server refuses it the same way.
func TestCopyIndicesQueriesByChunkIDWithoutACursor(t *testing.T) {
	const chunks = 130 // 64 + 64 + 2: more than one batch, one short batch
	source := copyTestSingleVectorChunks(keysOf(copyTestChunkMap(chunks))...)
	repo := source.serve(t)

	require.NoError(t, copyTestRun(t, source, repo, copyTestChunkMap(chunks)))

	assert.Equal(t, 3, source.queryCount(), "130 mapped chunks are read as three batches")
	assert.Equal(t, 130, len(source.writtenObjects()), "every mapped chunk must be copied")
	assert.ElementsMatch(t, []int{64, 64, 2}, batchSizes(source.requestedBatches()),
		"each read asks for one batch of the mapping")

	var read []string
	for _, batch := range source.requestedBatches() {
		assert.True(t, slices.IsSorted(batch), "a batch is read in mapping order")
		read = append(read, batch...)
	}
	assert.ElementsMatch(t, keysOf(copyTestChunkMap(chunks)), read,
		"the reads cover the mapping exactly, each chunk once")
}

// A source that answers every request with the same page must not have that
// page replayed into the target. Reads are bounded by the mapping, so the copy
// asks for each batch once and cannot walk in circles.
func TestCopyIndicesDoesNotReplayARepeatedPage(t *testing.T) {
	const pageSize = 64
	source := copyTestSingleVectorChunks(keysOf(copyTestChunkMap(pageSize))...)
	source.ignoreChunkFilter = true
	repo := source.serve(t)

	require.NoError(t, copyTestRun(t, source, repo, copyTestChunkMap(pageSize)))

	assert.Equal(t, 1, source.queryCount(), "the mapping is read once, not walked page by page")
	assert.Equal(t, pageSize, len(source.writtenObjects()), "the page must not be written twice")
}

// A chunk holds one vector for its content plus one per generated question,
// all sharing the chunk_id. Every one of them has to reach the target: a query
// that names one chunk ID must not be mistaken for a query that returns one
// row.
func TestCopyIndicesCopiesEveryVectorOfAChunk(t *testing.T) {
	source := copyTestStore(map[string][]string{
		"chunk-0": {"chunk-0", "chunk-0-q1", "chunk-0-q2", "chunk-0-q3"},
	})
	repo := source.serve(t)

	require.NoError(t, copyTestRun(t, source, repo, map[string]string{"chunk-0": "target-chunk-0"}))

	written := source.writtenObjects()
	assert.Equal(t, 4, len(written), "the chunk's content vector and all three question vectors")
	assert.ElementsMatch(t,
		[]string{"target-chunk-0", "target-chunk-0-q1", "target-chunk-0-q2", "target-chunk-0-q3"},
		sourceIDsOf(written))
	for _, obj := range written {
		assert.Equal(t, "target-chunk-0", obj.targetChunkID)
	}
}

// A batch that comes back with exactly as many rows as it asked for may have
// been cut off, so it has to be split and re-read. Here four chunk IDs produce
// five vectors: the first read is full, the two halves are not, and all five
// vectors still have to be copied.
func TestCopyIndicesSplitsABatchThatFillsTheLimit(t *testing.T) {
	source := copyTestStore(map[string][]string{
		"chunk-0": {"chunk-0", "chunk-0-q1"},
		"chunk-1": {"chunk-1"},
		"chunk-2": {"chunk-2"},
		"chunk-3": {"chunk-3"},
	})
	repo := source.serve(t)
	repo.copyQueryLimit = 4

	mapping := map[string]string{
		"chunk-0": "target-chunk-0",
		"chunk-1": "target-chunk-1",
		"chunk-2": "target-chunk-2",
		"chunk-3": "target-chunk-3",
	}
	require.NoError(t, copyTestRun(t, source, repo, mapping))

	assert.Equal(t, 5, len(source.writtenObjects()), "every vector of the batch must be copied")
	assert.Equal(t, 3, source.queryCount(), "a full read is followed by two half reads")
	assert.ElementsMatch(t, []int{4, 2, 2}, batchSizes(source.requestedBatches()))
}

// When one chunk on its own fills the query limit it cannot be read
// completely, and the copy has to say so rather than report success on an
// index that is missing vectors.
func TestCopyIndicesStopsOnAChunkThatFillsTheLimit(t *testing.T) {
	source := copyTestStore(map[string][]string{
		"chunk-0": {"chunk-0", "chunk-0-q1", "chunk-0-q2", "chunk-0-q3", "chunk-0-q4"},
	})
	repo := source.serve(t)
	repo.copyQueryLimit = 3

	err := copyTestRun(t, source, repo, map[string]string{"chunk-0": "target-chunk-0"})

	require.ErrorContains(t, err, "chunk-0", "the error must name the chunk that cannot be read")
	require.ErrorContains(t, err, "QUERY_MAXIMUM_RESULTS",
		"the error must say the chunk's vector count reaches the server's query limit")
	assert.Zero(t, len(source.writtenObjects()), "an unreadable chunk must not be reported as copied")
}

// The server's QUERY_MAXIMUM_RESULTS is not exposed over the client API, so the
// copy asks for the documented default first and, refused for it, retries at
// the limit the server names.
func TestCopyIndicesLowersTheQueryLimitWhenTheServerRefuses(t *testing.T) {
	source := copyTestSingleVectorChunks("chunk-0", "chunk-1", "chunk-2")
	source.queryLimit = 4
	repo := source.serve(t)

	require.NoError(t, copyTestRun(t, source, repo, copyTestChunkMap(3)))

	assert.Equal(t, []int{10000, 4}, source.queryLimits(),
		"the refused default is retried at the server's own limit")
	assert.Equal(t, 3, len(source.writtenObjects()))
	assert.Equal(t, 2, source.queryCount(), "only the first read pays for the refusal")
}

// A query Weaviate could not run must stop the copy. Reading it as "the source
// has no more objects" turns a failed query into a successful, silently
// truncated copy.
func TestCopyIndicesReportsAQueryError(t *testing.T) {
	const failure = "class Weknora_embeddings_64 does not exist"
	source := copyTestSingleVectorChunks("chunk-0")
	source.queryError = failure
	repo := source.serve(t)

	require.ErrorContains(t, copyTestRun(t, source, repo, copyTestChunkMap(1)), failure)
	assert.Equal(t, 1, source.queryCount(), "the walk must stop on the failed query")
	assert.Zero(t, len(source.writtenObjects()), "a failed query must not be reported as a finished copy")
}

// A row the copy cannot identify is a response it does not understand, and
// must not be counted towards a copy reported as complete.
func TestCopyIndicesRejectsARowWithoutAnID(t *testing.T) {
	source := copyTestSingleVectorChunks("chunk-0")
	source.omitIDs = true
	repo := source.serve(t)

	require.ErrorContains(t, copyTestRun(t, source, repo, copyTestChunkMap(1)), "has no id")
	assert.Equal(t, 1, source.queryCount(), "the walk must stop on the unreadable response")
	assert.Zero(t, len(source.writtenObjects()), "nothing may be reported as copied")
}

// Objects the server refuses come back next to a successful batch call. A copy
// that drops them must fail instead of reporting every row as copied.
func TestCopyIndicesReportsRefusedObjects(t *testing.T) {
	source := copyTestSingleVectorChunks("chunk-0", "chunk-1")
	fake := &fakeWeaviate{
		search: source.resolve,
		batchObjects: func(*pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error) {
			return &pb.BatchObjectsReply{Errors: []*pb.BatchObjectsReply_BatchError{
				{Index: 1, Error: "vector lengths don't match"},
			}}, nil
		},
	}
	repo := fake.repository(t, "Copy")

	err := copyTestRun(t, source, repo, copyTestChunkMap(2))
	require.ErrorContains(t, err, "batch upsert failed")
	require.ErrorContains(t, err, "vector lengths don't match", "the server's reason must reach the caller")
}

func keysOf[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func batchSizes(batches [][]string) []int {
	sizes := make([]int, 0, len(batches))
	for _, batch := range batches {
		sizes = append(sizes, len(batch))
	}
	return sizes
}

func sourceIDsOf(objects []copyWrittenObject) []string {
	ids := make([]string, 0, len(objects))
	for _, obj := range objects {
		ids = append(ids, obj.sourceID)
	}
	return ids
}
