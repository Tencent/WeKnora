package invoke

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- 模型名谓词（providers.go 声明面的直接覆盖；审查轮 T-5 补齐） ---
// 注：qwen/dashscope 系谓词 2026-09-13 迁入 adapters/aliyun.go 私有化，
// 断言随迁（TestAliyunThinkingPredicates / TestAliyunExplicitCachePredicate）。

// TestLKEAPThinkingPredicates pins the LKEAP DeepSeek V3.x / R1 split.
func TestLKEAPThinkingPredicates(t *testing.T) {
	assert.True(t, IsLKEAPDeepSeekV3Model("deepseek-v3.1"))
	assert.False(t, IsLKEAPDeepSeekV3Model("deepseek-r1"))
	assert.True(t, IsLKEAPDeepSeekR1Model("DeepSeek-R1-0528"))
	assert.False(t, IsLKEAPDeepSeekR1Model("deepseek-v3"))
	assert.True(t, IsLKEAPThinkingModel("deepseek-v3"))
	assert.True(t, IsLKEAPThinkingModel("deepseek-r1"))
	assert.False(t, IsLKEAPThinkingModel("deepseek-chat"))
}

// TestMoonshotFixedTempPredicate pins the temperature=1-only model list
// (moonshot-v1 series + kimi-k2.5/k2.6 exact; kimi-k2 stays free).
func TestMoonshotFixedTempPredicate(t *testing.T) {
	assert.True(t, IsMoonshotFixedTempModel("moonshot-v1-8k"))
	assert.True(t, IsMoonshotFixedTempModel("moonshot-v1-128k"))
	assert.True(t, IsMoonshotFixedTempModel("kimi-k2.5"))
	assert.True(t, IsMoonshotFixedTempModel("kimi-k2.6"))
	assert.True(t, IsMoonshotFixedTempModel("Kimi-K2.6"))
	assert.False(t, IsMoonshotFixedTempModel("kimi-k2"))
	assert.False(t, IsMoonshotFixedTempModel("kimi-k2-thinking"))
	assert.False(t, IsMoonshotFixedTempModel("moonshot-latest"))
}

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

// TestDetectProvider pins the URL→provider mode table (migrated from the v1
// provider package, P4 审核轮 P2-5：删除包时丢失的直接覆盖已恢复).
func TestDetectProvider(t *testing.T) {
	tests := []struct {
		url      string
		expected ProviderName
	}{
		{"https://api.openai.com/v1", ProviderOpenAI},
		{"https://api.anthropic.com/v1", ProviderAnthropic},
		{"https://openrouter.ai/api/v1", ProviderOpenRouter},
		{"https://litellm.example.com/v1", ProviderLiteLLM},
		{LiteLLMBaseURL, ProviderLiteLLM},
		{"http://localhost:4000/v1", ProviderGeneric},
		{"https://router.requesty.ai/v1", ProviderRequesty},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", ProviderAliyun},
		{"https://open.bigmodel.cn/api/paas/v4", ProviderZhipu},
		{"https://api.deepseek.com/v1", ProviderDeepSeek},
		{"https://generativelanguage.googleapis.com/v1beta/openai", ProviderGemini},
		{"https://ark.cn-beijing.volces.com/api/v3", ProviderVolcengine},
		{"https://api.hunyuan.cloud.tencent.com/v1", ProviderHunyuan},
		{"https://api.minimaxi.com/v1", ProviderMiniMax},
		{"https://api.minimax.io/v1", ProviderMiniMax},
		{"https://api.xiaomimimo.com/v1", ProviderMimo},
		{"https://custom-endpoint.example.com/v1", ProviderGeneric},
		{"http://localhost:11434/v1", ProviderGeneric},
		{"https://integrate.api.nvidia.com/v1", ProviderNvidia},
		{"https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking", ProviderNvidia},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := DetectProvider(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}
