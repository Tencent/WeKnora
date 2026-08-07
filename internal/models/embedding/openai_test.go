package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenAIEmbedderBatchEmbedOmitsDimensionsByDefault(t *testing.T) {
	requestBody := captureOpenAIEmbeddingRequest(t, "text-embedding-3-small", 256, false)

	if _, ok := requestBody["dimensions"]; ok {
		t.Fatalf("expected request body to omit dimensions by default, got %v", requestBody)
	}
}

func TestOpenAIEmbedderBatchEmbedSendsDimensionsWhenOverrideEnabled(t *testing.T) {
	requestBody := captureOpenAIEmbeddingRequest(t, "text-embedding-3-small", 256, true)

	got, ok := requestBody["dimensions"]
	if !ok {
		t.Fatalf("expected request body to include dimensions, got %v", requestBody)
	}
	if got != float64(256) {
		t.Fatalf("unexpected dimensions value: got %v want 256", got)
	}
}

func TestOpenAIEmbedderBatchEmbedOmitsDimensionsForOpenAICompatibleModels(t *testing.T) {
	requestBody := captureOpenAIEmbeddingRequest(t, "text-embedding-v3", 1024, false)

	if _, ok := requestBody["dimensions"]; ok {
		t.Fatalf("expected request body to omit dimensions for OpenAI-compatible model, got %v", requestBody)
	}
}

func TestOpenAIEmbedderBatchEmbedOmitsDimensionsForFixedSizeModels(t *testing.T) {
	requestBody := captureOpenAIEmbeddingRequest(t, "text-embedding-ada-002", 1536, false)

	if _, ok := requestBody["dimensions"]; ok {
		t.Fatalf("expected request body to omit dimensions for fixed-size model, got %v", requestBody)
	}
}

func TestOpenAIEmbedderRetries429WithRetryAfter(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`))
	}))
	defer server.Close()

	embedder, err := NewOpenAIEmbedder("test-key", server.URL, "text-embedding-3-small", 511, 0, "m1", nil)
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	got, err := embedder.BatchEmbed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("unexpected embeddings: %#v", got)
	}
	if hits.Load() < 2 {
		t.Fatalf("hits = %d, want >= 2 (429 then success)", hits.Load())
	}
}

func TestOpenAIEmbedderExhausts429Retries(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"still limited"}`))
	}))
	defer server.Close()

	embedder, err := NewOpenAIEmbedder("test-key", server.URL, "text-embedding-3-small", 511, 0, "m1", nil)
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	// Force a single attempt so the test stays fast.
	embedder.maxRetries = 0
	_, err = embedder.BatchEmbed(context.Background(), []string{"hello"})
	if !IsRateLimitError(err) {
		t.Fatalf("err = %v, want RateLimitError", err)
	}
}

func captureOpenAIEmbeddingRequest(t *testing.T, modelName string, dimensions int, supportsDimensionOverride bool) map[string]any {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	requestBody := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`))
	}))
	defer server.Close()

	embedder, err := NewOpenAIEmbedder(
		"test-key",
		server.URL,
		modelName,
		511,
		dimensions,
		"8f7d6082-5a15-4f84-ae55-88b2bdac4ba0",
		nil,
	)
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	embedder.SetSupportsDimensionOverride(supportsDimensionOverride)

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}

	return requestBody
}
