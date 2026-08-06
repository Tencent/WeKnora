package vlm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestRemoteAPIVLMPredictShapesReasoningModelRequest(t *testing.T) {
	requestBody := captureVLMRequest(t, "gpt-5-mini")

	if _, ok := requestBody["max_tokens"]; ok {
		t.Fatalf("reasoning-model request contains unsupported max_tokens: %v", requestBody)
	}
	if got := requestBody["max_completion_tokens"]; got != float64(defaultMaxToks) {
		t.Fatalf("max_completion_tokens = %v, want %d", got, defaultMaxToks)
	}
	if _, ok := requestBody["temperature"]; ok {
		t.Fatalf("reasoning-model request contains unsupported temperature: %v", requestBody)
	}
}

func TestRemoteAPIVLMPredictPreservesStandardModelRequest(t *testing.T) {
	requestBody := captureVLMRequest(t, "gpt-4o")

	if got := requestBody["max_tokens"]; got != float64(defaultMaxToks) {
		t.Fatalf("max_tokens = %v, want %d", got, defaultMaxToks)
	}
	if _, ok := requestBody["max_completion_tokens"]; ok {
		t.Fatalf("standard-model request contains max_completion_tokens: %v", requestBody)
	}
	if got, ok := requestBody["temperature"].(float64); !ok || math.Abs(got-float64(defaultTemp)) > 1e-6 {
		t.Fatalf("temperature = %v, want %v", got, defaultTemp)
	}
}

func captureVLMRequest(t *testing.T, modelName string) map[string]any {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	requestBody := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"caption"}}]}`))
	}))
	defer server.Close()

	client, err := NewRemoteAPIVLM(&Config{
		ModelName: modelName,
		ModelID:   modelName,
		APIKey:    "test-key",
		BaseURL:   server.URL,
		Provider:  "openai",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}
	if _, err := client.Predict(context.Background(), nil, "describe the image"); err != nil {
		t.Fatalf("Predict: %v", err)
	}

	return requestBody
}
