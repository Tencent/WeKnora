package vlm

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newVLMChatTestServer emulates an OpenAI-compatible chat completions endpoint
// and records the last decoded request body.
func newVLMChatTestServer(t *testing.T, lastRequest *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode VLM request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*lastRequest = req

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"choices": [
				{"index": 0, "message": {"role": "assistant", "content": "extracted text"}, "finish_reason": "stop"}
			]
		}`))
	}))
}

// testPNG is a minimal byte slice that http.DetectContentType reports as a PNG.
var testPNG = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 16))

// TestRemoteAPIVLMSendsMaxCompletionTokensForReasoningModel is the regression
// test for issue #2537: GPT-5 / o-series vision models reject max_tokens and
// every non-default sampling parameter. The catalog marks those families on
// the OpenAI vendor, and the generic vendor carries the same family entries
// so relays that forward to OpenAI behave the same way.
func TestRemoteAPIVLMSendsMaxCompletionTokensForReasoningModel(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	for _, provider := range []string{"openai", ""} {
		var lastRequest map[string]interface{}
		server := newVLMChatTestServer(t, &lastRequest)

		v, err := NewRemoteAPIVLM(&Config{
			BaseURL:   server.URL,
			ModelName: "gpt-5-nano",
			APIKey:    "sk-test",
			Provider:  provider,
		})
		if err != nil {
			t.Fatalf("NewRemoteAPIVLM: %v", err)
		}

		content, err := v.Predict(t.Context(), [][]byte{testPNG}, "extract the text")
		if err != nil {
			t.Fatalf("Predict (provider=%q): %v", provider, err)
		}
		if content != "extracted text" {
			t.Errorf("content = %q, want %q", content, "extracted text")
		}
		if _, ok := lastRequest["max_tokens"]; ok {
			t.Errorf("provider=%q: request carries max_tokens, which reasoning models reject: %v",
				provider, lastRequest["max_tokens"])
		}
		if got, ok := lastRequest["max_completion_tokens"]; !ok {
			t.Errorf("provider=%q: request is missing max_completion_tokens", provider)
		} else if got != float64(defaultMaxToks) {
			t.Errorf("max_completion_tokens = %v, want %d", got, defaultMaxToks)
		}
		if _, ok := lastRequest["temperature"]; ok {
			t.Errorf("provider=%q: request carries temperature, which reasoning models reject: %v",
				provider, lastRequest["temperature"])
		}
		server.Close()
	}
}

// TestRemoteAPIVLMKeepsMaxTokensForNonReasoningModel guards against the fix
// regressing ordinary vision models on an unknown (generic) endpoint, which
// still expect max_tokens.
func TestRemoteAPIVLMKeepsMaxTokensForNonReasoningModel(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newVLMChatTestServer(t, &lastRequest)
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "qwen2.5-vl-7b-instruct",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}

	if _, err := v.Predict(t.Context(), [][]byte{testPNG}, "extract the text"); err != nil {
		t.Fatalf("Predict: %v", err)
	}

	if got, ok := lastRequest["max_tokens"]; !ok {
		t.Error("request is missing max_tokens")
	} else if got != float64(defaultMaxToks) {
		t.Errorf("max_tokens = %v, want %d", got, defaultMaxToks)
	}
	if _, ok := lastRequest["max_completion_tokens"]; ok {
		t.Error("request carries max_completion_tokens for a non-reasoning model")
	}
	if got, ok := lastRequest["temperature"]; !ok {
		t.Error("request is missing temperature")
	} else if f, isFloat := got.(float64); !isFloat || math.Abs(f-float64(defaultTemp)) > 1e-6 {
		t.Errorf("temperature = %v, want %v", got, defaultTemp)
	}
	messages := lastRequest["messages"].([]interface{})
	parts := messages[0].(map[string]interface{})["content"].([]interface{})
	if len(parts) != 2 {
		t.Fatalf("user content parts = %d, want text + image", len(parts))
	}
	image := parts[1].(map[string]interface{})["image_url"].(map[string]interface{})
	if !strings.HasPrefix(image["url"].(string), "data:image/png;base64,") {
		t.Errorf("image url = %q, want a PNG data URI", image["url"])
	}
}

// TestRemoteAPIVLMReportsTruncatedCompletion covers the other way a reasoning
// model yields nothing: the completion budget also covers reasoning tokens, so
// an exhausted budget returns an empty message with finish_reason=length
// instead of an API error.
func TestRemoteAPIVLMReportsTruncatedCompletion(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"choices": [
				{"index": 0, "message": {"role": "assistant", "content": ""}, "finish_reason": "length"}
			]
		}`))
	}))
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "gpt-5-nano",
		APIKey:    "sk-test",
		Provider:  "openai",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}

	_, err = v.Predict(t.Context(), [][]byte{testPNG}, "extract the text")
	if err == nil {
		t.Fatal("Predict returned nil error for a truncated completion")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error = %q, want it to mention truncation", err.Error())
	}
}
