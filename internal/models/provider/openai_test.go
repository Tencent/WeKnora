package provider

import "testing"

// TestIsOpenAIReasoningOrGPT5Model 验证 GPT-5 / o-series 模型识别逻辑。
// 见 issue #1283：这些模型必须使用 max_completion_tokens 替代 max_tokens。
func TestIsOpenAIReasoningOrGPT5Model(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},

		{"gpt-5", "gpt-5", true},
		{"gpt-5-mini", "gpt-5-mini", true},
		{"gpt-5.2", "gpt-5.2", true},
		{"gpt-5.5-pro", "gpt-5.5-pro", true},
		{"gpt-5 mixed case", "GPT-5.4-Mini", true},

		{"o1", "o1", true},
		{"o1-mini", "o1-mini", true},
		{"o1-preview", "o1-preview", true},
		{"o3", "o3", true},
		{"o3-mini", "o3-mini", true},
		{"o4-mini", "o4-mini", true},

		{"gpt-4", "gpt-4", false},
		{"gpt-4o", "gpt-4o", false},
		{"gpt-4o-mini", "gpt-4o-mini", false},
		{"gpt-3.5-turbo", "gpt-3.5-turbo", false},

		{"name starting with 'o1' but not o-series", "olympus-1", false},
		{"name starting with 'openai-'", "openai-gpt-4", false},
		{"name starting with 'o3' but not o-series", "o3xtra", false},
		{"random", "qwen-max", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsOpenAIReasoningOrGPT5Model(tc.input)
			if got != tc.want {
				t.Errorf("IsOpenAIReasoningOrGPT5Model(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
