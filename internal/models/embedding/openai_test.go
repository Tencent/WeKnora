package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIEmbedderBatchEmbedSendsConfiguredInputTypeByRole(t *testing.T) {
	var seen []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if inputType, ok := payload["input_type"].(string); ok {
			seen = append(seen, inputType)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"embedding":[1,2,3],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewOpenAIEmbedder("key", "https://example.com/v1", "text-embedding", 128, 3, "model-id", nil)
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder returned error: %v", err)
	}
	embedder.httpClient = &http.Client{Transport: transport}
	embedder.SetAsymmetricInputTypes("query", "passage")

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}, WithQueryInput()); err != nil {
		t.Fatalf("query BatchEmbed returned error: %v", err)
	}
	if _, err := embedder.BatchEmbed(context.Background(), []string{"world"}, WithDocumentInput()); err != nil {
		t.Fatalf("document BatchEmbed returned error: %v", err)
	}

	if len(seen) != 2 || seen[0] != "query" || seen[1] != "passage" {
		t.Fatalf("input_type values = %#v, want [query passage]", seen)
	}
}
