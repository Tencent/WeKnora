package opensearch

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMoveIndicesRequiresCompleteResult(t *testing.T) {
	for _, tt := range []struct {
		name, response string
		fails          bool
	}{
		{"complete", `{"total":2,"updated":2,"version_conflicts":0,"failures":[]}`, false},
		{"partial", `{"total":100,"updated":40,"failures":[]}`, true},
		{"empty", `{"total":0,"updated":0,"failures":[]}`, false},
		{"missing counts", `{"failures":[]}`, true},
		{"missing total", `{"updated":2,"failures":[]}`, true},
		{"missing updated", `{"total":2,"failures":[]}`, true},
		{"negative", `{"total":-1,"updated":-1,"failures":[]}`, true},
		{"conflict", `{"updated":1,"version_conflicts":1,"failures":[]}`, true},
		{"timeout", `{"timed_out":true,"version_conflicts":0,"failures":[]}`, true},
		{"failure", `{"failures":[{"id":"doc","cause":{"type":"unavailable"}}]}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, server := newTestRepo(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.response))
			})
			defer server.Close()
			err := repo.MoveKnowledgeIndices(
				context.Background(), "source", "target", "doc", []string{"chunk"}, 1, "file",
			)
			if tt.fails {
				require.ErrorIs(t, err, ErrTransport)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMoveIndicesIncludesOrphansEvenWithoutDatabaseChunks(t *testing.T) {
	var body map[string]any
	repo, server := newTestRepo(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode move request: %v", err)
		}
		_, _ = w.Write([]byte(`{"total":1,"updated":1,"failures":[]}`))
	})
	defer server.Close()
	require.NoError(t, repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 1, "file"))
	want := `{"bool":{"filter":[{"term":{"knowledge_base_id":"source"}},{"term":{"knowledge_id":"doc"}}]}}`
	query, err := json.Marshal(body["query"])
	require.NoError(t, err)
	require.JSONEq(t, want, string(query), "all vectors for this exact source document must be selected")
	script := body["script"].(map[string]any)
	require.Equal(t, map[string]any{"target": "target"}, script["params"])
}
