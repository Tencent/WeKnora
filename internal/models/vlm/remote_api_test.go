package vlm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestRemoteAPIVLMPredictUsesCompletionTokensForReasoningModels(t *testing.T) {
	var gotRequest openai.ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":0,
			"model":"gpt-5-mini",
			"choices":[{"index":0,"message":{"role":"assistant","content":"described"},"finish_reason":"stop"}]
		}`))
	}))
	defer server.Close()

	config := openai.DefaultConfig("test-key")
	config.BaseURL = server.URL
	model := &RemoteAPIVLM{
		modelName:   "gpt-5-mini",
		client:      openai.NewClientWithConfig(config),
		baseURL:     server.URL,
		temperature: defaultTemp,
	}

	result, err := model.Predict(context.Background(), [][]byte{{0x89, 'P', 'N', 'G'}}, "describe")
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	if result != "described" {
		t.Fatalf("Predict() = %q, want described", result)
	}
	if gotRequest.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0", gotRequest.MaxTokens)
	}
	if gotRequest.MaxCompletionTokens != defaultMaxToks {
		t.Errorf("MaxCompletionTokens = %d, want %d", gotRequest.MaxCompletionTokens, defaultMaxToks)
	}
	if gotRequest.Temperature != 0 {
		t.Errorf("Temperature = %v, want 0", gotRequest.Temperature)
	}
}
