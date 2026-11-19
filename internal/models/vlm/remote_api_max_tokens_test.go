package vlm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestVLMMaxTokensReadsEnv pins the parsing rules of VLM_MAX_TOKENS: a set and
// positive value wins, everything else falls back to the built-in budget.
func TestVLMMaxTokensReadsEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "unset", env: "", want: defaultMaxToks},
		{name: "blank", env: "   ", want: defaultMaxToks},
		{name: "positive", env: "12000", want: 12000},
		{name: "padded positive", env: " 8000 ", want: 8000},
		{name: "zero", env: "0", want: defaultMaxToks},
		{name: "negative", env: "-1", want: defaultMaxToks},
		{name: "not a number", env: "lots", want: defaultMaxToks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VLM_MAX_TOKENS", tc.env)
			if got := vlmMaxTokens(); got != tc.want {
				t.Errorf("vlmMaxTokens() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRemoteAPIVLMSendsConfiguredMaxTokens is the regression test for the
// hard-coded 5000-token budget: a raised VLM_MAX_TOKENS must reach the request
// body instead of being ignored.
func TestRemoteAPIVLMSendsConfiguredMaxTokens(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	t.Setenv("VLM_MAX_TOKENS", "12000")

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

	got, ok := lastRequest["max_tokens"]
	if !ok {
		t.Fatal("request is missing max_tokens")
	}
	if got != float64(12000) {
		t.Errorf("max_tokens = %v, want 12000", got)
	}
}

// TestRemoteAPIVLMTruncationReportsConfiguredMaxTokens checks that the
// truncation error quotes the budget that was actually requested, so an
// operator reading the log knows which cap to raise.
func TestRemoteAPIVLMTruncationReportsConfiguredMaxTokens(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	t.Setenv("VLM_MAX_TOKENS", "7000")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": ""},
				"finish_reason": "length",
			}},
		})
	}))
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{BaseURL: server.URL, ModelName: "generic-vision", Provider: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Predict(t.Context(), [][]byte{testPNG}, "extract text")
	if got != "" {
		t.Fatalf("content = %q, want empty", got)
	}
	if !errors.Is(err, ErrTruncatedCompletion) {
		t.Fatalf("error = %v, want ErrTruncatedCompletion", err)
	}
	if want := fmt.Sprintf("at %s tokens", strconv.Itoa(7000)); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to mention the configured budget (%s)", err, want)
	}
}
