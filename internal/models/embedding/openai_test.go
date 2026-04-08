package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestOpenAIEmbedderBatchEmbedDoesNotSendDimensions(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewOpenAIEmbedder(
		"test-key",
		"https://api.openai.com/v1",
		"text-embedding-3-large",
		511,
		1024,
		"model-id",
		nil,
	)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	embedder.httpClient = &http.Client{Transport: transport}

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}

	if _, ok := requestBody["dimensions"]; ok {
		t.Fatalf("expected OpenAI request body to omit dimensions, got %v", requestBody)
	}
}
