package v7

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elastic/go-elasticsearch/v7"
	"github.com/stretchr/testify/require"
)

func TestMoveIndicesRequiresCompleteCounts(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		fails      bool
	}{
		{"complete", `{"total":100,"updated":100}`, false},
		{"empty retry", `{"total":0,"updated":0}`, false},
		{"partial", `{"total":100,"updated":40}`, true},
		{"missing total", `{"updated":40}`, true},
		{"missing updated", `{"total":40}`, true},
		{"missing counts", `{}`, true},
		{"negative", `{"total":-1,"updated":-1}`, true},
		{"conflict", `{"total":1,"updated":1,"version_conflicts":1}`, true},
		{"timeout", `{"total":1,"updated":1,"timed_out":true}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Elastic-Product", "Elasticsearch")
				if r.URL.Path == "/" {
					_, _ = w.Write(
						[]byte(
							`{"version":{"number":"7.17.0","build_flavor":"default"},"tagline":"You Know, for Search"}`,
						),
					)
					return
				}
				if r.URL.Path != "/vectors/_update_by_query" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client, err := elasticsearch.NewClient(
				elasticsearch.Config{Addresses: []string{server.URL}, DisableRetry: true},
			)
			require.NoError(t, err)
			repo := &elasticsearchRepository{client: client, index: "vectors"}
			err = repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, "file")
			if tt.fails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
