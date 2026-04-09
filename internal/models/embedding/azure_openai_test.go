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
			"https://example-resource.openai.azure.com/openai/deployments/text-embedding-3-large-deployment/embeddings?api-version=2024-10-21"; got != want {
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
		"text-embedding-3-large-deployment",
		511,
		256,
		"text-embedding-3-large",
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

func TestAzureOpenAIEmbedderBatchEmbedOmitsDimensionsForFixedSizeModels(t *testing.T) {
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

	embedder, err := NewAzureOpenAIEmbedder(
		"test-key",
		"https://example-resource.openai.azure.com",
		"ada-002-deployment",
		511,
		1536,
		"text-embedding-ada-002",
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

	if _, ok := requestBody["dimensions"]; ok {
		t.Fatalf("expected request body to omit dimensions for fixed-size model, got %v", requestBody)
	}
}

func TestAzureOpenAIEmbedderBatchEmbedOmitsDimensionsForOlderAPIVersion(t *testing.T) {
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

	embedder, err := NewAzureOpenAIEmbedder(
		"test-key",
		"https://example-resource.openai.azure.com",
		"text-embedding-3-large-deployment",
		511,
		256,
		"text-embedding-3-large",
		"2024-02-15-preview",
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
		t.Fatalf("expected request body to omit dimensions for older API versions, got %v", requestBody)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
