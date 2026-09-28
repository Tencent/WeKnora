package weaviate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// weaviateErrorBody is how Weaviate words a refused schema request.
func weaviateErrorBody(message string) string {
	body, _ := json.Marshal(map[string]any{"error": []any{map[string]any{"message": message}}})
	return string(body)
}

// schemaTestServer stands in for Weaviate's schema API. A class exists once a
// create has been answered with 200; probe runs before every existence check.
type schemaTestServer struct {
	mu      sync.Mutex
	exists  bool
	probes  int
	creates int
	probe   func()
	create  func(attempt int) (int, string)
}

func (s *schemaTestServer) repository(t *testing.T) *weaviateRepository {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/schema/"):
			if s.probe != nil {
				s.probe()
			}
			s.mu.Lock()
			s.probes++
			exists := s.exists
			s.mu.Unlock()
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"class":"Weknora_embeddings_1024"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/schema":
			s.mu.Lock()
			s.creates++
			code, body := s.create(s.creates)
			if code == http.StatusOK {
				s.exists = true
			}
			s.mu.Unlock()
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/v1/meta":
			_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	return &weaviateRepository{client: client, collectionBaseName: "Weknora_embeddings"}
}

// The first batches written at a new dimension are saved by several workers at
// once, and each may find the class missing before any has created it. Only
// one create wins. Weaviate refuses the others with 422, in one of two wordings
// depending on whether they lose before or inside the Raft apply; neither may
// fail their writes. The barrier makes every worker see the class as missing,
// so the race happens every run.
func TestEnsureCollectionToleratesConcurrentCreation(t *testing.T) {
	const workers = 8
	var arrived atomic.Int32
	allProbed := make(chan struct{})
	server := &schemaTestServer{
		probe: func() {
			n := arrived.Add(1)
			if n == workers {
				close(allProbed)
			}
			if n > workers {
				return // a loser looking again after its 422
			}
			select {
			case <-allProbed:
			case <-time.After(5 * time.Second):
				t.Error("not every worker probed the class")
			}
		},
		create: func(attempt int) (int, string) {
			switch {
			case attempt == 1:
				return http.StatusOK, `{"class":"Weknora_embeddings_1024"}`
			case attempt%2 == 0:
				return http.StatusUnprocessableEntity,
					weaviateErrorBody("updating schema: TYPE_ADD_CLASS: class already exists")
			default:
				return http.StatusUnprocessableEntity,
					weaviateErrorBody("class name Weknora_embeddings_1024 already exists")
			}
		},
	}
	repo := server.repository(t)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.ensureCollection(context.Background(), 1024)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err, "a worker lost the creation race")
	}
	assert.Equal(t, workers, server.creates, "every worker should have attempted the create")
	_, ok := repo.initializedCollections.Load(1024)
	assert.True(t, ok, "dimension 1024 should be marked initialized")
}

// A 422 for any other reason leaves the class missing, so the write must fail
// rather than be recorded as initialized.
func TestEnsureCollectionFailsWhenTheCreateIsRejected(t *testing.T) {
	server := &schemaTestServer{create: func(int) (int, string) {
		return http.StatusUnprocessableEntity, weaviateErrorBody("invalid property: data type not supported")
	}}
	repo := server.repository(t)

	require.ErrorContains(t, repo.ensureCollection(context.Background(), 1024), "failed to create collection")
	assert.Equal(t, 2, server.probes, "the class should be looked up again after the 422")
	_, ok := repo.initializedCollections.Load(1024)
	assert.False(t, ok)
}

// Only a 422 means the create was refused; anything else is reported as it is.
func TestEnsureCollectionDoesNotRecheckOtherFailures(t *testing.T) {
	server := &schemaTestServer{create: func(int) (int, string) {
		return http.StatusInternalServerError, weaviateErrorBody("disk full")
	}}
	repo := server.repository(t)

	require.Error(t, repo.ensureCollection(context.Background(), 1024))
	assert.Equal(t, 1, server.probes)
}
