package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/sashabaranov/go-openai"
)

// RemoteAPIChat implements OpenAI-compatible chat against a remote endpoint.
// Provider-specific behavior (request shape, endpoint path, signed auth) is
// injected via the three customizer hooks below — the struct itself stays
// provider-agnostic.
type RemoteAPIChat struct {
	modelName string
	client    *openai.Client
	modelID   string
	baseURL   string
	apiKey    string
	provider  provider.ProviderName
	appID     string
	appSecret string
	// customHeaders 为用户在模型配置中指定的自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	customHeaders map[string]string

	// requestCustomizer 允许子类自定义请求
	// 返回自定义请求体（如果为 nil 则使用标准请求）和是否需要使用原始 HTTP 请求
	requestCustomizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (customReq any, useRawHTTP bool)

	// endpointCustomizer 允许子类自定义请求的 endpoint
	// 返回是否使用自定义请求地址, 返回空则使用默认OpenAI格式地址
	endpointCustomizer func(baseURL string, modelID string, isStream bool) (endpoint string)

	// headerCustomizer 允许子类自定义原始 HTTP 请求头（例如签名认证）
	headerCustomizer func(req *http.Request, body []byte) error
}

// NewRemoteAPIChat 创建远程 API 聊天实例
func NewRemoteAPIChat(chatConfig *ChatConfig) (*RemoteAPIChat, error) {
	if chatConfig.BaseURL != "" {
		if err := secutils.ValidateURLForSSRF(chatConfig.BaseURL); err != nil {
			return nil, fmt.Errorf("baseURL SSRF check failed: %w", err)
		}
	}

	apiKey := chatConfig.APIKey
	providerName := provider.ProviderName(chatConfig.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(chatConfig.BaseURL)
	}

	var config openai.ClientConfig
	if providerName == provider.ProviderAzureOpenAI {
		config = openai.DefaultAzureConfig(apiKey, chatConfig.BaseURL)
		config.AzureModelMapperFunc = func(model string) string {
			return model
		}
		if chatConfig.ExtraConfig != nil {
			if v, ok := chatConfig.ExtraConfig["api_version"]; ok {
				config.APIVersion = v
			}
		}
	} else {
		config = openai.DefaultConfig(apiKey)
		if baseURL := chatConfig.BaseURL; baseURL != "" {
			config.BaseURL = baseURL
		}
	}

	// 如果指定了 CustomHeaders，则给 SDK 使用的 HTTPClient 挂一层 RoundTripper，
	// 在每个请求上自动注入这些 header（raw HTTP 路径会在发送前单独处理）。
	if len(chatConfig.CustomHeaders) > 0 {
		if httpClient, ok := config.HTTPClient.(*http.Client); ok {
			config.HTTPClient = secutils.WrapHTTPClientWithHeaders(httpClient, chatConfig.CustomHeaders)
		} else {
			// SDK 默认未显式设置时 HTTPClient 为 nil，此时构造一个新的注入了 header 的 client。
			config.HTTPClient = secutils.WrapHTTPClientWithHeaders(nil, chatConfig.CustomHeaders)
		}
	}

	modelName := chatConfig.ModelName
	if chatConfig.ExtraConfig != nil {
		if override := strings.TrimSpace(chatConfig.ExtraConfig["remote_model_name"]); override != "" {
			modelName = override
		}
	}
	if providerName == provider.ProviderWeKnoraCloud {
		if chatConfig.AppID == "" {
			return nil, fmt.Errorf("WeKnoraCloud provider: AppID is required")
		}
		if chatConfig.AppSecret == "" {
			return nil, fmt.Errorf("WeKnoraCloud provider: AppSecret is required")
		}
	}

	return &RemoteAPIChat{
		modelName:     modelName,
		client:        openai.NewClientWithConfig(config),
		modelID:       chatConfig.ModelID,
		baseURL:       chatConfig.BaseURL,
		apiKey:        apiKey,
		provider:      providerName,
		appID:         chatConfig.AppID,
		appSecret:     chatConfig.AppSecret,
		customHeaders: chatConfig.CustomHeaders,
	}, nil
}

// SetRequestCustomizer 设置请求自定义器
func (c *RemoteAPIChat) SetRequestCustomizer(
	customizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool),
) {
	c.requestCustomizer = customizer
}

// SetEndpointCustomizer 设置请求地址自定义器
func (c *RemoteAPIChat) SetEndpointCustomizer(customizer func(baseURL string, modelID string, isStream bool) string) {
	c.endpointCustomizer = customizer
}

// SetHeaderCustomizer 设置原始 HTTP 请求头自定义器
func (c *RemoteAPIChat) SetHeaderCustomizer(customizer func(req *http.Request, body []byte) error) {
	c.headerCustomizer = customizer
}

// ConvertMessages 转换消息格式为 OpenAI 格式（导出供子类使用）
func (c *RemoteAPIChat) ConvertMessages(messages []Message) []openai.ChatCompletionMessage {
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role: msg.Role,
		}

		// 优先处理多内容消息（包含图片等）
		switch {
		case len(msg.MultiContent) > 0:
			openaiMsg.MultiContent = make([]openai.ChatMessagePart, 0, len(msg.MultiContent))
			for _, part := range msg.MultiContent {
				switch part.Type {
				case "text":
					openaiMsg.MultiContent = append(openaiMsg.MultiContent, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: part.Text,
					})
				case "image_url":
					if part.ImageURL != nil {
						openaiMsg.MultiContent = append(openaiMsg.MultiContent, openai.ChatMessagePart{
							Type: openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{
								URL:    part.ImageURL.URL,
								Detail: openai.ImageURLDetail(part.ImageURL.Detail),
							},
						})
					}
				}
			}
		case len(msg.Images) > 0 && msg.Role == "user":
			parts := make([]openai.ChatMessagePart, 0, len(msg.Images)+1)
			for _, imgURL := range msg.Images {
				resolved := resolveImageURLForLLM(imgURL)
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    resolved,
						Detail: openai.ImageURLDetailAuto,
					},
				})
			}
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: msg.Content,
			})
			openaiMsg.MultiContent = parts
		case msg.Content != "":
			openaiMsg.Content = msg.Content
		}

		if len(msg.ToolCalls) > 0 {
			openaiMsg.ToolCalls = make([]openai.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				toolType := openai.ToolType(tc.Type)
				openaiMsg.ToolCalls = append(openaiMsg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: toolType,
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}

		if msg.Role == "tool" {
			openaiMsg.ToolCallID = msg.ToolCallID
			openaiMsg.Name = msg.Name
		}

		openaiMessages = append(openaiMessages, openaiMsg)
	}
	return openaiMessages
}

// BuildChatCompletionRequest 构建标准聊天请求参数（导出供子类使用）
func (c *RemoteAPIChat) BuildChatCompletionRequest(messages []Message, opts *ChatOptions, isStream bool) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:    c.modelName,
		Messages: c.ConvertMessages(messages),
		Stream:   isStream,
	}

	if isStream {
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}

	if opts != nil {
		applyChatOpts(&req, opts)
	}

	return req
}

// applyChatOpts folds ChatOptions into an openai.ChatCompletionRequest.
// Split out so the request-building method stays small and readable.
func applyChatOpts(req *openai.ChatCompletionRequest, opts *ChatOptions) {
	req.Temperature = float32(opts.Temperature)
	if opts.TopP > 0 {
		req.TopP = float32(opts.TopP)
	}
	if opts.MaxTokens > 0 {
		req.MaxTokens = opts.MaxTokens
	}
	if opts.MaxCompletionTokens > 0 {
		req.MaxCompletionTokens = opts.MaxCompletionTokens
	}
	if opts.FrequencyPenalty > 0 {
		req.FrequencyPenalty = float32(opts.FrequencyPenalty)
	}
	if opts.PresencePenalty > 0 {
		req.PresencePenalty = float32(opts.PresencePenalty)
	}

	// Tools
	if len(opts.Tools) > 0 {
		req.Tools = make([]openai.Tool, 0, len(opts.Tools))
		for _, tool := range opts.Tools {
			toolType := openai.ToolType(tool.Type)
			openaiTool := openai.Tool{
				Type: toolType,
				Function: &openai.FunctionDefinition{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
				},
			}
			if tool.Function.Parameters != nil {
				openaiTool.Function.Parameters = tool.Function.Parameters
			}
			req.Tools = append(req.Tools, openaiTool)
		}
	}

	// ParallelToolCalls
	if opts.ParallelToolCalls != nil {
		req.ParallelToolCalls = *opts.ParallelToolCalls
	}

	// ToolChoice
	if opts.ToolChoice != "" {
		switch opts.ToolChoice {
		case "none", "required", "auto":
			req.ToolChoice = opts.ToolChoice
		default:
			req.ToolChoice = openai.ToolChoice{
				Type: "function",
				Function: openai.ToolFunction{
					Name: opts.ToolChoice,
				},
			}
		}
	}

	// Response format. When a JSON schema is supplied we also append the schema
	// hint to the last message content, mirroring the pre-refactor behavior.
	if len(opts.Format) > 0 {
		req.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}
		if len(req.Messages) > 0 {
			req.Messages[len(req.Messages)-1].Content += fmt.Sprintf("\nUse this JSON schema: %s", opts.Format)
		}
	}
}

// logRequest logs a marshaled request for debugging. Compacts image data URLs
// to avoid MB-scale log lines.
func (c *RemoteAPIChat) logRequest(ctx context.Context, req any, isStream bool) {
	if jsonData, err := json.MarshalIndent(req, "", "  "); err == nil {
		logger.Infof(ctx, "[LLM Request] model=%s, stream=%v, request:\n%s",
			c.modelName, isStream, secutils.CompactImageDataURLForLog(string(jsonData)))
	}
}

// GetModelName returns the model name used for requests.
func (c *RemoteAPIChat) GetModelName() string { return c.modelName }

// GetModelID returns the local model ID.
func (c *RemoteAPIChat) GetModelID() string { return c.modelID }

// GetProvider returns the resolved provider name.
func (c *RemoteAPIChat) GetProvider() provider.ProviderName { return c.provider }

// GetBaseURL returns the configured base URL.
func (c *RemoteAPIChat) GetBaseURL() string { return c.baseURL }

// GetAPIKey returns the configured API key. Used by provider-specific header
// signers (e.g. WeKnoraCloud) that live outside of this file.
func (c *RemoteAPIChat) GetAPIKey() string { return c.apiKey }
