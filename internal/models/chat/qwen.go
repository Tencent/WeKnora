package chat

import (
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// QwenChat 阿里云 Qwen 模型聊天实现
// Qwen3 模型需要特殊处理 enable_thinking 参数
type QwenChat struct {
	*RemoteAPIChat
}

// QwenChatCompletionRequest Qwen 模型的自定义请求结构体
type QwenChatCompletionRequest struct {
	openai.ChatCompletionRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

// NewQwenChat 创建 Qwen 聊天实例
func NewQwenChat(config *ChatConfig) (*QwenChat, error) {
	config.Provider = string(provider.ProviderAliyun)

	remoteChat, err := NewRemoteAPIChat(config)
	if err != nil {
		return nil, err
	}

	chat := &QwenChat{
		RemoteAPIChat: remoteChat,
	}

	// 设置请求自定义器
	remoteChat.SetRequestCustomizer(chat.customizeRequest)

	return chat, nil
}

// isQwen3Model 检查是否为 Qwen3 模型
func (c *QwenChat) isQwen3Model() bool {
	return provider.IsQwen3Model(c.GetModelName())
}

// customizeRequest 自定义 Qwen 请求
func (c *QwenChat) customizeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool) {
	// 仅 Qwen3 模型需要特殊处理
	if !c.isQwen3Model() {
		return nil, false
	}

	// 非流式请求需要显式禁用 thinking
	if !isStream {
		qwenReq := QwenChatCompletionRequest{
			ChatCompletionRequest: *req,
		}
		enableThinking := false
		qwenReq.EnableThinking = &enableThinking
		return qwenReq, true
	}

	return nil, false
}
