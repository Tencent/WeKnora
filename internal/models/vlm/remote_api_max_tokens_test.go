package vlm

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestRemoteAPIVLMMaxTokens is the regression test for issue #4692: dense OCR
// pages ran out of the old fixed 5000-token budget and the chunk was dropped.
// The budget now defaults higher, is capped by the model's own output limit,
// and can be set per model with extra_config.max_tokens.
func TestRemoteAPIVLMMaxTokens(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	tests := []struct {
		name            string
		extra           map[string]any
		maxOutputTokens int
		want            int
	}{
		{name: "default", want: defaultMaxToks},
		{name: "capped by model limit", maxOutputTokens: 8000, want: 8000},
		{name: "model limit above default", maxOutputTokens: 65536, want: defaultMaxToks},
		{name: "extra_config override", extra: map[string]any{"max_tokens": "32000"}, maxOutputTokens: 8000, want: 32000},
		{name: "invalid override ignored", extra: map[string]any{"max_tokens": "lots"}, want: defaultMaxToks},
		{name: "non-positive override ignored", extra: map[string]any{"max_tokens": "0"}, maxOutputTokens: 8000, want: 8000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lastRequest map[string]interface{}
			server := newVLMChatTestServer(t, &lastRequest)
			defer server.Close()

			v, err := NewRemoteAPIVLM(&Config{
				BaseURL:         server.URL,
				ModelName:       "qwen2.5-vl-7b-instruct",
				APIKey:          "sk-test",
				Extra:           tt.extra,
				MaxOutputTokens: tt.maxOutputTokens,
			})
			if err != nil {
				t.Fatalf("NewRemoteAPIVLM: %v", err)
			}
			if _, err := v.Predict(t.Context(), [][]byte{testPNG}, "extract the text"); err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if got := lastRequest["max_tokens"]; got != float64(tt.want) {
				t.Errorf("max_tokens = %v, want %d", got, tt.want)
			}
		})
	}
}

func TestConfigFromModel_MaxOutputTokens(t *testing.T) {
	row := &types.Model{Name: "vl", Source: types.ModelSourceRemote, Parameters: types.ModelParameters{
		MaxOutputTokens: 8000,
		Spec:            &types.ModelSpecOverride{MaxOutputTokens: 4000},
	}}
	if got := ConfigFromModel(row, "", "").MaxOutputTokens; got != 8000 {
		t.Errorf("row field: MaxOutputTokens = %d, want 8000", got)
	}

	spec := &types.Model{Name: "vl", Source: types.ModelSourceRemote, Parameters: types.ModelParameters{
		Spec: &types.ModelSpecOverride{MaxOutputTokens: 4000},
	}}
	if got := ConfigFromModel(spec, "", "").MaxOutputTokens; got != 4000 {
		t.Errorf("spec fallback: MaxOutputTokens = %d, want 4000", got)
	}

	unset := &types.Model{Name: "vl", Source: types.ModelSourceRemote}
	if got := ConfigFromModel(unset, "", "").MaxOutputTokens; got != 0 {
		t.Errorf("unset: MaxOutputTokens = %d, want 0", got)
	}
}
