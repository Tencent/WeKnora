package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func captureOpenAIEmbeddingRequest(t *testing.T, modelName string, dimensions int, supportsDimensionOverride bool) map[string]any {
	t.Helper()

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
