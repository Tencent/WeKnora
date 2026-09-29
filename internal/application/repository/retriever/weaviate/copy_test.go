package weaviate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

const copyTestCollection = "Copy_64"

var cursorArgPattern = regexp.MustCompile(`after:\s*"([^"]*)"`)

// copyPageServer answers Get with rows chosen by page, and counts what the
// walk asked for and what it wrote. It stands in for a data node whose cursor
// ordering is not stable: the same `after` can be answered with a page that
// was already returned.
type copyPageServer struct {
	mu         sync.Mutex
	queries    int
	batches    int
	objects    int
	cursors    map[string]int
	unexpected []string
}

func (s *copyPageServer) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	reuse := 0
	for _, n := range s.cursors {
		reuse = max(reuse, n)
	}
	return fmt.Sprintf("queries=%d batches=%d objects=%d distinct cursors=%d max cursor reuse=%d unexpected=%d",
		s.queries, s.batches, s.objects, len(s.cursors), reuse, len(s.unexpected))
}

func (s *copyPageServer) counts() (queries, objects int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries, s.objects
}

// newCopyPageServer serves page(query, call) for every Get. Nothing in it
// consumes the cursor, so a page is free to repeat itself.
func newCopyPageServer(t *testing.T, page func(query string, call int) []any) (*copyPageServer, *weaviateRepository) {
	t.Helper()
	s := &copyPageServer{cursors: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			s.mu.Lock()
			s.queries++
			call := s.queries
			cursor := ""
			if match := cursorArgPattern.FindStringSubmatch(body.Query); match != nil {
				cursor = match[1]
			}
			s.cursors[cursor]++
			s.mu.Unlock()
			rows := page(body.Query, call)
			if rows == nil {
				rows = []any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"Get": map[string]any{copyTestCollection: rows}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batch/objects":
			var body struct {
				Objects []any `json:"objects"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.batches++
			s.objects += len(body.Objects)
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
	}))
	t.Cleanup(server.Close)
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	return s, &weaviateRepository{client: client, collectionBaseName: "Copy"}
}

func copyTestRow(id, chunkID string) map[string]any {
	return map[string]any{
		fieldContent:         "content of " + chunkID,
		fieldSourceID:        chunkID,
		fieldSourceType:      1,
		fieldChunkID:         chunkID,
		fieldKnowledgeID:     "know-1",
		fieldKnowledgeBaseID: "kb-src",
		fieldTagID:           "",
		"_additional":        map[string]any{"id": id, "vector": []any{0.5, 0.25}},
	}
}

func copyTestChunkMap(size int) map[string]string {
	m := make(map[string]string, size)
	for i := range size {
		m[fmt.Sprintf("chunk-%d", i)] = fmt.Sprintf("target-chunk-%d", i)
	}
	return m
}

func copyTestUUID(i int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
}

// A source that answers every cursor with the page it already returned must
// fail loudly. The cursor is the last id of the page, so such a source never
// advances it and the walk would otherwise replay the page - writing a fresh
// copy of it into the target each round - until the task deadline.
func TestCopyIndicesRejectsARepeatedPage(t *testing.T) {
	const pageSize = 64
	page := make([]any, 0, pageSize)
	for i := range pageSize {
		page = append(page, copyTestRow(copyTestUUID(i), fmt.Sprintf("chunk-%d", i)))
	}
	server, repo := newCopyPageServer(t, func(string, int) []any { return page })
	defer func() { t.Log("copy walk: " + server.summary()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := repo.CopyIndices(ctx, "kb-src", map[string]string{"know-1": "know-2"},
		copyTestChunkMap(pageSize), "kb-dst", 64, "doc")

	require.ErrorContains(t, err, "made no progress")
	queries, objects := server.counts()
	assert.Equal(t, 2, queries, "the walk must stop on the first repeated cursor")
	assert.Equal(t, pageSize, objects, "the repeated page must not be written twice")
}

// A source that always advances but never ends must not pin the copy task
// until its deadline either: the walk stops at the page cap and reports it.
func TestCopyIndicesStopsAtThePageCap(t *testing.T) {
	server, repo := newCopyPageServer(t, func(_ string, call int) []any {
		return []any{copyTestRow(copyTestUUID(call), "chunk-absent")}
	})
	defer func() { t.Log("copy walk: " + server.summary()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := repo.CopyIndices(ctx, "kb-src", map[string]string{"know-1": "know-2"},
		map[string]string{"chunk-0": "target-chunk-0"}, "kb-dst", 64, "doc")

	require.ErrorContains(t, err, "copy indices exceeded")
	queries, objects := server.counts()
	assert.Equal(t, maxCopyPaginationHops, queries, "the walk must stop at the page cap")
	assert.Zero(t, objects, "rows outside the chunk map are never written")
}
