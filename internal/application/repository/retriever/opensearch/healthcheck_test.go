package opensearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/Tencent/WeKnora/internal/types"
)

// clusterHandler serves both GET / (version info) and /_cat/plugins, so the
// full TestConnection probe (version + k-NN plugin) can run end to end.
func clusterHandler(distribution, number string, plugins []osapi.CatPluginResp) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_cat/plugins" {
			_ = json.NewEncoder(w).Encode(plugins)
			return
		}
		_, _ = w.Write([]byte(`{"version":{"distribution":"` + distribution + `","number":"` + number + `"}}`))
	}
}

func TestTestConnection_Success(t *testing.T) {
	ts := httptest.NewServer(clusterHandler("opensearch", "3.3.2", []osapi.CatPluginResp{
		{Name: "node-1", Component: "opensearch-knn"},
	}))
	defer ts.Close()
	if err := TestConnection(context.Background(), &types.ConnectionConfig{Addr: ts.URL}); err != nil {
		t.Errorf("healthy cluster: want nil, got %v", err)
	}
}

func TestTestConnection_RejectsElasticsearch(t *testing.T) {
	ts := httptest.NewServer(clusterHandler("elasticsearch", "8.10.4", nil))
	defer ts.Close()
	if err := TestConnection(context.Background(), &types.ConnectionConfig{Addr: ts.URL}); err == nil {
		t.Error("elasticsearch cluster should be rejected")
	}
}

func TestTestConnection_EmptyAddr(t *testing.T) {
	if err := TestConnection(context.Background(), &types.ConnectionConfig{}); err == nil {
		t.Error("empty addr should be rejected")
	}
}
