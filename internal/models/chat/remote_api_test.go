package chat

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRemoteChat(t *testing.T) *RemoteAPIChat {
	t.Helper()

	chat, err := NewRemoteAPIChat(&ChatConfig{
		Source:    types.ModelSourceRemote,
		BaseURL:   "",
		ModelName: "test-model",
		APIKey:    "test-key",
		ModelID:   "test-model",
	})
	require.NoError(t, err)
	return chat
}

func TestBuildChatCompletionRequest_ParallelToolCalls(t *testing.T) {
	chat := newTestRemoteChat(t)
	messages := []Message{{Role: "user", Content: "hello"}}

	t.Run("nil ParallelToolCalls leaves default", func(t *testing.T) {
		opts := &ChatOptions{Temperature: 0.7}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.Nil(t, req.ParallelToolCalls, "should be nil when not set")
	})

	t.Run("ParallelToolCalls true is propagated", func(t *testing.T) {
		ptc := true
		opts := &ChatOptions{
			Temperature:       0.7,
			ParallelToolCalls: &ptc,
			Tools: []Tool{{
				Type: "function",
				Function: FunctionDef{
					Name:        "mcp_weather_getforecast",
					Description: "Get weather",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			}},
		}
		req := chat.BuildChatCompletionRequest(messages, opts, true)
		assert.NotNil(t, req.ParallelToolCalls)

		val, ok := req.ParallelToolCalls.(bool)
		if ok {
			assert.Equal(t, true, val)
		} else {
			assert.Equal(t, true, req.ParallelToolCalls)
		}

		assert.Len(t, req.Tools, 1)
		assert.Equal(t, "mcp_weather_getforecast", req.Tools[0].Function.Name)
	})

	t.Run("ParallelToolCalls false is propagated", func(t *testing.T) {
		ptc := false
		opts := &ChatOptions{
			Temperature:       0.7,
			ParallelToolCalls: &ptc,
		}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.NotNil(t, req.ParallelToolCalls)

		val, ok := req.ParallelToolCalls.(bool)
		if ok {
			assert.Equal(t, false, val)
		} else {
			assert.Equal(t, false, req.ParallelToolCalls)
		}
	})
}

func TestBuildChatCompletionRequest_MCPToolsFormat(t *testing.T) {
	chat := newTestRemoteChat(t)
	messages := []Message{{Role: "user", Content: "查询乙醇的理化性质"}}

	mcpTools := []Tool{
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "mcp_hazardous_chemicals_gethazardouschemicals",
				Description: "[MCP Service: hazardous_chemicals (external)] Get hazardous chemicals list",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "mcp_hazardous_chemicals_gethazardouschemicalbybizid",
				Description: "[MCP Service: hazardous_chemicals (external)] Get hazardous chemical by biz ID",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"bizId":{"type":"string"}},"required":["bizId"]}`),
			},
		},
	}

	ptc := true
	opts := &ChatOptions{
		Temperature:       0.7,
		Tools:             mcpTools,
		ParallelToolCalls: &ptc,
	}

	req := chat.BuildChatCompletionRequest(messages, opts, true)

	assert.Len(t, req.Tools, 2)
	assert.Equal(t, "mcp_hazardous_chemicals_gethazardouschemicals", req.Tools[0].Function.Name)
	assert.Equal(t, "mcp_hazardous_chemicals_gethazardouschemicalbybizid", req.Tools[1].Function.Name)
	assert.Equal(t, true, req.ParallelToolCalls)
	assert.True(t, req.Stream)

	for _, tool := range req.Tools {
		name := tool.Function.Name
		assert.NotContains(t, name, "ed606721", "tool name must use service name, not UUID")
		assert.Regexp(t, `^[a-zA-Z0-9_-]+$`, name, "tool name must match OpenAI pattern")
		assert.LessOrEqual(t, len(name), 64, "tool name must be <= 64 chars")
	}
}

func TestBuildChatCompletionRequest_ToolChoice(t *testing.T) {
	chat := newTestRemoteChat(t)
	messages := []Message{{Role: "user", Content: "test"}}

	t.Run("auto tool choice", func(t *testing.T) {
		opts := &ChatOptions{ToolChoice: "auto"}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.Equal(t, "auto", req.ToolChoice)
	})

	t.Run("specific tool choice", func(t *testing.T) {
		opts := &ChatOptions{ToolChoice: "mcp_svc_tool"}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.NotNil(t, req.ToolChoice)
	})
}

// TestRemoteAPIChat 综合测试 Remote API Chat 的所有功能
func TestRemoteAPIChat(t *testing.T) {
	// 获取环境变量
	deepseekAPIKey := os.Getenv("DEEPSEEK_API_KEY")
	aliyunAPIKey := os.Getenv("ALIYUN_API_KEY")

	// 定义测试配置
	testConfigs := []struct {
		name    string
		apiKey  string
		config  *ChatConfig
		skipMsg string
	}{
		{
			name:   "DeepSeek API",
			apiKey: deepseekAPIKey,
			config: &ChatConfig{
				Source:    types.ModelSourceRemote,
				BaseURL:   "https://api.deepseek.com/v1",
				ModelName: "deepseek-chat",
				APIKey:    deepseekAPIKey,
				ModelID:   "deepseek-chat",
			},
			skipMsg: "DEEPSEEK_API_KEY environment variable not set",
		},
		{
			name:   "Aliyun DeepSeek",
			apiKey: aliyunAPIKey,
			config: &ChatConfig{
				Source:    types.ModelSourceRemote,
				BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
				ModelName: "deepseek-v3.1",
				APIKey:    aliyunAPIKey,
				ModelID:   "deepseek-v3.1",
			},
			skipMsg: "ALIYUN_API_KEY environment variable not set",
		},
		{
			name:   "Aliyun Qwen3-32b",
			apiKey: aliyunAPIKey,
			config: &ChatConfig{
				Source:    types.ModelSourceRemote,
				BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
				ModelName: "qwen3-32b",
				APIKey:    aliyunAPIKey,
				ModelID:   "qwen3-32b",
			},
			skipMsg: "ALIYUN_API_KEY environment variable not set",
		},
		{
			name:   "Aliyun Qwen-max",
			apiKey: aliyunAPIKey,
			config: &ChatConfig{
				Source:    types.ModelSourceRemote,
				BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
				ModelName: "qwen-max",
				APIKey:    aliyunAPIKey,
				ModelID:   "qwen-max",
			},
			skipMsg: "ALIYUN_API_KEY environment variable not set",
		},
	}

	// 测试消息
	testMessages := []Message{
		{
			Role:    "user",
			Content: "test",
		},
	}

	// 测试选项
	testOptions := &ChatOptions{
		Temperature: 0.7,
		MaxTokens:   100,
	}

	// 创建上下文
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 遍历所有配置进行测试
	for _, tc := range testConfigs {
		t.Run(tc.name, func(t *testing.T) {
			// 检查 API Key
			if tc.apiKey == "" {
				t.Skip(tc.skipMsg)
			}

			// 创建聊天实例
			chat, err := NewRemoteAPIChat(tc.config)
			require.NoError(t, err)
			assert.Equal(t, tc.config.ModelName, chat.GetModelName())
			assert.Equal(t, tc.config.ModelID, chat.GetModelID())

			// 测试基本聊天功能
			t.Run("Basic Chat", func(t *testing.T) {
				response, err := chat.Chat(ctx, testMessages, testOptions)
				require.NoError(t, err)
				require.NotNil(t, response, "response should not be nil")
				assert.NotEmpty(t, response.Content)
				assert.Greater(t, response.Usage.TotalTokens, 0)
				assert.Greater(t, response.Usage.PromptTokens, 0)
				assert.Greater(t, response.Usage.CompletionTokens, 0)

				t.Logf("%s Response: %s", tc.name, response.Content)
				t.Logf("Usage: Prompt=%d, Completion=%d, Total=%d",
					response.Usage.PromptTokens,
					response.Usage.CompletionTokens,
					response.Usage.TotalTokens)
			})
		})
	}
}
