package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestJinaEmbedderBatchEmbedSendsConfiguredTaskByRole(t *testing.T) {
	var seen []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if task, ok := payload["task"].(string); ok {
			seen = append(seen, task)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[1,2,3],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewJinaEmbedder("key", "https://api.jina.ai/v1", "jina-embeddings-v5-text-small-retrieval", 0, 3, "model-id", nil)
	if err != nil {
		t.Fatalf("NewJinaEmbedder returned error: %v", err)
	}
	embedder.httpClient = &http.Client{Transport: transport}
	embedder.SetAsymmetricTasks("retrieval.query", "retrieval.passage", "", "")

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}, WithQueryInput()); err != nil {
		t.Fatalf("query BatchEmbed returned error: %v", err)
	}
	if _, err := embedder.BatchEmbed(context.Background(), []string{"world"}, WithDocumentInput()); err != nil {
		t.Fatalf("document BatchEmbed returned error: %v", err)
	}

	if len(seen) != 2 || seen[0] != "retrieval.query" || seen[1] != "retrieval.passage" {
		t.Fatalf("task values = %#v, want [retrieval.query retrieval.passage]", seen)
	}
}
