package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// DeepSeekChat DeepSeek 模型聊天实现
// DeepSeek 模型不支持 tool_choice 参数
type DeepSeekChat struct {
	*RemoteAPIChat
}

// NewDeepSeekChat 创建 DeepSeek 聊天实例
func NewDeepSeekChat(config *ChatConfig) (*DeepSeekChat, error) {
	config.Provider = string(provider.ProviderDeepSeek)

	remoteChat, err := NewRemoteAPIChat(config)
	if err != nil {
		return nil, err
	}

	chat := &DeepSeekChat{
		RemoteAPIChat: remoteChat,
	}

	// 设置请求自定义器
	remoteChat.SetRequestCustomizer(chat.customizeRequest)

	return chat, nil
}

// customizeRequest 自定义 DeepSeek 请求
func (c *DeepSeekChat) customizeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool) {
	// DeepSeek 模型不支持 tool_choice，需要清除
	if opts != nil && opts.ToolChoice != "" {
		logger.Infof(context.Background(), "deepseek model, skip tool_choice")
		req.ToolChoice = nil
	}
	return nil, false
}

// GenericChat 通用 OpenAI 兼容实现（如 vLLM）
// 支持 ChatTemplateKwargs 参数
type GenericChat struct {
	*RemoteAPIChat
}

// NewGenericChat 创建通用聊天实例
func NewGenericChat(config *ChatConfig) (*GenericChat, error) {
	config.Provider = string(provider.ProviderGeneric)

	remoteChat, err := NewRemoteAPIChat(config)
	if err != nil {
		return nil, err
	}

	chat := &GenericChat{
		RemoteAPIChat: remoteChat,
	}

	// 设置请求自定义器
	remoteChat.SetRequestCustomizer(chat.customizeRequest)

	return chat, nil
}

// customizeRequest 自定义 Generic 请求
func (c *GenericChat) customizeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool) {
	// Generic provider（如 vLLM）使用 ChatTemplateKwargs 传递 thinking 参数
	thinking := false
	if opts != nil && opts.Thinking != nil {
		thinking = *opts.Thinking
	}
	req.ChatTemplateKwargs = map[string]interface{}{
		"enable_thinking": thinking,
	}
	return nil, false // 使用标准请求（已修改）
}
