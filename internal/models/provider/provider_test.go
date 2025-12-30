package provider

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderRegistry(t *testing.T) {
	// Test that all default providers are registered
	t.Run("default providers registered", func(t *testing.T) {
		providers := List()
		assert.NotEmpty(t, providers, "should have registered providers")

		// Check specific providers exist
		for _, name := range []ProviderName{ProviderOpenAI, ProviderAliyun, ProviderZhipu, ProviderGeneric} {
			p, ok := Get(name)
			assert.True(t, ok, "provider %s should be registered", name)
			assert.NotNil(t, p, "provider %s should not be nil", name)
		}
	})

	t.Run("GetOrDefault fallback", func(t *testing.T) {
		// Non-existent provider should fall back to generic
		p := GetOrDefault("nonexistent")
		require.NotNil(t, p)
		assert.Equal(t, ProviderGeneric, p.Info().Name)
	})
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		url      string
		expected ProviderName
	}{
		{"https://api.openai.com/v1", ProviderOpenAI},
		{"https://openrouter.ai/api/v1", ProviderOpenRouter},
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
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := DetectProvider(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOpenAIProviderValidation(t *testing.T) {
	p := &OpenAIProvider{}

	t.Run("valid config", func(t *testing.T) {
		config := &Config{
			APIKey:    "sk-test",
			ModelName: "gpt-4",
		}
		err := p.ValidateConfig(config)
		assert.NoError(t, err)
	})

	t.Run("missing API key", func(t *testing.T) {
		config := &Config{
			ModelName: "gpt-4",
		}
		err := p.ValidateConfig(config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "API key")
	})

	t.Run("missing model name", func(t *testing.T) {
		config := &Config{
			APIKey: "sk-test",
		}
		err := p.ValidateConfig(config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "model name")
	})
}

func TestAliyunProviderValidation(t *testing.T) {
	p := &AliyunProvider{}

	t.Run("valid config", func(t *testing.T) {
		config := &Config{
			APIKey:    "sk-test",
			ModelName: "qwen-max",
		}
		err := p.ValidateConfig(config)
		assert.NoError(t, err)
	})

	t.Run("info", func(t *testing.T) {
		info := p.Info()
		assert.Equal(t, ProviderAliyun, info.Name)
		assert.Contains(t, info.ModelTypes, types.ModelTypeKnowledgeQA)
		assert.Contains(t, info.ModelTypes, types.ModelTypeEmbedding)
		assert.Contains(t, info.ModelTypes, types.ModelTypeRerank)
	})
}

func TestAliyunModelDetection(t *testing.T) {
	t.Run("Qwen3 model detection", func(t *testing.T) {
		assert.True(t, IsQwen3Model("qwen3-32b"))
		assert.True(t, IsQwen3Model("qwen3-72b"))
		assert.False(t, IsQwen3Model("qwen-max"))
		assert.False(t, IsQwen3Model("qwen2.5-72b"))
	})

	t.Run("DeepSeek model detection", func(t *testing.T) {
		assert.True(t, IsDeepSeekModel("deepseek-chat"))
		assert.True(t, IsDeepSeekModel("deepseek-v3.1"))
		assert.True(t, IsDeepSeekModel("DeepSeek-Chat"))
		assert.False(t, IsDeepSeekModel("qwen-max"))
	})
}

func TestZhipuProviderValidation(t *testing.T) {
	p := &ZhipuProvider{}

	t.Run("valid config", func(t *testing.T) {
		config := &Config{
			APIKey:    "test-key",
			ModelName: "glm-4",
		}
		err := p.ValidateConfig(config)
		assert.NoError(t, err)
	})

	t.Run("info", func(t *testing.T) {
		info := p.Info()
		assert.Equal(t, ProviderZhipu, info.Name)
		assert.Equal(t, ZhipuChatBaseURL, info.GetDefaultURL(types.ModelTypeKnowledgeQA))
		assert.Equal(t, ZhipuEmbeddingBaseURL, info.GetDefaultURL(types.ModelTypeEmbedding))
	})
}

func TestListByModelType(t *testing.T) {
	t.Run("chat models", func(t *testing.T) {
		providers := ListByModelType(types.ModelTypeKnowledgeQA)
		assert.NotEmpty(t, providers)
		// Multiple providers support chat
		assert.GreaterOrEqual(t, len(providers), 9)
	})

	t.Run("rerank models", func(t *testing.T) {
		providers := ListByModelType(types.ModelTypeRerank)
		assert.NotEmpty(t, providers)
		// Check that Aliyun supports rerank
		found := false
		for _, p := range providers {
			if p.Name == ProviderAliyun {
				found = true
				break
			}
		}
		assert.True(t, found, "Aliyun should support rerank")
	})
}
