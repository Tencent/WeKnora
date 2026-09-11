package invoke

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
