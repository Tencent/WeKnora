package vlm

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestConfigFromModelCarriesSpecOverride pins the per-row catalog override on
// the VLM path. dto.NewModelResponse computes a VLLM row's advertised
// capabilities from Parameters.Spec, so dropping the override here made the UI
// describe a model the runtime never built: /models reported the pinned
// protocol and levels while the request went out with the vendor defaults.
func TestConfigFromModelCarriesSpecOverride(t *testing.T) {
	override := &types.ModelSpecOverride{MaxOutputTokens: 4096}
	m := &types.Model{
		ID:     "vlm-1",
		Name:   "gpt-4o",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:  "https://api.example.com/v1",
			Provider: "openai",
			Spec:     override,
		},
	}
	cfg := ConfigFromModel(m, "", "")
	if cfg.Spec != override {
		t.Fatalf("Spec not propagated: %+v", cfg.Spec)
	}
}

// TestRemoteAPIVLMAppliesSpecOverride checks the override actually reaches the
// wire: pinning max_output_tokens on the row must clamp the VLM's own default
// completion budget.
func TestRemoteAPIVLMAppliesSpecOverride(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")

	var lastRequest map[string]interface{}
	server := newVLMChatTestServer(t, &lastRequest)
	defer server.Close()

	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "some-unlisted-vision-model",
		APIKey:    "sk-test",
		Provider:  "openai",
		Spec:      &types.ModelSpecOverride{API: "openai-responses"},
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}
	// The override selects a different protocol, which is only observable
	// through the request shape the server receives.
	if _, err := v.Predict(context.Background(), [][]byte{testPNG}, "read this"); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if _, ok := lastRequest["input"]; !ok {
		t.Errorf("spec.api override ignored: expected an openai-responses body, got %v keys", lastRequest)
	}
}
