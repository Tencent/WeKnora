// Package chat exposes the Chat interface used by the application layer and
// builds the right protocol client for a configured model. Every vendor fact
// comes from internal/models/catalog; every wire protocol lives under
// internal/models/api. This package only glues the two together and wraps
// the client with debug / tracing / concurrency decorators.
package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/anthropicmessages"
	"github.com/Tencent/WeKnora/internal/models/api/googlegenai"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/api/openairesponses"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	// Built-in vendors register with the catalog through package init; the
	// chat factory is the one place that must never see an empty catalog.
	_ "github.com/Tencent/WeKnora/internal/models/vendors"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Message and the other aliases below re-export the request model defined
// in internal/models/api so historical chat.* names keep working.
//
//nolint:revive // ChatOptions is the historical name every caller uses.
type (
	Message            = api.Message
	MessageContentPart = api.MessageContentPart
	ImageURL           = api.ImageURL
	MessageKind        = api.MessageKind
	ToolCall           = api.ToolCall
	FunctionCall       = api.FunctionCall
	Tool               = api.Tool
	FunctionDef        = api.FunctionDef
	ChatOptions        = api.Options
	CacheRetention     = api.CacheRetention
	ReasoningEffort    = api.ReasoningEffort
)

// Re-exported constants of the request model.
const (
	MessageKindCompactionSummary = api.MessageKindCompactionSummary
	CacheRetentionNone           = api.CacheRetentionNone
	CacheRetentionShort          = api.CacheRetentionShort
	CacheRetentionLong           = api.CacheRetentionLong
)

// SanitizeReasoningEffort is re-exported for the callers that build
// ChatOptions from stored strings (agent config, session SummaryConfig).
var SanitizeReasoningEffort = api.SanitizeReasoningEffort

// Prompt-cache helpers re-exported for the application layer.
var (
	FingerprintPromptPrefix = api.FingerprintPromptPrefix
	PromptPrefixFingerprint = api.PromptPrefixFingerprint
	BuildPromptCacheKey     = api.BuildPromptCacheKey
)

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

// ChatConfig is the operator-facing configuration of one chat model.
type ChatConfig struct {
	Source    types.ModelSource
	BaseURL   string
	ModelName string
	APIKey    string
	ModelID   string
	Provider  string
	// MaxConcurrency caps concurrent background calls to this model; 0 falls
	// back to the process-wide default (see limiter.GateN).
	MaxConcurrency int
	ExtraConfig    map[string]string
	// CustomHeaders 允许在调用远程 API 时附加自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	CustomHeaders map[string]string
	AppID         string
	AppSecret     string // 加密值，由工厂函数调用方传入，在使用前已解密
	// Spec carries per-row catalog overrides (protocol, compat, levels).
	Spec *types.ModelSpecOverride
}

// ConfigFromModel 根据 types.Model 构造 ChatConfig。
// 保证生产路径（service 层根据 DB 中的模型配置拉起实例）和测试路径
// （handler 层根据前端表单临时拉起实例）走完全相同的字段映射。
// appID / appSecret 是已经解密/解析好的 WeKnoraCloud 凭证，调用方负责传入。
func ConfigFromModel(m *types.Model, appID, appSecret string) *ChatConfig {
	if m == nil {
		return nil
	}
	return &ChatConfig{
		ModelID:        m.ID,
		APIKey:         m.Parameters.APIKey,
		BaseURL:        m.Parameters.BaseURL,
		ModelName:      m.Name,
		Source:         m.Source,
		Provider:       m.Parameters.Provider,
		MaxConcurrency: m.Parameters.MaxConcurrency,
		ExtraConfig:    m.Parameters.ExtraConfig,
		CustomHeaders:  m.Parameters.CustomHeaders,
		AppID:          appID,
		AppSecret:      appSecret,
		Spec:           m.Parameters.Spec,
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
	c, err = wrapChatLangfuse(c, err)
	// Outermost: hold the per-model concurrency slot only around the real
	// provider round-trip, so the wait is excluded from debug/langfuse timing.
	return wrapChatConcurrency(c, config.MaxConcurrency, err)
}

// Resolve looks the configuration up in the catalog. It is exposed so the
// handler layer can report the effective protocol and capabilities.
func Resolve(config *ChatConfig) (*catalog.Resolved, error) {
	if config == nil {
		return nil, fmt.Errorf("chat config is nil")
	}
	return catalog.Resolve(catalog.Ref{
		Provider:  config.Provider,
		Model:     config.ModelName,
		BaseURL:   config.BaseURL,
		ModelType: types.ModelTypeKnowledgeQA,
		Extra:     config.ExtraConfig,
		Override:  config.Spec,
	})
}

// EffectiveThinkingControl reports how the resolved model encodes thinking
// ("none" when it cannot be asked to think). Kept for the model debug view.
func EffectiveThinkingControl(config *ChatConfig) string {
	resolved, err := Resolve(config)
	if err != nil {
		return "none"
	}
	caps := resolved.Capabilities()
	if len(caps.ThinkingLevels) == 0 {
		return "none"
	}
	return caps.ThinkingFormat
}

// NewRemoteChat builds the protocol client for a remote model.
func NewRemoteChat(config *ChatConfig) (Chat, error) {
	resolved, err := Resolve(config)
	if err != nil {
		return nil, err
	}
	vendor := resolved.Vendor
	if resolved.BaseURL != "" {
		if err := secutils.ValidateURLForSSRF(resolved.BaseURL); err != nil {
			return nil, fmt.Errorf("baseURL SSRF check failed: %w", err)
		}
	}

	creds := catalog.Credentials{APIKey: config.APIKey, AppID: config.AppID, AppSecret: config.AppSecret}
	if creds.APIKey == "" {
		creds.APIKey = vendor.DefaultAPIKey
	}
	if vendor.Auth == catalog.AuthSigned {
		if creds.AppID == "" {
			return nil, fmt.Errorf("%s provider: AppID is required", vendor.Name)
		}
		if creds.AppSecret == "" {
			return nil, fmt.Errorf("%s provider: AppSecret is required", vendor.Name)
		}
	} else if vendor.RequiresAuth && resolved.API == api.APIAnthropicMessages && strings.TrimSpace(creds.APIKey) == "" {
		return nil, fmt.Errorf("%s provider: API key is required", vendor.Name)
	}

	headers := make(map[string]string, len(vendor.Headers)+len(config.CustomHeaders))
	for k, v := range vendor.Headers {
		headers[k] = v
	}
	for k, v := range config.CustomHeaders {
		headers[k] = v
	}
	endpoint := api.Endpoint{
		BaseURL: resolved.BaseURL,
		Model:   resolved.RemoteModel,
		ModelID: config.ModelID,
		Auth:    vendor.AuthFunc(resolved.API, creds),
		Headers: headers,
	}
	if vendor.Endpoint != nil {
		url, query := vendor.Endpoint(catalog.EndpointRequest{
			BaseURL:   resolved.BaseURL,
			Model:     resolved.RemoteModel,
			ModelType: types.ModelTypeKnowledgeQA,
			API:       resolved.API,
			Extra:     config.ExtraConfig,
		})
		if url != "" {
			endpoint.URL = url
			endpoint.Query = query
		}
	}

	switch resolved.API {
	case api.APIOpenAICompletions:
		return openaicompletions.New(openaicompletions.Config{
			Endpoint:       endpoint,
			Settings:       resolved.OpenAICompletions,
			ThinkingLevels: resolved.ThinkingLevels,
			Reasoning:      resolved.Spec.Reasoning,
		}), nil
	case api.APIOpenAIResponses:
		return openairesponses.New(openairesponses.Config{
			Endpoint:       endpoint,
			Settings:       resolved.OpenAIResponses,
			ThinkingLevels: resolved.ThinkingLevels,
			Reasoning:      resolved.Spec.Reasoning,
		}), nil
	case api.APIAnthropicMessages:
		return anthropicmessages.New(anthropicmessages.Config{
			Endpoint:       endpoint,
			Settings:       resolved.AnthropicMessages,
			ThinkingLevels: resolved.ThinkingLevels,
			Reasoning:      resolved.Spec.Reasoning,
		}), nil
	case api.APIGoogleGenerativeAI:
		return googlegenai.New(googlegenai.Config{
			Endpoint:       endpoint,
			Settings:       resolved.GoogleGenerativeAI,
			ThinkingLevels: resolved.ThinkingLevels,
			Reasoning:      resolved.Spec.Reasoning,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported chat api %q for provider %s", resolved.API, vendor.ID)
	}
}
