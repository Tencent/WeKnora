package chat

import (
	"github.com/Tencent/WeKnora/internal/models/provider"
)

// NvidiaChat NVIDIA 模型聊天实现
// NVIDIA 模型需要自定义请求地址
type NvidiaChat struct {
	*RemoteAPIChat
}

// NewNvidiaChat 创建 NVIDIA 聊天实例
func NewNvidiaChat(config *ChatConfig) (*NvidiaChat, error) {
	config.Provider = string(provider.ProviderAliyun)

	remoteChat, err := NewRemoteAPIChat(config)
	if err != nil {
		return nil, err
	}

	chat := &NvidiaChat{
		RemoteAPIChat: remoteChat,
	}

	// 设置请求地址自定义器
	remoteChat.SetEndpointCustomizer(chat.endpointCustomizer)
	return chat, nil
}

// customizeRequest 自定义 Qwen 请求
func (c *NvidiaChat) endpointCustomizer(baseURL string, modelID string, isStream bool) string {
	return baseURL
}
