package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	wvrepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/weaviate"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// ---------------------------------------------------------------------------
// Regression for the review of #3844: the replacement invariant has to hold
// against the real Weaviate repository, not only against a service double that
// returns an error. Weaviate answers a batch delete it could not fully perform
// with HTTP 200 and the outcome in the body, so the service-level chain
// (purgeStaleIndexEntries -> dropStaleImageChunks -> processImage) is only
// reliable once the repository reads that body and turns it into an error.
// ---------------------------------------------------------------------------

// weaviatePurgeServer stands in for Weaviate during a retry of one image. It
// answers the batch delete the way the real endpoint words it and records the
// source ids the service asked to purge.
type weaviatePurgeServer struct {
	mu sync.Mutex
	// deletes counts the batch deletes that reached the server.
	deletes int
	// sourceIDs is the valueTextArray of the last batch delete filter.
	sourceIDs []string
	// matches, successful and failed are the counts the response reports.
	matches    int64
	successful int64
	failed     int64
}

func (s *weaviatePurgeServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/batch/objects":
			var req struct {
				Match struct {
					Class string `json:"class"`
					Where struct {
						Path  []string `json:"path"`
						Value []string `json:"valueTextArray"`
					} `json:"where"`
				} `json:"match"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, "Svc_3", req.Match.Class)
			assert.Equal(t, []string{"source_id"}, req.Match.Where.Path)
			s.mu.Lock()
			s.deletes++
			s.sourceIDs = req.Match.Where.Value
			body, _ := json.Marshal(map[string]any{
				"output": "minimal",
				"results": map[string]any{
					"matches":    s.matches,
					"successful": s.successful,
					"failed":     s.failed,
					"limit":      10000,
					"objects":    []any{},
				},
			})
			s.mu.Unlock()
			_, _ = w.Write(body)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batch/objects":
			// The index write of the replacement chunks, reached only after a
			// purge the backend acknowledged.
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/schema/"):
			_, _ = w.Write([]byte(`{"class":"Svc_3"}`))
		case r.URL.Path == "/v1/meta":
			_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// newWeaviatePurgeBackend builds the real Weaviate repository around the test
// server, so the service runs the same SDK and repository code as production.
func newWeaviatePurgeBackend(
	t *testing.T, server *weaviatePurgeServer,
) interfaces.RetrieveEngineRepository {
	t.Helper()
	httpServer := httptest.NewServer(server.handler(t))
	t.Cleanup(httpServer.Close)
	client, err := sdk.NewClient(sdk.Config{
		Scheme: "http", Host: strings.TrimPrefix(httpServer.URL, "http://"),
	})
	require.NoError(t, err)
	return wvrepo.NewWeaviateRetrieveEngineRepository(
		client, &types.IndexConfig{CollectionPrefix: "Svc"})
}

// TestProcessImageRepairKeepsStaleRowsWhenWeaviateReportsFailedDeletions is the
// backend-level regression the review asked for. The real repository is wired to
// an HTTP server that answers the batch delete with HTTP 200 and failed=1,
// successful=0 — the shape the SDK reports as a nil error. The purge must fail,
// the old chunk row must survive (it is the only record of the source id still
// to purge) and no replacement chunk may be written.
func TestProcessImageRepairKeepsStaleRowsWhenWeaviateReportsFailedDeletions(t *testing.T) {
	t.Parallel()

	server := &weaviatePurgeServer{matches: 1, successful: 0, failed: 1}
	backend := newWeaviatePurgeBackend(t, server)
	h := newIdxHarnessWithEngine(
		t, retriever.NewKVHybridRetrieveEngine(backend, idxEngineType), defaultIndexKB())

	stale := idxImageChunk(
		"stale-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	h.repo.byID[stale.ID] = stale

	_, err := h.runRetry(t)

	// Not require: the state assertions below are the invariant itself, and a
	// run that lost the error must still show what it did to the rows.
	assert.Error(t, err, "a batch delete Weaviate could not complete must fail the purge")
	assert.ErrorContains(t, err, "1 object(s) still in Svc_3",
		"the error must name how many objects were not deleted")
	// The service reached the wire: this test does not pass because the request
	// never left, or because a mock failed earlier for another reason.
	assert.Equal(t, 1, server.deletes)
	assert.Equal(t, []string{stale.ID}, server.sourceIDs)

	if _, present := h.repo.byID[stale.ID]; !present {
		t.Errorf("stale chunk %s was deleted although its index entry survived", stale.ID)
	}
	if len(h.repo.deleted) != 0 {
		t.Errorf("deleted chunks = %v, want none while an index entry survives", h.repo.deleted)
	}
	if len(h.repo.created) != 0 {
		t.Errorf("persisted chunks = %d, want none when the purge failed", len(h.repo.created))
	}
	if h.repo.updates != 0 {
		t.Errorf("chunk status updates = %d, want none when the purge failed", h.repo.updates)
	}
}

// TestProcessImageRepairReplacesChunksWhenWeaviateConfirmsDeletion is the
// control for the test above: the same wiring, with the deletion acknowledged,
// drops the old row and writes the replacement. It keeps the regression from
// passing merely because this backend never lets the retry through.
func TestProcessImageRepairReplacesChunksWhenWeaviateConfirmsDeletion(t *testing.T) {
	t.Parallel()

	server := &weaviatePurgeServer{matches: 1, successful: 1, failed: 0}
	backend := newWeaviatePurgeBackend(t, server)
	h := newIdxHarnessWithEngine(
		t, retriever.NewKVHybridRetrieveEngine(backend, idxEngineType), defaultIndexKB())

	stale := idxImageChunk(
		"stale-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	h.repo.byID[stale.ID] = stale

	if _, err := h.runRetry(t); err != nil {
		t.Fatalf("processImage: %v", err)
	}

	assert.Equal(t, 1, server.deletes)
	assert.Equal(t, []string{stale.ID}, server.sourceIDs)
	if _, present := h.repo.byID[stale.ID]; present {
		t.Errorf("stale chunk %s survived an acknowledged purge", stale.ID)
	}
	assert.Equal(t, []string{stale.ID}, h.repo.deleted)
	assert.Len(t, h.repo.created, 1, "the replacement chunk must be written")
}
