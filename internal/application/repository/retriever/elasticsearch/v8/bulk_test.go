package v8

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	typesLocal "github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/elastic/go-elasticsearch/v8"
)

// bulkReasonSentinel stands for document content that a per-item error.reason
// can carry; the error returned to the caller must never include it.
const bulkReasonSentinel = "LEAK-SENTINEL-DOC-BODY"

// fakeBulkServer answers every request the v8 repository makes while BatchSave
// runs, and returns body for the _bulk call.
type fakeBulkServer struct {
	body  string
	calls int
}

func (f *fakeBulkServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			f.calls++
			_, _ = io.WriteString(w, f.body)
		case strings.HasSuffix(r.URL.Path, "/_mapping"):
			_, _ = io.WriteString(w,
				`{"xwrag_default":{"mappings":{"properties":{"chunk_id":{"type":"keyword"}}}}}`)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		default:
			_, _ = io.WriteString(w,
				`{"acknowledged":true,"shards_acknowledged":true,"index":"xwrag_default"}`)
		}
	}
}

// newBulkTestRepository builds the repository through its public constructor
// against a fake Elasticsearch that answers _bulk with body.
func newBulkTestRepository(t *testing.T, body string) (interfaces.RetrieveEngineRepository, *fakeBulkServer) {
	t.Helper()

	fake := &fakeBulkServer{body: body}
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)

	client, err := elasticsearch.NewTypedClient(
		elasticsearch.Config{Addresses: []string{server.URL}, DisableRetry: true},
	)
	require.NoError(t, err)

	return NewElasticsearchEngineRepository(client, nil, nil), fake
}

func bulkTestIndexInfos() []*typesLocal.IndexInfo {
	return []*typesLocal.IndexInfo{
		{
			Content: "alpha", SourceID: "src-1", SourceType: typesLocal.ChunkSourceType,
			ChunkID: "chunk-1", KnowledgeID: "k-1", KnowledgeBaseID: "kb-1", IsEnabled: true,
		},
		{
			Content: "beta", SourceID: "src-2", SourceType: typesLocal.ChunkSourceType,
			ChunkID: "chunk-2", KnowledgeID: "k-1", KnowledgeBaseID: "kb-1", IsEnabled: true,
		},
	}
}

func bulkTestParams() map[string]any {
	return map[string]any{
		"embedding": map[string][]float32{"src-1": {0.1, 0.2}, "src-2": {0.3, 0.4}},
	}
}

// TestBatchSaveReportsPerItemBulkErrors is a regression test for #3832:
// Elasticsearch answers an accepted _bulk request with HTTP 200, so a document
// rejected for a mapping mismatch, a read-only index or an item-level
// rejection is visible only in the response body. BatchSave must surface those
// per-item failures to the caller.
func TestBatchSaveReportsPerItemBulkErrors(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantContains []string
	}{
		{
			name: "all documents rejected",
			body: `{"took":1,"errors":true,"items":[` +
				`{"create":{"_id":"chunk-1","status":400,` +
				`"error":{"type":"mapper_parsing_exception","reason":"` + bulkReasonSentinel + `"}}},` +
				`{"create":{"_id":"chunk-2","status":429,` +
				`"error":{"type":"es_rejected_execution_exception","reason":"` + bulkReasonSentinel + `"}}}]}`,
			wantContains: []string{
				"2/2 documents failed",
				"chunk-1", "mapper_parsing_exception",
				"chunk-2", "es_rejected_execution_exception",
			},
		},
		{
			name: "partial failure",
			body: `{"took":1,"errors":true,"items":[` +
				`{"create":{"_id":"chunk-1","status":201,"result":"created"}},` +
				`{"create":{"_id":"chunk-2","status":400,` +
				`"error":{"type":"cluster_block_exception","reason":"` + bulkReasonSentinel + `"}}}]}`,
			wantContains: []string{"1/2 documents failed", "chunk-2", "cluster_block_exception"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, fake := newBulkTestRepository(t, tc.body)

			err := repo.BatchSave(context.Background(), bulkTestIndexInfos(), bulkTestParams())

			require.Positive(t, fake.calls, "the _bulk endpoint was never called")
			require.Error(t, err)
			for _, want := range tc.wantContains {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), bulkReasonSentinel,
				"the returned error must not carry error.reason")
		})
	}

	t.Run("all documents accepted", func(t *testing.T) {
		repo, fake := newBulkTestRepository(t, `{"took":1,"errors":false,"items":[`+
			`{"create":{"_id":"chunk-1","status":201,"result":"created"}},`+
			`{"create":{"_id":"chunk-2","status":201,"result":"created"}}]}`)

		err := repo.BatchSave(context.Background(), bulkTestIndexInfos(), bulkTestParams())

		require.Positive(t, fake.calls, "the _bulk endpoint was never called")
		require.NoError(t, err)
	})
}
