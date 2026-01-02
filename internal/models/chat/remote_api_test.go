package chat

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemoteAPIChat Remote API Chat의 모든 기능 종합 테스트
func TestRemoteAPIChat(t *testing.T) {
	// 환경 변수 가져오기
	deepseekAPIKey := os.Getenv("DEEPSEEK_API_KEY")
	aliyunAPIKey := os.Getenv("ALIYUN_API_KEY")

	// 테스트 구성 정의
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

	// 테스트 메시지
	testMessages := []Message{
		{
			Role:    "user",
			Content: "test",
		},
	}

	// 테스트 옵션
	testOptions := &ChatOptions{
		Temperature: 0.7,
		MaxTokens:   100,
	}

	// 컨텍스트 생성
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 모든 구성 반복 테스트
	for _, tc := range testConfigs {
		t.Run(tc.name, func(t *testing.T) {
			// API Key 확인
			if tc.apiKey == "" {
				t.Skip(tc.skipMsg)
			}

			// 채팅 인스턴스 생성
			chat, err := NewRemoteAPIChat(tc.config)
			require.NoError(t, err)
			assert.Equal(t, tc.config.ModelName, chat.GetModelName())
			assert.Equal(t, tc.config.ModelID, chat.GetModelID())

			// 기본 채팅 기능 테스트
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
