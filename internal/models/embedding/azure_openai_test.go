package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestAzureOpenAIEmbedderBatchEmbedSendsConfiguredDimensions(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		if got, want := r.URL.String(),
			"https://example-resource.openai.azure.com/openai/deployments/test-deployment/embeddings?api-version=2024-10-21"; got != want {
			t.Fatalf("unexpected request path: got %s want %s", got, want)
		}

		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewAzureOpenAIEmbedder(
		"test-key",
		"https://example-resource.openai.azure.com",
		"test-deployment",
		511,
		256,
		"model-id",
		"2024-10-21",
		nil,
	)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	embedder.httpClient = &http.Client{Transport: transport}

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}

	got, ok := requestBody["dimensions"]
	if !ok {
		t.Fatalf("expected request body to include dimensions, got %v", requestBody)
	}

	if got != float64(256) {
		t.Fatalf("unexpected dimensions value: got %v want 256", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
