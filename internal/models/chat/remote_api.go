package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/sashabaranov/go-openai"
)

// LLM 调用超时配置。仅作为"上层未设置 deadline 时"的兜底，避免 hung 请求
// 永久阻塞 worker。如果上层 ctx 已经设置了 deadline（无论比默认更短还是更长），
// 都会原样尊重，不再叠加默认超时。可通过环境变量覆盖：
//   - WEKNORA_LLM_CHAT_TIMEOUT_SECONDS    非流式调用兜底超时（默认 600s）
//   - WEKNORA_LLM_STREAM_TIMEOUT_SECONDS  流式调用兜底超时（默认 1800s）
var (
	defaultChatTimeout   = envDurationSeconds("WEKNORA_LLM_CHAT_TIMEOUT_SECONDS", 600*time.Second)
	defaultStreamTimeout = envDurationSeconds("WEKNORA_LLM_STREAM_TIMEOUT_SECONDS", 1800*time.Second)
)

// envDurationSeconds 读取以"秒"为单位的环境变量，解析失败或非正值时回退到 fallback。
func envDurationSeconds(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

// withLLMTimeout 仅在上层 ctx 没有 deadline 时附加一个兜底超时；
// 如果上层已显式设置 deadline（无论更短或更长），则原样返回，
// 让调用方对自己的超时策略拥有最终决定权。
func withLLMTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// rawHTTPClient is a shared HTTP client for raw HTTP LLM calls with connection-level timeouts.
// Per-request timeout is enforced via context deadline (see defaultChatTimeout / defaultStreamTimeout)
// rather than http.Client.Timeout, so streaming calls are not prematurely terminated.
// Uses SSRFSafeDialContext to prevent DNS rebinding attacks at the connection layer.
var rawHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         secutils.SSRFSafeDialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 5,
	},
}

// RemoteAPIChat 实现了基于 OpenAI 兼容 API 的聊天
// 这是一个通用实现，不包含任何 provider 特定的逻辑
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
func (c *RemoteAPIChat) SetRequestCustomizer(customizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool)) {
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
		if len(msg.MultiContent) > 0 {
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
		} else if len(msg.Images) > 0 && msg.Role == "user" {
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
		} else if msg.Content != "" {
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

		// 处理 Tools
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

		// 处理 ParallelToolCalls
		if opts.ParallelToolCalls != nil {
			val := *opts.ParallelToolCalls
			req.ParallelToolCalls = val
		}

		// 处理 ToolChoice（标准实现）
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

		if len(opts.Format) > 0 {
			req.ResponseFormat = &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			}
			req.Messages[len(req.Messages)-1].Content += fmt.Sprintf("\nUse this JSON schema: %s", opts.Format)
		}
	}

	return req
}

// logRequest 记录请求日志
func (c *RemoteAPIChat) logRequest(ctx context.Context, req any, isStream bool) {
	if jsonData, err := json.MarshalIndent(req, "", "  "); err == nil {
		logger.Infof(ctx, "[LLM Request] model=%s, stream=%v, request:\n%s", c.modelName, isStream, secutils.CompactImageDataURLForLog(string(jsonData)))
	}
}

// Chat 进行非流式聊天
func (c *RemoteAPIChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	// 仅在调用方未设置 deadline 时附加一个兜底超时，防止 hung 请求永久阻塞 worker；
	// 调用方若显式设置了更短或更长的 deadline，都会被原样尊重。
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultChatTimeout)
	defer cancel()

	req := c.BuildChatCompletionRequest(messages, opts, false)
	var customEndpoint string
	if c.endpointCustomizer != nil {
		customEndpoint = c.endpointCustomizer(c.baseURL, c.modelID, true)
	}
	// 检查是否需要自定义请求
	if c.requestCustomizer != nil {
		customReq, useRawHTTP := c.requestCustomizer(&req, opts, false)
		if useRawHTTP && customReq != nil {
			return c.chatWithRawHTTP(timeoutCtx, customEndpoint, customReq)
		}
	}

	// 使用自定义请求地址
	if customEndpoint != "" {
		return c.chatWithRawHTTP(timeoutCtx, customEndpoint, &req)
	}

	c.logRequest(timeoutCtx, req, false)
	resp, err := c.client.CreateChatCompletion(timeoutCtx, req)
	if err != nil {
		if isMultimodalNotSupportedError(err) {
			logger.Warnf(timeoutCtx, "[LLM Request] Model %s does not support multimodal, retrying without images", c.modelName)
			cleaned := stripImagesFromMessages(messages)
			req = c.BuildChatCompletionRequest(cleaned, opts, false)
			resp, err = c.client.CreateChatCompletion(timeoutCtx, req)
		}
		if err != nil {
			return nil, fmt.Errorf("create chat completion: %w", err)
		}
	}

	result, err := c.parseCompletionResponse(&resp)
	if err != nil {
		return nil, err
	}
	logger.Infof(timeoutCtx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
		c.modelName, result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	return result, nil
}

// chatWithRawHTTP 使用原始 HTTP 请求进行聊天（供自定义请求使用）
func (c *RemoteAPIChat) chatWithRawHTTP(ctx context.Context, endpoint string, customReq any) (*types.ChatResponse, error) {
	jsonData, err := json.Marshal(customReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if endpoint == "" {
		endpoint = c.baseURL + "/chat/completions"
	}
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	logger.Infof(ctx, "[LLM Request] Remote HTTP, endpoint=%s, model=%s, raw HTTP request:\n%s",
		endpoint, c.modelName, secutils.CompactImageDataURLForLog(string(jsonData)))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	if c.headerCustomizer != nil {
		if err := c.headerCustomizer(httpReq, jsonData); err != nil {
			return nil, fmt.Errorf("customize headers: %w", err)
		}
	} else if c.provider == provider.ProviderAzureOpenAI {
		httpReq.Header.Set("api-key", c.apiKey)
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	// 注入用户自定义 header（保留头会在工具内部自动跳过）
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	logger.Infof(ctx, "[LLM Request] Remote HTTP, endpoint=%s, model=%s",
		endpoint, c.modelName)

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp openai.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result, err := c.parseCompletionResponse(&chatResp)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
		c.modelName, result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	return result, nil
}

// parseCompletionResponse 解析非流式响应
func (c *RemoteAPIChat) parseCompletionResponse(resp *openai.ChatCompletionResponse) (*types.ChatResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}

	choice := resp.Choices[0]

	// 处理思考模型的输出：移除 <think></think> 标签包裹的思考过程
	// 为设置了 Thinking=false 但模型仍返回思考内容的情况和部分不支持Thinking=false的思考模型(例如Miniax-M2.1)提供兜底策略
	content := removeThinkingContent(choice.Message.Content)

	response := &types.ChatResponse{
		Content:      content,
		FinishReason: string(choice.FinishReason),
		Usage: types.TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	if len(choice.Message.ToolCalls) > 0 {
		response.ToolCalls = make([]types.LLMToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			response.ToolCalls = append(response.ToolCalls, types.LLMToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: types.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	return response, nil
}

// removeThinkingContent 移除思考模型输出中的 <think></think> 思考过程
// 仅当内容以 <think> 开头时才处理
func removeThinkingContent(content string) string {
	const thinkStartTag = "<think>"
	const thinkEndTag = "</think>"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, thinkStartTag) {
		return content
	}

	// 查找最后一个 </think> 标签（处理嵌套情况）
	if lastEndIdx := strings.LastIndex(trimmed, thinkEndTag); lastEndIdx != -1 {
		if result := strings.TrimSpace(trimmed[lastEndIdx+len(thinkEndTag):]); result != "" {
			return result
		}
		return ""
	}

	return "" // 未找到 </think>，可能思考内容过长被截断，返回空字符串
}

// ChatStream 进行流式聊天
func (c *RemoteAPIChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	// 仅在调用方未设置 deadline 时附加兜底超时；流式调用默认超时更长，
	// 因为带思考/推理的模型可能数十秒甚至几分钟才产出首 token。
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultStreamTimeout)

	req := c.BuildChatCompletionRequest(messages, opts, true)

	var customEndpoint string
	if c.endpointCustomizer != nil {
		customEndpoint = c.endpointCustomizer(c.baseURL, c.modelID, true)
	}

	// 检查是否需要自定义请求
	if c.requestCustomizer != nil {
		customReq, useRawHTTP := c.requestCustomizer(&req, opts, true)
		if useRawHTTP && customReq != nil {
			ch, err := c.chatStreamWithRawHTTP(timeoutCtx, customEndpoint, customReq)
			return wrapStreamCancel(ch, err, cancel)
		}
	}
	// 使用自定义请求地址
	if customEndpoint != "" {
		ch, err := c.chatStreamWithRawHTTP(timeoutCtx, customEndpoint, &req)
		return wrapStreamCancel(ch, err, cancel)
	}
	c.logRequest(timeoutCtx, req, true)

	streamChan := make(chan types.StreamResponse)

	stream, err := c.client.CreateChatCompletionStream(timeoutCtx, req)
	if err != nil {
		if isMultimodalNotSupportedError(err) {
			logger.Warnf(timeoutCtx, "[LLM Stream] Model %s does not support multimodal, retrying without images", c.modelName)
			cleaned := stripImagesFromMessages(messages)
			req = c.BuildChatCompletionRequest(cleaned, opts, true)
			stream, err = c.client.CreateChatCompletionStream(timeoutCtx, req)
		}
		if err != nil {
			cancel()
			close(streamChan)
			return nil, fmt.Errorf("create chat completion stream: %w", err)
		}
	}

	go func() {
		defer cancel()
		c.processStream(timeoutCtx, stream, streamChan)
	}()

	return streamChan, nil
}

// wrapStreamCancel 在子 channel 关闭后执行 cancel，避免 timeout context 泄漏。
// 当底层调用直接返回 error 时，立即调用 cancel 并将 error 透出。
func wrapStreamCancel(in <-chan types.StreamResponse, err error, cancel context.CancelFunc) (<-chan types.StreamResponse, error) {
	if err != nil {
		cancel()
		return nil, err
	}
	out := make(chan types.StreamResponse)
	go func() {
		defer cancel()
		defer close(out)
		for v := range in {
			out <- v
		}
	}()
	return out, nil
}

// chatStreamWithRawHTTP 使用原始 HTTP 请求进行流式聊天
func (c *RemoteAPIChat) chatStreamWithRawHTTP(ctx context.Context, endpoint string, customReq any) (<-chan types.StreamResponse, error) {
	jsonData, err := json.Marshal(customReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if endpoint == "" {
		endpoint = c.baseURL + "/chat/completions"
	}
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}

	if prettyJSON, pErr := json.MarshalIndent(customReq, "", "  "); pErr == nil {
		logger.Infof(ctx, "[LLM Stream Request] endpoint=%s, model=%s, stream=true, request:\n%s",
			endpoint, c.modelName, secutils.CompactImageDataURLForLog(string(prettyJSON)))
	} else {
		logger.Infof(ctx, "[LLM Stream] endpoint=%s, model=%s", endpoint, c.modelName)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	if c.headerCustomizer != nil {
		if err := c.headerCustomizer(httpReq, jsonData); err != nil {
			return nil, fmt.Errorf("customize headers: %w", err)
		}
	} else if c.provider == provider.ProviderAzureOpenAI {
		httpReq.Header.Set("api-key", c.apiKey)
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	// 注入用户自定义 header（保留头会在工具内部自动跳过）
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	streamChan := make(chan types.StreamResponse)

	go c.processRawHTTPStream(ctx, resp, streamChan)

	return streamChan, nil
}

// processStream 处理 OpenAI SDK 流式响应
func (c *RemoteAPIChat) processStream(ctx context.Context, stream *openai.ChatCompletionStream, streamChan chan types.StreamResponse) {
	defer close(streamChan)
	defer stream.Close()

	state := newStreamState()

	for {
		response, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				if state.usage != nil {
					logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
						c.modelName, state.usage.PromptTokens, state.usage.CompletionTokens, state.usage.TotalTokens)
				}
				toolCalls := state.buildOrderedToolCalls()
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "",
					Done:         true,
					ToolCalls:    toolCalls,
					Usage:        state.usage,
					FinishReason: state.lastFinishReason,
				}
			} else {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeError,
					Content:      err.Error(),
					Done:         true,
				}
			}
			return
		}

		if response.Usage != nil {
			state.usage = &types.TokenUsage{
				PromptTokens:     response.Usage.PromptTokens,
				CompletionTokens: response.Usage.CompletionTokens,
				TotalTokens:      response.Usage.TotalTokens,
			}
		}

		if len(response.Choices) > 0 {
			c.processStreamDelta(ctx, &response.Choices[0], state, streamChan, response.Choices[0].Delta.ReasoningContent)
		}
	}
}

// processRawHTTPStream 处理原始 HTTP 流式响应
func (c *RemoteAPIChat) processRawHTTPStream(ctx context.Context, resp *http.Response, streamChan chan types.StreamResponse) {
	defer close(streamChan)
	defer resp.Body.Close()

	state := newStreamState()
	reader := NewSSEReader(resp.Body)

	for {
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				if state.usage != nil {
					logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
						c.modelName, state.usage.PromptTokens, state.usage.CompletionTokens, state.usage.TotalTokens)
				}
				toolCalls := state.buildOrderedToolCalls()
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "",
					Done:         true,
					ToolCalls:    toolCalls,
					Usage:        state.usage,
				}
			} else {
				logger.Errorf(ctx, "Stream read error: %v", err)
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeError,
					Content:      err.Error(),
					Done:         true,
				}
			}
			return
		}

		if event == nil {
			continue
		}

		if event.Done {
			if state.usage != nil {
				logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
					c.modelName, state.usage.PromptTokens, state.usage.CompletionTokens, state.usage.TotalTokens)
			}
			toolCalls := state.buildOrderedToolCalls()
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "",
				Done:         true,
				ToolCalls:    toolCalls,
				Usage:        state.usage,
			}
			return
		}

		if event.Data == nil {
			continue
		}

		// 使用局部结构体进行一次性解析，同时捕捉标准字段和 vLLM 的 reasoning 字段，避免性能损失
		var streamResp struct {
			openai.ChatCompletionStreamResponse
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					openai.ChatCompletionStreamChoiceDelta
					Reasoning string `json:"reasoning,omitempty"`
				} `json:"delta"`
				FinishReason openai.FinishReason `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal(event.Data, &streamResp); err != nil {
			logger.Errorf(ctx, "Failed to parse stream response: %v", err)
			continue
		}

		if streamResp.Usage != nil {
			state.usage = &types.TokenUsage{
				PromptTokens:     streamResp.Usage.PromptTokens,
				CompletionTokens: streamResp.Usage.CompletionTokens,
				TotalTokens:      streamResp.Usage.TotalTokens,
			}
		}

		if len(streamResp.Choices) > 0 {
			choice := streamResp.Choices[0]
			// 统一获取逻辑（支持标准和 vLLM 两种路径）
			reasoning := choice.Delta.Reasoning
			if reasoning == "" {
				reasoning = choice.Delta.ReasoningContent
			}

			// 构造一个标准 SDK 兼容的 choice 对象传给下游，保证现有逻辑完全不动
			sdkChoice := openai.ChatCompletionStreamChoice{
				Index:        choice.Index,
				Delta:        choice.Delta.ChatCompletionStreamChoiceDelta,
				FinishReason: choice.FinishReason,
			}
			c.processStreamDelta(ctx, &sdkChoice, state, streamChan, reasoning)
		}
	}
}

// streamState 流式处理状态
type streamState struct {
	toolCallMap      map[int]*types.LLMToolCall
	lastFunctionName map[int]string
	nameNotified     map[int]bool
	hasThinking      bool
	fieldExtractors  map[int]*jsonFieldExtractor // per tool-call-index extractors for streaming field extraction
	usage            *types.TokenUsage           // captured from the final stream chunk when include_usage is enabled
	lastFinishReason string                      // last observed finish_reason for EOF handler fallback

	// Diagnostic flags (fire-once) used to log earliest signals of tool_call
	// presence/absence at the OpenAI-protocol level. These are independent of
	// the higher-level ResponseTypeToolCall marker (which only fires once
	// function name has stabilized) and let us distinguish between
	//   (A) no tool_calls field ever observed (true natural-stop), and
	//   (B) tool_calls field observed but marker not yet emitted.
	firstToolCallSeen    bool // true once any delta carried tool_calls
	noToolCallStopLogged bool // true once we logged "stop without tool_calls"
	firstContentSeen     bool // true once delta.Content first appeared
	firstReasoningSeen   bool // true once reasoning_content first appeared
	streamStartedAt      time.Time
}

func newStreamState() *streamState {
	return &streamState{
		toolCallMap:      make(map[int]*types.LLMToolCall),
		lastFunctionName: make(map[int]string),
		nameNotified:     make(map[int]bool),
		hasThinking:      false,
		fieldExtractors:  make(map[int]*jsonFieldExtractor),
		streamStartedAt:  time.Now(),
	}
}

// elapsedMs returns the milliseconds elapsed since the stream state was
// initialized. Used to attach time-since-stream-start to fire-once diagnostic
// logs so a single grep can reveal the temporal layout of a single stream
// (TTFC / TTFT / first-tool-call / natural-stop confirmation, etc).
func (s *streamState) elapsedMs() int64 {
	if s.streamStartedAt.IsZero() {
		return 0
	}
	return time.Since(s.streamStartedAt).Milliseconds()
}

func (s *streamState) buildOrderedToolCalls() []types.LLMToolCall {
	if len(s.toolCallMap) == 0 {
		return nil
	}
	result := make([]types.LLMToolCall, 0, len(s.toolCallMap))
	for i := 0; i < len(s.toolCallMap); i++ {
		if tc, ok := s.toolCallMap[i]; ok && tc != nil {
			result = append(result, *tc)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// processStreamDelta 处理流式响应的单个 delta
func (c *RemoteAPIChat) processStreamDelta(ctx context.Context, choice *openai.ChatCompletionStreamChoice, state *streamState, streamChan chan types.StreamResponse, reasoningContent string) {
	delta := choice.Delta
	isDone := string(choice.FinishReason) != ""

	// Track finish_reason for EOF handler fallback
	if isDone {
		state.lastFinishReason = string(choice.FinishReason)
	}

	// 处理 tool calls
	if len(delta.ToolCalls) > 0 {
		c.processToolCallsDelta(ctx, delta.ToolCalls, state, streamChan)
	}

	// Earliest reliable "no tool_calls" signal at the OpenAI-protocol level:
	// finish_reason=stop arrived AND we never observed a tool_calls field on
	// any prior delta. Logged once per stream so callers can grep for the
	// natural-stop entry point without waiting for the higher-level summary.
	if isDone &&
		string(choice.FinishReason) == "stop" &&
		!state.firstToolCallSeen &&
		!state.noToolCallStopLogged {
		logger.Infof(ctx, "[LLM Stream] Natural-stop at OpenAI layer "+
			"(finish=stop, tool_calls field never observed, thinking_seen=%t, "+
			"first_content_seen=%t, elapsed_ms=%d)",
			state.hasThinking, state.firstContentSeen, state.elapsedMs())
		state.noToolCallStopLogged = true
	}

	// 发送思考内容（ReasoningContent，支持 DeepSeek 等模型）
	if reasoningContent != "" {
		// Earliest reasoning_content signal at the OpenAI-protocol level. Fired
		// once per stream so we can distinguish "model emitted thinking before
		// answer" vs "model never produced thinking" when triaging logs.
		if !state.firstReasoningSeen {
			state.firstReasoningSeen = true
			logger.Infof(ctx, "[LLM Stream] First reasoning_content at OpenAI layer "+
				"(len=%d, preview=%q, elapsed_ms=%d)",
				len(reasoningContent), truncateForDebug(reasoningContent, 80), state.elapsedMs())
		}
		state.hasThinking = true
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeThinking,
			Content:      reasoningContent,
			Done:         false,
		}
	}

	// 发送回答内容
	if delta.Content != "" {
		// Earliest delta.Content signal at the OpenAI-protocol level. Fired once
		// per stream so we can measure TTFC (time-to-first-content) and tell
		// "answer started before any tool_call" from "tool_call came first".
		if !state.firstContentSeen {
			state.firstContentSeen = true
			logger.Infof(ctx, "[LLM Stream] First delta.Content at OpenAI layer "+
				"(len=%d, preview=%q, tool_call_seen=%t, thinking_seen=%t, elapsed_ms=%d)",
				len(delta.Content), truncateForDebug(delta.Content, 80),
				state.firstToolCallSeen, state.firstReasoningSeen, state.elapsedMs())
		}
		// If we had thinking content and this is the first answer chunk,
		// send a thinking done event first
		if state.hasThinking {
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeThinking,
				Content:      "",
				Done:         true,
			}
			state.hasThinking = false // Only send once
		}
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      delta.Content,
			Done:         isDone,
			ToolCalls:    state.buildOrderedToolCalls(),
			FinishReason: string(choice.FinishReason),
		}
	}

	if isDone && len(state.toolCallMap) > 0 {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "",
			Done:         true,
			ToolCalls:    state.buildOrderedToolCalls(),
			FinishReason: string(choice.FinishReason),
		}
	}

	// Ensure thinking done is sent when stream finishes without any answer content
	// (e.g., model only produced reasoning then hit finish_reason with empty content).
	if isDone && state.hasThinking {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeThinking,
			Content:      "",
			Done:         true,
		}
		state.hasThinking = false
	}

	// Catch-all: isDone but none of the above branches sent a response with
	// FinishReason (empty content, no tool calls, no thinking). This prevents
	// the finish_reason from being lost in the streaming pipeline.
	if isDone && delta.Content == "" && len(state.toolCallMap) == 0 && !state.hasThinking {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			FinishReason: string(choice.FinishReason),
		}
	}
}

// processToolCallsDelta 处理 tool calls 的增量更新
func (c *RemoteAPIChat) processToolCallsDelta(ctx context.Context, toolCalls []openai.ToolCall, state *streamState, streamChan chan types.StreamResponse) {
	// Earliest signal at the OpenAI-protocol level that this stream will
	// produce at least one tool call. Fires *before* the function name has
	// stabilized, i.e. earlier than the higher-level ResponseTypeToolCall
	// marker downstream consumers see. Useful for distinguishing
	// "tool_calls field arrived but marker not yet emitted" from
	// "tool_calls field truly absent" when triaging stream behavior.
	if !state.firstToolCallSeen && len(toolCalls) > 0 {
		state.firstToolCallSeen = true
		var firstID, firstName string
		for _, tc := range toolCalls {
			if tc.ID != "" {
				firstID = tc.ID
			}
			if tc.Function.Name != "" {
				firstName = tc.Function.Name
			}
			if firstID != "" || firstName != "" {
				break
			}
		}
		logger.Infof(ctx, "[LLM Stream] First tool_calls delta at OpenAI layer "+
			"(count=%d, first_id=%q, first_name=%q, "+
			"first_content_seen=%t, thinking_seen=%t, elapsed_ms=%d)",
			len(toolCalls), firstID, firstName,
			state.firstContentSeen, state.firstReasoningSeen, state.elapsedMs())
	}

	for _, tc := range toolCalls {
		var toolCallIndex int
		if tc.Index != nil {
			toolCallIndex = *tc.Index
		}
		toolCallEntry, exists := state.toolCallMap[toolCallIndex]
		if !exists || toolCallEntry == nil {
			toolCallEntry = &types.LLMToolCall{
				Type: string(tc.Type),
				Function: types.FunctionCall{
					Name:      "",
					Arguments: "",
				},
			}
			state.toolCallMap[toolCallIndex] = toolCallEntry
		}

		if tc.ID != "" {
			toolCallEntry.ID = tc.ID
		}
		if tc.Type != "" {
			toolCallEntry.Type = string(tc.Type)
		}
		if tc.Function.Name != "" {
			// 防御性校验：解决部分供应商（如vLLM Ascend等）在每个流 Chunk 中重复发送完整工具名的问题。
			// 如果当前已存名字与新收到名字一致，则视为冗余重复，不进行叠加。
			if toolCallEntry.Function.Name != tc.Function.Name {
				toolCallEntry.Function.Name += tc.Function.Name
			}
		}

		argsUpdated := false
		if tc.Function.Arguments != "" {
			toolCallEntry.Function.Arguments += tc.Function.Arguments
			argsUpdated = true
		}

		currName := toolCallEntry.Function.Name
		if currName != "" &&
			currName == state.lastFunctionName[toolCallIndex] &&
			argsUpdated &&
			!state.nameNotified[toolCallIndex] &&
			toolCallEntry.ID != "" {
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeToolCall,
				Content:      "",
				Done:         false,
				Data: map[string]interface{}{
					"tool_name":    currName,
					"tool_call_id": toolCallEntry.ID,
				},
			}
			state.nameNotified[toolCallIndex] = true
		}

		state.lastFunctionName[toolCallIndex] = currName

		// Stream final_answer tool arguments as answer-type chunks
		if toolCallEntry.Function.Name == "final_answer" && argsUpdated {
			extractor, exists := state.fieldExtractors[toolCallIndex]
			if !exists {
				extractor = newJSONFieldExtractor("answer")
				state.fieldExtractors[toolCallIndex] = extractor
				// Detect non-incremental arrival: if the first args chunk is large,
				// the model likely returned all arguments at once (non-streaming tool call)
				if len(tc.Function.Arguments) > 200 {
					logger.Warnf(ctx, "[LLM Stream] final_answer args arrived in large chunk (%d bytes), "+
						"model may not support incremental tool call streaming", len(tc.Function.Arguments))
				}
			}
			answerChunk := extractor.Feed(tc.Function.Arguments)
			if answerChunk != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      answerChunk,
					Done:         false,
					Data: map[string]interface{}{
						"source": "final_answer_tool",
					},
				}
			}
		}

		// Stream thinking tool's thought field as thinking-type chunks
		if toolCallEntry.Function.Name == "thinking" && argsUpdated {
			extractor, exists := state.fieldExtractors[toolCallIndex]
			if !exists {
				extractor = newJSONFieldExtractor("thought")
				state.fieldExtractors[toolCallIndex] = extractor
			}
			thoughtChunk := extractor.Feed(tc.Function.Arguments)
			if thoughtChunk != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeThinking,
					Content:      thoughtChunk,
					Done:         false,
					Data: map[string]interface{}{
						"source":       "thinking_tool",
						"tool_call_id": toolCallEntry.ID,
					},
				}
			}
		}
	}
}

// GetModelName 获取模型名称
func (c *RemoteAPIChat) GetModelName() string {
	return c.modelName
}

// GetModelID 获取模型ID
func (c *RemoteAPIChat) GetModelID() string {
	return c.modelID
}

// GetProvider 获取 provider 名称
func (c *RemoteAPIChat) GetProvider() provider.ProviderName {
	return c.provider
}

// GetBaseURL 获取 baseURL
func (c *RemoteAPIChat) GetBaseURL() string {
	return c.baseURL
}

// GetAPIKey 获取 apiKey
func (c *RemoteAPIChat) GetAPIKey() string {
	return c.apiKey
}
