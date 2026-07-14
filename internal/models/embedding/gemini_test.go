package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiEmbedderBatchEmbedUsesNativeAPI(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	var gotPath string
	var gotAPIKey string
	var gotReq geminiBatchEmbedRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"embeddings": [
				{"values": [0.1, 0.2]},
				{"values": [0.3, 0.4]}
			]
		}`))
	}))
	defer server.Close()

	embedder, err := NewGeminiEmbedder("test-key", server.URL+"/openai", "gemini-embedding-2",
		0, 768, "model-id", nil)
	if err != nil {
		t.Fatalf("NewGeminiEmbedder: %v", err)
	}

	embeddings, err := embedder.BatchEmbed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}

	if gotPath != "/models/gemini-embedding-2:batchEmbedContents" {
		t.Fatalf("path = %q, want native batchEmbedContents path", gotPath)
	}
	if gotAPIKey != "test-key" {
		t.Fatalf("x-goog-api-key = %q", gotAPIKey)
	}
	if len(gotReq.Requests) != 2 {
		t.Fatalf("requests len = %d", len(gotReq.Requests))
	}
	if gotReq.Requests[0].Model != "models/gemini-embedding-2" {
		t.Fatalf("request model = %q", gotReq.Requests[0].Model)
	}
	if gotReq.Requests[0].OutputDimensionality != 0 {
		t.Fatalf("output_dimensionality = %d, want omitted by default", gotReq.Requests[0].OutputDimensionality)
	}
	if gotReq.Requests[0].Content.Parts[0].Text != "hello" {
		t.Fatalf("first text = %q", gotReq.Requests[0].Content.Parts[0].Text)
	}
	if len(embeddings) != 2 || len(embeddings[0]) != 2 || embeddings[1][1] != 0.4 {
		t.Fatalf("unexpected embeddings: %#v", embeddings)
	}
}

func TestGeminiEmbedderBatchEmbedSendsOutputDimensionalityWhenOverrideEnabled(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	var gotReq geminiBatchEmbedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	embedder, err := NewGeminiEmbedder("test-key", server.URL, "gemini-embedding-2",
		0, 768, "model-id", nil)
	if err != nil {
		t.Fatalf("NewGeminiEmbedder: %v", err)
	}
	embedder.SetSupportsDimensionOverride(true)

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}
	if gotReq.Requests[0].OutputDimensionality != 768 {
		t.Fatalf("output_dimensionality = %d, want 768", gotReq.Requests[0].OutputDimensionality)
	}
}

func TestGeminiEmbedderReturnsAPIErrorBody(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	embedder, err := NewGeminiEmbedder("test-key", server.URL, "gemini-embedding-2",
		0, 0, "model-id", nil)
	if err != nil {
		t.Fatalf("NewGeminiEmbedder: %v", err)
	}

	_, err = embedder.BatchEmbed(context.Background(), []string{"hello"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}
