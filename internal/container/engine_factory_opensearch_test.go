package container

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

// osClusterHandler simulates an OpenSearch cluster for the driver's
// construction probe: GET / (version) + /_cat/plugins (k-NN on every node).
func osClusterHandler(distribution, number string, knnInstalled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_cat/plugins" {
			if knnInstalled {
				_, _ = w.Write([]byte(`[{"name":"node-1","component":"opensearch-knn"}]`))
			} else {
				_, _ = w.Write([]byte(`[{"name":"node-1","component":"opensearch-sql"}]`))
			}
			return
		}
		_, _ = w.Write([]byte(`{"version":{"distribution":"` + distribution + `","number":"` + number + `"}}`))
	}
}

func TestCreateOpenSearchEngine_WiresClientAndRepo(t *testing.T) {
	ts := httptest.NewServer(osClusterHandler("opensearch", "3.3.2", true))
	defer ts.Close()

	store := types.VectorStore{
		ID:               "", // env-style (no index prefix)
		EngineType:       types.OpenSearchRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{Addr: ts.URL},
	}
	svc, err := createOpenSearchEngine(context.Background(), store, nil)
	if err != nil {
		t.Fatalf("createOpenSearchEngine: %v", err)
	}
	if svc == nil {
		t.Fatal("want non-nil engine service")
	}
	if svc.EngineType() != types.OpenSearchRetrieverEngineType {
		t.Errorf("engine type: want opensearch, got %s", svc.EngineType())
	}
}

func TestCreateOpenSearchEngine_RejectsBadCluster(t *testing.T) {
	// Elasticsearch distribution must be rejected at construction.
	ts := httptest.NewServer(osClusterHandler("elasticsearch", "8.10.4", true))
	defer ts.Close()
	_, err := createOpenSearchEngine(context.Background(),
		types.VectorStore{EngineType: types.OpenSearchRetrieverEngineType,
			ConnectionConfig: types.ConnectionConfig{Addr: ts.URL}}, nil)
	if err == nil {
		t.Error("elasticsearch cluster should be rejected at engine creation")
	}
}

func TestCreateEngineServiceFromStore_OpenSearchCaseReached(t *testing.T) {
	ts := httptest.NewServer(osClusterHandler("opensearch", "2.11.0", true))
	defer ts.Close()
	svc, err := createEngineServiceFromStore(context.Background(),
		types.VectorStore{EngineType: types.OpenSearchRetrieverEngineType,
			ConnectionConfig: types.ConnectionConfig{Addr: ts.URL}},
		nil, &config.Config{}, nil)
	if err != nil {
		t.Fatalf("createEngineServiceFromStore (opensearch case): %v", err)
	}
	if svc == nil || svc.EngineType() != types.OpenSearchRetrieverEngineType {
		t.Errorf("opensearch case not wired correctly: %v", svc)
	}
}

// TestInitRetrieveEngineRegistry_OpenSearchEnvPath exercises the
// RETRIEVE_DRIVER=opensearch env-path registration block end to end.
func TestInitRetrieveEngineRegistry_OpenSearchEnvPath(t *testing.T) {
	ts := httptest.NewServer(osClusterHandler("opensearch", "3.3.2", true))
	defer ts.Close()

	// In-memory DB: the vector_stores table is absent, so loadDBStores logs
	// and returns (non-fatal) — only the env-path block matters here.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-mem db: %v", err)
	}

	t.Setenv("RETRIEVE_DRIVER", "opensearch")
	t.Setenv("OPENSEARCH_ADDR", ts.URL)

	registry, err := initRetrieveEngineRegistry(db, &config.Config{}, &fakeAuditSvc{})
	if err != nil {
		t.Fatalf("initRetrieveEngineRegistry: %v", err)
	}
	if _, err := registry.GetRetrieveEngineService(types.OpenSearchRetrieverEngineType); err != nil {
		t.Errorf("opensearch engine not registered via env path: %v", err)
	}
}
