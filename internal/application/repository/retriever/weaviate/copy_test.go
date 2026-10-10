package weaviate

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

const copyTestCollection = "Copy_64"

// cursorConflictMessage is what weaviate v1.37.3 answers when a Get combines a
// filter with the cursor API (entities/filters/cursor_validator.go).
const cursorConflictMessage = "where cannot be set with after and limit parameters"

var (
	afterArgPattern = regexp.MustCompile(`after:\s*"([^"]*)"`)
	limitArgPattern = regexp.MustCompile(`limit:\s*(\d+)`)
	chunkArgPattern = regexp.MustCompile(`path: \["chunk_id"\] valueText: \[([^\]]*)\]`)
	quotedPattern   = regexp.MustCompile(`"([^"]*)"`)
)

// copySourceRow is one source vector, in the shape a GraphQL Get answers with.
func copySourceRow(id, chunkID, sourceID string) map[string]any {
	return map[string]any{
		fieldContent:         "content of " + sourceID,
		fieldSourceID:        sourceID,
		fieldSourceType:      1,
		fieldChunkID:         chunkID,
		fieldKnowledgeID:     "know-1",
		fieldKnowledgeBaseID: "kb-src",
		fieldTagID:           "",
		"_additional":        map[string]any{"id": id, "vector": []any{0.5, 0.25}},
	}
}

// copyWrittenObject is one object the copy sent to the batch endpoint.
type copyWrittenObject struct {
	sourceID      string
	targetChunkID string
}

// copyPageServer stands in for a Weaviate data node holding the source
// vectors. It answers Get the way the real one does - with the rows whose
// chunk_id is in the ContainsAny list the query asked for, cut off at the
// query's limit - and it validates the cursor API the way the real one does,
// so a query that combines `where` with `after` fails here exactly as it fails
// in production.
type copyPageServer struct {
	mu sync.Mutex
	// rows holds each chunk's vectors: one for the chunk content plus one per
	// generated question, all sharing the chunk_id.
	rows  map[string][]map[string]any
	order []string

	// queryLimit is the server's QUERY_MAXIMUM_RESULTS; 0 means no refusal.
	queryLimit int
	// ignoreChunkFilter answers every query with the same page, the way a
	// source whose pages repeat themselves behaves.
	ignoreChunkFilter bool
	// queryError, when set, is the GraphQL error every Get is answered with.
	queryError string
	// omitData answers every Get with a data object that carries no Get key.
	omitData bool
	// omitCollection answers with a Get object that carries no key for the
	// queried collection.
	omitCollection bool

	queries    int
	limits     []int
	requested  [][]string
	rejected   []string
	batches    int
	written    []copyWrittenObject
	unexpected []string
}

// copyTestStore builds a source whose chunks hold the given vectors. Each
// entry maps a chunk ID to its source IDs: the chunk's own content vector
// first, then one per generated question.
func copyTestStore(vectors map[string][]string) *copyPageServer {
	s := &copyPageServer{rows: make(map[string][]map[string]any)}
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
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
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

// serve starts an httptest server for s and returns a repository pointed at
// it. The copy's own query limit is left at its default; tests that need the
// truncation path lower it on the returned repository.
func (s *copyPageServer) serve(t *testing.T) *weaviateRepository {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(server.Close)
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	return &weaviateRepository{client: client, collectionBaseName: "Copy"}
}

func (s *copyPageServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(s.resolve(body.Query))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/batch/objects":
		var body struct {
			Objects []struct {
				Properties map[string]any `json:"properties"`
			} `json:"objects"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.batches++
		for _, obj := range body.Objects {
			s.written = append(s.written, copyWrittenObject{
				sourceID:      fmt.Sprint(obj.Properties[fieldSourceID]),
				targetChunkID: fmt.Sprint(obj.Properties[fieldChunkID]),
			})
		}
		s.mu.Unlock()
		_, _ = w.Write([]byte(`[]`))
	case r.Method == http.MethodGet && r.URL.Path == "/v1/meta":
		_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
	default:
		s.mu.Lock()
		s.unexpected = append(s.unexpected, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}
}

// resolve answers one GraphQL Get the way Weaviate does. A query it refused
// comes back as a successful call carrying an errors array, which is what the
// copy has to read as a failure.
func (s *copyPageServer) resolve(query string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++

	limit := 0
	if match := limitArgPattern.FindStringSubmatch(query); match != nil {
		limit, _ = strconv.Atoi(match[1])
	}
	s.limits = append(s.limits, limit)

	// The cursor API refuses a filtered query: a Get that asks for rows after a
	// cursor cannot also carry where and limit.
	if strings.Contains(query, "where:") {
		if afterArgPattern.MatchString(query) {
			s.rejected = append(s.rejected, query)
			return graphQLError("cursor api: invalid 'after' parameter: " + cursorConflictMessage)
		}
	}

	switch {
	case s.queryError != "":
		return graphQLError(s.queryError)
	case s.omitData:
		return map[string]any{"data": map[string]any{}}
	case s.omitCollection:
		return map[string]any{"data": map[string]any{"Get": map[string]any{}}}
	}

	if s.queryLimit > 0 && limit > s.queryLimit {
		return graphQLError(fmt.Sprintf(
			"query maximum results exceeded: the total limit calculated from the provided offset '0' "+
				"and limit '%d' exceeds the configured value for QUERY_MAXIMUM_RESULTS '%d'. "+
				"If you've supplied a negative offset or limit, this may be an underflow error",
			limit, s.queryLimit))
	}

	chunkIDs := copyRequestedChunkIDs(query)
	s.requested = append(s.requested, chunkIDs)

	order := chunkIDs
	if s.ignoreChunkFilter {
		order = s.order
	}
	rows := make([]any, 0, len(s.rows))
	for _, chunkID := range order {
		for _, row := range s.rows[chunkID] {
			rows = append(rows, row)
		}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return map[string]any{"data": map[string]any{"Get": map[string]any{copyTestCollection: rows}}}
}

func graphQLError(message string) map[string]any {
	return map[string]any{"errors": []any{map[string]any{"message": message}}}
}

// copyRequestedChunkIDs pulls the chunk_id ContainsAny values out of a query.
func copyRequestedChunkIDs(query string) []string {
	match := chunkArgPattern.FindStringSubmatch(query)
	if match == nil {
		return nil
	}
	var ids []string
	for _, quoted := range quotedPattern.FindAllStringSubmatch(match[1], -1) {
		ids = append(ids, quoted[1])
	}
	return ids
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

// check states the invariants every copy test depends on: no Get ever combined
// where with after, and every Get named the chunk IDs it wanted.
func (s *copyPageServer) check(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.rejected, "a source query combined where with after; weaviate refuses that")
	assert.Empty(t, s.unexpected, "the copy called an endpoint it should not have")
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
// filtered Get that carries `after` is refused by Weaviate ("where cannot be
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

// A query Weaviate could not run comes back as HTTP 200 with an errors array and
// no data, so the SDK reports no Go error. Reading that as "the source has no
// more objects" turns a failed query into a successful, silently truncated copy
// - and the response type check used to panic before it could even get there.
func TestCopyIndicesReportsAQueryError(t *testing.T) {
	const failure = "class Weknora_embeddings_64 does not exist"
	source := copyTestSingleVectorChunks("chunk-0")
	source.queryError = failure
	repo := source.serve(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CopyIndices panicked on a query that could not run: %v", r)
		}
	}()

	require.ErrorContains(t, copyTestRun(t, source, repo, copyTestChunkMap(1)), failure)
	assert.Equal(t, 1, source.queryCount(), "the walk must stop on the failed query")
	assert.Zero(t, len(source.writtenObjects()), "a failed query must not be reported as a finished copy")
}

// A response the walk cannot read must be reported, not turned into a panic by
// an unchecked assertion on the response shape.
func TestCopyIndicesRejectsAResponseWithoutData(t *testing.T) {
	brokenResponses := map[string]func(*copyPageServer){
		"no data object":       func(s *copyPageServer) { s.omitData = true },
		"no collection in Get": func(s *copyPageServer) { s.omitCollection = true },
	}
	for name, breakResponse := range brokenResponses {
		t.Run(name, func(t *testing.T) {
			source := copyTestSingleVectorChunks("chunk-0")
			breakResponse(source)
			repo := source.serve(t)
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("CopyIndices panicked on a response without data: %v", r)
				}
			}()

			require.ErrorContains(t, copyTestRun(t, source, repo, copyTestChunkMap(1)), "invalid response")
			assert.Equal(t, 1, source.queryCount(), "the walk must stop on the unreadable response")
			assert.Zero(t, len(source.writtenObjects()), "nothing may be reported as copied")
		})
	}
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
