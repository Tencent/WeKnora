package v7

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elastic/go-elasticsearch/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// copySourceHits covers every source_id shape CopyIndices remaps: a regular
// chunk row, a generated-question row sharing that chunk, a row with an
// unrelated source_id (remapped to a fresh uuid), and a legacy row written
// before is_enabled existed.
const copySourceHits = `[
 {"_index":"vectors","_id":"d1","_score":1,"_source":{"content":"chunk","source_id":"c1","source_type":0,
  "chunk_id":"c1","knowledge_id":"k1","knowledge_base_id":"kb-src","embedding":[0.1,0.2],"is_enabled":true}},
 {"_index":"vectors","_id":"d2","_score":1,"_source":{"content":"question","source_id":"c1-q1","source_type":1,
  "chunk_id":"c1","knowledge_id":"k1","knowledge_base_id":"kb-src","embedding":[0.3,0.4],"is_enabled":true}},
 {"_index":"vectors","_id":"d3","_score":1,"_source":{"content":"summary","source_id":"img-9","source_type":2,
  "chunk_id":"c2","knowledge_id":"k1","knowledge_base_id":"kb-src","embedding":[0.5,0.6],"is_enabled":false}},
 {"_index":"vectors","_id":"d4","_score":1,"_source":{"content":"legacy","source_id":"c3","source_type":0,
  "chunk_id":"c3","knowledge_id":"k1","knowledge_base_id":"kb-src","embedding":[0.7,0.8]}}
]`

type copiedDoc struct {
	Content         string    `json:"content"`
	SourceID        string    `json:"source_id"`
	SourceType      int       `json:"source_type"`
	ChunkID         string    `json:"chunk_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Embedding       []float32 `json:"embedding"`
	IsEnabled       *bool     `json:"is_enabled"`
}

// fakeCopyServer serves one page of copySourceHits and records the search
// query and every document sent through _bulk.
type fakeCopyServer struct {
	mu          sync.Mutex
	searchQuery string
	docs        []copiedDoc
}

func (f *fakeCopyServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`{"version":{"number":"7.17.0","build_flavor":"default"},` +
				`"tagline":"You Know, for Search"}`))
		case "/vectors/_search":
			f.searchQuery = string(body)
			_, _ = w.Write([]byte(`{"took":1,"timed_out":false,` +
				`"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},` +
				`"hits":{"total":{"value":4,"relation":"eq"},"hits":` + copySourceHits + `}}`))
		case "/vectors/_bulk":
			sc := bufio.NewScanner(bytes.NewReader(body))
			sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
			for sc.Scan() {
				line := sc.Bytes()
				var probe map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(line, &probe))
				if _, ok := probe["content"]; !ok {
					continue // action line
				}
				var doc copiedDoc
				require.NoError(t, json.Unmarshal(line, &doc))
				f.docs = append(f.docs, doc)
			}
			_, _ = w.Write([]byte(`{"took":1,"errors":false,"items":[]}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}
}

func TestCopyIndicesKeepsVectorsAndEnabledState(t *testing.T) {
	fake := &fakeCopyServer{}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	client, err := elasticsearch.NewClient(
		elasticsearch.Config{Addresses: []string{server.URL}, DisableRetry: true},
	)
	require.NoError(t, err)
	repo := &elasticsearchRepository{client: client, index: "vectors"}

	err = repo.CopyIndices(context.Background(), "kb-src",
		map[string]string{"k1": "tk1"},
		map[string]string{"c1": "t1", "c2": "t2", "c3": "t3"},
		"kb-dst", 2, "manual")
	require.NoError(t, err)

	assert.NotContains(t, fake.searchQuery, "is_enabled",
		"the copy scan must include disabled source rows so they stay disabled, not vanish")

	require.Len(t, fake.docs, 4)
	byContent := make(map[string]copiedDoc, len(fake.docs))
	for _, d := range fake.docs {
		byContent[d.Content] = d
		assert.Equal(t, "tk1", d.KnowledgeID)
		assert.Equal(t, "kb-dst", d.KnowledgeBaseID)
		require.NotNil(t, d.IsEnabled, "is_enabled must be written for %q", d.Content)
	}

	chunk := byContent["chunk"]
	assert.Equal(t, "t1", chunk.ChunkID)
	assert.Equal(t, "t1", chunk.SourceID)
	assert.Equal(t, 0, chunk.SourceType)
	assert.Equal(t, []float32{0.1, 0.2}, chunk.Embedding)
	assert.True(t, *chunk.IsEnabled)

	question := byContent["question"]
	assert.Equal(t, "t1", question.ChunkID)
	assert.Equal(t, "t1-q1", question.SourceID)
	assert.Equal(t, 1, question.SourceType)
	assert.Equal(t, []float32{0.3, 0.4}, question.Embedding)
	assert.True(t, *question.IsEnabled)

	summary := byContent["summary"]
	assert.Equal(t, "t2", summary.ChunkID)
	assert.NotEqual(t, "img-9", summary.SourceID)
	assert.False(t, strings.HasPrefix(summary.SourceID, "t2"), "unrelated source ids get a fresh uuid")
	assert.Equal(t, 2, summary.SourceType)
	assert.Equal(t, []float32{0.5, 0.6}, summary.Embedding)
	assert.False(t, *summary.IsEnabled)

	legacy := byContent["legacy"]
	assert.Equal(t, "t3", legacy.SourceID)
	assert.Equal(t, []float32{0.7, 0.8}, legacy.Embedding)
	assert.True(t, *legacy.IsEnabled, "a source row without is_enabled is enabled for retrieval")
}
