package chat

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// LKEAPChat 腾讯云知识引擎原子能力 (LKEAP) 聊天实现
// 支持 DeepSeek-R1, DeepSeek-V3 系列模型，具备思维链能力
// 参考：https://cloud.tencent.com/document/product/1772/115963
//
// 与标准 OpenAI API 的区别：
// 1. thinking 参数格式不同：LKEAP 使用 {"type": "enabled"/"disabled"}
// 2. 仅 DeepSeek V3.x 系列需要显式设置 thinking 参数，R1 系列默认开启
type LKEAPChat struct {
	*RemoteAPIChat
}

// LKEAPThinkingConfig 思维链配置（LKEAP 特有格式）
type LKEAPThinkingConfig struct {
	Type string `json:"type"` // "enabled" 或 "disabled"
}

// LKEAPChatCompletionRequest LKEAP 自定义请求结构体
type LKEAPChatCompletionRequest struct {
	openai.ChatCompletionRequest
	Thinking *LKEAPThinkingConfig `json:"thinking,omitempty"` // 思维链开关（仅 V3.x 系列）
}

// NewLKEAPChat 创建 LKEAP 聊天实例
func NewLKEAPChat(config *ChatConfig) (*LKEAPChat, error) {
	// 确保 provider 设置正确
	config.Provider = string(provider.ProviderLKEAP)

	remoteChat, err := NewRemoteAPIChat(config)
	if err != nil {
		return nil, err
	}

	chat := &LKEAPChat{
		RemoteAPIChat: remoteChat,
	}

	// 设置请求自定义器，添加 LKEAP 特有的 thinking 参数
	remoteChat.SetRequestCustomizer(chat.customizeRequest)

	return chat, nil
}

// isDeepSeekV3Model 检查是否为 DeepSeek V3.x 系列模型
func (c *LKEAPChat) isDeepSeekV3Model() bool {
	return strings.Contains(strings.ToLower(c.GetModelName()), "deepseek-v3")
}

// customizeRequest 自定义 LKEAP 请求
func (c *LKEAPChat) customizeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool) {
	// 仅对 DeepSeek V3.x 系列模型需要特殊处理 thinking 参数
	// R1 系列模型默认开启思维链，无需额外参数
	if !c.isDeepSeekV3Model() || opts == nil || opts.Thinking == nil {
		return nil, false // 使用标准请求
	}

	// 构建 LKEAP 特有请求
	lkeapReq := LKEAPChatCompletionRequest{
		ChatCompletionRequest: *req,
	}

	thinkingType := "disabled"
	if *opts.Thinking {
		thinkingType = "enabled"
	}
	lkeapReq.Thinking = &LKEAPThinkingConfig{Type: thinkingType}

	return lkeapReq, true // 使用原始 HTTP 请求
}
