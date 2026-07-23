package vlm

import (
	"testing"
)

// TestShouldShapeReasoning verifies VLM requests are reshaped for GPT-5 /
// o-series models purely by model name. The go-openai SDK rejects such
// requests client-side (ReasoningValidator) regardless of provider, so the
// decision must NOT depend on provider - including the generic / legacy
// inline-config path where no provider is set. The function taking only the
// model name (no provider argument) is itself the regression guard against
// re-introducing provider gating.
func TestShouldShapeReasoning(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		want      bool
	}{
		{"gpt-5", "gpt-5", true},
		{"gpt-5-mini", "gpt-5-mini", true},
		{"gpt-5.2", "gpt-5.2", true},
		{"GPT-5 mixed case", "GPT-5.4-Mini", true},
		{"o1", "o1", true},
		{"o3-mini", "o3-mini", true},
		{"o4-mini", "o4-mini", true},

		{"gpt-4o", "gpt-4o", false},
		{"gpt-4-turbo", "gpt-4-turbo", false},
		{"qwen-vl", "qwen-vl", false},
		{"empty model", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldShapeReasoning(tc.modelName); got != tc.want {
				t.Errorf("shouldShapeReasoning(%q) = %v, want %v", tc.modelName, got, tc.want)
			}
		})
	}
}
