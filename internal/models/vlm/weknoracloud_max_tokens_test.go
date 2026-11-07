package vlm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWeKnoraCloudVLMSendsHistoricalBudget pins the hosted endpoint at its own
// budget: raising the self-hosted OCR default must not change what
// WeKnoraCloud requests send, even when VLM_MAX_TOKENS is set process wide.
func TestWeKnoraCloudVLMSendsHistoricalBudget(t *testing.T) {
	t.Setenv("VLM_MAX_TOKENS", "32000")

	var lastBody weKnoraCloudVLMRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&lastBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	vlm := newTestWeKnoraCloudVLM(server.URL, server.Client())
	if _, err := vlm.Predict(context.Background(), nil, "describe this page"); err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	if lastBody.MaxTokens != weKnoraCloudMaxToks {
		t.Errorf("max_tokens = %d, want %d", lastBody.MaxTokens, weKnoraCloudMaxToks)
	}
}
