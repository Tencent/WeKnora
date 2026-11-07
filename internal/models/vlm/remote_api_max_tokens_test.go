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

// TestVLMMaxTokensReadsEnv pins the parsing rules of the VLM_MAX_TOKENS
// layer: a set and positive value is reported as the process-wide override,
// everything else reports 0 so the budget falls through to the remaining
// layers.
func TestVLMMaxTokensReadsEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "unset", env: "", want: 0},
		{name: "blank", env: "   ", want: 0},
		{name: "positive", env: "12000", want: 12000},
		{name: "padded positive", env: " 8000 ", want: 8000},
		{name: "zero", env: "0", want: 0},
		{name: "negative", env: "-1", want: 0},
		{name: "not a number", env: "lots", want: 0},
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

// TestRemoteAPIBudget pins the precedence of the OCR completion budget: a
// valid extra_config.max_tokens (this model) beats a valid VLM_MAX_TOKENS
// (this process), and both beat the built-in default capped by the output
// limit the model row declares.
func TestRemoteAPIBudget(t *testing.T) {
	cases := []struct {
		name       string
		extra      int
		env        int
		modelLimit int
		want       int
	}{
		{name: "default when nothing is set", want: defaultMaxToks},
		{name: "row limit below the default caps it", modelLimit: 8192, want: 8192},
		{name: "row limit above the default is ignored", modelLimit: 384000, want: defaultMaxToks},
		{name: "env override wins over the row cap", env: 12000, modelLimit: 8192, want: 12000},
		{name: "per-model override wins over everything", extra: 32000, env: 12000, modelLimit: 8192, want: 32000},
		{name: "invalid per-model override is ignored", extra: -5, modelLimit: 8192, want: 8192},
		{name: "non-positive env override is ignored", modelLimit: 4096, want: 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remoteAPIBudget(tc.extra, tc.env, tc.modelLimit)
			if got != tc.want {
				t.Errorf("remoteAPIBudget(%d, %d, %d) = %d, want %d",
					tc.extra, tc.env, tc.modelLimit, got, tc.want)
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

// TestRemoteAPIVLMSendsExtraConfigMaxTokens is the regression test for the
// silent page drops on dense scans: extra_config.max_tokens is the per-model
// override and must reach the request body, winning over VLM_MAX_TOKENS. An
// invalid value must fall through instead of breaking the request.
func TestRemoteAPIVLMSendsExtraConfigMaxTokens(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	cases := []struct {
		name  string
		value string
		want  float64
	}{
		{name: "valid override wins over the env value", value: "32000", want: 32000},
		{name: "invalid override falls through", value: "lots", want: 12000},
		{name: "non-positive override falls through", value: "0", want: 12000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VLM_MAX_TOKENS", "12000")

			var lastRequest map[string]interface{}
			server := newVLMChatTestServer(t, &lastRequest)
			defer server.Close()

			v, err := NewRemoteAPIVLM(&Config{
				BaseURL:   server.URL,
				ModelName: "deepseek-vl",
				APIKey:    "sk-test",
				Extra:     map[string]any{"max_tokens": tc.value},
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
			if got != tc.want {
				t.Errorf("max_tokens = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRemoteAPIVLMCapsBudgetByModelRow checks that a declared output limit on
// the model row lowers the built-in default (protecting endpoints that reject
// oversized max_tokens), while a limit above the default leaves it alone.
func TestRemoteAPIVLMCapsBudgetByModelRow(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	cases := []struct {
		name       string
		modelLimit int
		want       float64
	}{
		{name: "row limit below the default caps it", modelLimit: 8192, want: 8192},
		{name: "row limit above the default keeps the default", modelLimit: 384000, want: float64(defaultMaxToks)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lastRequest map[string]interface{}
			server := newVLMChatTestServer(t, &lastRequest)
			defer server.Close()

			v, err := NewRemoteAPIVLM(&Config{
				BaseURL:         server.URL,
				ModelName:       "deepseek-vision",
				APIKey:          "sk-test",
				MaxOutputTokens: tc.modelLimit,
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
			if got != tc.want {
				t.Errorf("max_tokens = %v, want %v", got, tc.want)
			}
		})
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
