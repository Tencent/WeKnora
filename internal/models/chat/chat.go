package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
)

// Tool represents a function/tool definition
type Tool struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef represents a function definition
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChatOptions 聊天选项
type ChatOptions struct {
	Temperature         float64         `json:"temperature"`                   // 温度参数
	TopP                float64         `json:"top_p"`                         // Top P 参数
	Seed                int             `json:"seed"`                          // 随机种子
	MaxTokens           int             `json:"max_tokens"`                    // 最大 token 数
	MaxCompletionTokens int             `json:"max_completion_tokens"`         // 最大完成 token 数
	FrequencyPenalty    float64         `json:"frequency_penalty"`             // 频率惩罚
	PresencePenalty     float64         `json:"presence_penalty"`              // 存在惩罚
	Thinking            *bool           `json:"thinking"`                      // 是否启用思考
	Tools               []Tool          `json:"tools,omitempty"`               // 可用工具列表
	ToolChoice          string          `json:"tool_choice,omitempty"`         // "auto", "required", "none", or specific tool
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"` // 是否允许并行工具调用（默认 nil 表示由模型决定）
	Format              json.RawMessage `json:"format,omitempty"`              // 响应格式定义
}

// MessageContentPart represents a part of multi-content message
type MessageContentPart struct {
	Type     string    `json:"type"`                // "text" or "image_url"
	Text     string    `json:"text,omitempty"`      // For type="text"
	ImageURL *ImageURL `json:"image_url,omitempty"` // For type="image_url"
}

// ImageURL represents the image URL structure
type ImageURL struct {
	URL    string `json:"url"`              // URL or base64 data URI
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// Message 表示聊天消息
type Message struct {
	Role         string               `json:"role"`                    // 角色：system, user, assistant, tool
	Content      string               `json:"content"`                 // 消息内容
	MultiContent []MessageContentPart `json:"multi_content,omitempty"` // 多内容消息（文本+图片）
	Name         string               `json:"name,omitempty"`          // Function/tool name (for tool role)
	ToolCallID   string               `json:"tool_call_id,omitempty"`  // Tool call ID (for tool role)
	ToolCalls    []ToolCall           `json:"tool_calls,omitempty"`    // Tool calls (for assistant role)
	Images       []string             `json:"images,omitempty"`        // Image URLs for multimodal (only for current user message)
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Chat 定义了聊天接口
type Chat interface {
	// Chat 进行非流式聊天
	Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error)

	// ChatStream 进行流式聊天
	ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error)

	// GetModelName 获取模型名称
	GetModelName() string

	// GetModelID 获取模型ID
	GetModelID() string
}

type ChatConfig struct {
	Source      types.ModelSource
	BaseURL     string
	ModelName   string
	APIKey      string
	ModelID     string
	Provider    string
	ExtraConfig map[string]string
	// CustomHeaders 允许在调用远程 OpenAI 兼容 API 时附加自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	CustomHeaders map[string]string
	AppID         string
	AppSecret     string // 加密值，由工厂函数调用方传入，在 NewWeKnoraCloudChat 中使用前已解密
}

// ConfigFromModel 根据 types.Model 构造 ChatConfig。
// 保证生产路径（service 层根据 DB 中的模型配置拉起实例）和测试路径
// （handler 层根据前端表单临时拉起实例）走完全相同的字段映射，避免重复样板。
// appID / appSecret 是已经解密/解析好的 WeKnoraCloud 凭证，调用方负责传入。
func ConfigFromModel(m *types.Model, appID, appSecret string) *ChatConfig {
	if m == nil {
		return nil
	}
	return &ChatConfig{
		ModelID:       m.ID,
		APIKey:        m.Parameters.APIKey,
		BaseURL:       m.Parameters.BaseURL,
		ModelName:     m.Name,
		Source:        m.Source,
		Provider:      m.Parameters.Provider,
		ExtraConfig:   m.Parameters.ExtraConfig,
		CustomHeaders: m.Parameters.CustomHeaders,
		AppID:         appID,
		AppSecret:     appSecret,
	}
}

// NewChat 创建聊天实例
func NewChat(config *ChatConfig, ollamaService *ollama.OllamaService) (Chat, error) {
	var c Chat
	var err error
	switch strings.ToLower(string(config.Source)) {
	case string(types.ModelSourceLocal):
		c, err = NewOllamaChat(config, ollamaService)
	case string(types.ModelSourceRemote):
		c, err = NewRemoteChat(config)
	default:
		return nil, fmt.Errorf("unsupported chat model source: %s", config.Source)
	}
	c, err = wrapChatDebug(c, err)
	return wrapChatLangfuse(c, err)
}

// NewRemoteChat 根据 provider 创建远程聊天实例
func NewRemoteChat(config *ChatConfig) (Chat, error) {
	providerName := provider.ProviderName(config.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(config.BaseURL)
	}
	if providerName == provider.ProviderAnthropic {
		return NewAnthropicChat(config)
	}

	remoteChat, err := NewRemoteAPIChat(config)
	if err != nil {
		return nil, err
	}

	// Look up provider-specific behavior from spec registry
	if spec := findProviderSpec(providerName, config.ModelName); spec != nil {
		if spec.RequestCustomizer != nil {
			remoteChat.SetRequestCustomizer(spec.RequestCustomizer)
		}
		if spec.EndpointCustomizer != nil {
			remoteChat.SetEndpointCustomizer(spec.EndpointCustomizer)
		}
		if spec.HeaderCustomizer != nil {
			remoteChat.SetHeaderCustomizer(func(req *http.Request, body []byte) error {
				return spec.HeaderCustomizer(remoteChat, req, body)
			})
		}
	}

	return remoteChat, nil
}
