package weaviate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdk "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

func TestMoveKnowledgeIndicesDrainsBeyondOffsetLimitAndRetries(t *testing.T) {
	const count = 10005
	var mu sync.Mutex
	remaining := make(map[string]bool, count)
	for i := range count {
		remaining[fmt.Sprintf("00000000-0000-0000-0000-%012d", i)] = true
	}
	failed := false
	patches, queries := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/graphql":
			queries++
			var body struct {
				Query string `json:"query"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(400)
				return
			}
			assert.Contains(t, body.Query, "knowledge_base_id")
			assert.Contains(t, body.Query, "knowledge_id")
			assert.Contains(t, body.Query, "source")
			assert.Contains(t, body.Query, "doc")
			if strings.Contains(body.Query, "offset") || strings.Contains(body.Query, "after") {
				_ = json.NewEncoder(w).
					Encode(map[string]any{"errors": []any{map[string]any{"message": "pagination unsupported"}}})
				return
			}
			rows := make([]any, 0, 100)
			for id := range remaining {
				rows = append(rows, map[string]any{"_additional": map[string]any{"id": id}})
				if len(rows) == 100 {
					break
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"Get": map[string]any{"Move_3": rows}}})
		case r.Method == http.MethodPatch:
			id := path.Base(r.URL.Path)
			assert.True(t, remaining[id], "only source-filtered IDs should be moved")
			var body struct {
				Properties map[string]any `json:"properties"`
				Vector     []float32      `json:"vector"`
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(400)
				return
			}
			assert.Equal(t, map[string]any{fieldKnowledgeBaseID: "target", fieldTagID: ""}, body.Properties)
			assert.Empty(t, body.Vector, "metadata merge must preserve existing vectors")
			if patches == 137 && !failed {
				failed = true
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			patches++
			delete(remaining, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			// SDK probes the server version before object writes.
			if r.URL.Path == "/v1/meta" {
				_, _ = w.Write([]byte(`{"version":"1.30.0"}`))
				return
			}
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	repo := &weaviateRepository{client: client, collectionBaseName: "Move"}
	require.Error(t, repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, ""))
	mu.Lock()
	left := len(remaining)
	mu.Unlock()
	require.Equal(t, count-137, left)
	require.NoError(t, repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, ""))
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, remaining)
	require.Equal(t, count, patches)
	require.Greater(t, queries, 100)
}

func TestMoveKnowledgeIndicesRejectsNoProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/graphql":
			_, _ = w.Write(
				[]byte(`{"data":{"Get":{"Move_3":[{"_additional":{"id":"00000000-0000-0000-0000-000000000001"}}]}}}`),
			)
		case "/v1/meta":
			_, _ = w.Write([]byte(`{"version":"1.30.0"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	client, err := sdk.NewClient(sdk.Config{Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://")})
	require.NoError(t, err)
	repo := &weaviateRepository{client: client, collectionBaseName: "Move"}
	require.ErrorContains(
		t,
		repo.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", nil, 3, ""),
		"no progress",
	)
}
