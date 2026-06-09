package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/sashabaranov/go-openai"
)

// RemoteAPIChat 实现了基于 OpenAI 兼容 API 的聊天。
// 它本身只负责通用的请求/响应/流式处理；所有 provider 特定行为都委托给
// providerAdapter（见 provider.go），thinking 编码委托给 ThinkingStrategy
// （见 thinking.go）。
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

	// adapter 承载所有 provider 特定行为（thinking / 参数特判 / endpoint / 鉴权 / 消息变换）。
	adapter providerAdapter
	// thinkingOverride 来自 extra_config.thinking_control，非 nil 时覆盖 adapter.Thinking()。
	thinkingOverride ThinkingStrategy
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
		modelName:        modelName,
		client:           openai.NewClientWithConfig(config),
		modelID:          chatConfig.ModelID,
		baseURL:          chatConfig.BaseURL,
		apiKey:           apiKey,
		provider:         providerName,
		appID:            chatConfig.AppID,
		appSecret:        chatConfig.AppSecret,
		customHeaders:    chatConfig.CustomHeaders,
		adapter:          resolveProvider(providerName, modelName),
		thinkingOverride: parseThinkingOverride(chatConfig.ExtraConfig),
	}, nil
}

// authCreds bundles the credentials passed to the adapter's Auth method.
func (c *RemoteAPIChat) authCreds() authCreds {
	return authCreds{APIKey: c.apiKey, AppID: c.appID, AppSecret: c.appSecret}
}

// shapedRequest builds the standard request and applies the adapter's message
// transform and parameter shaping (but not thinking, which may wrap the body).
func (c *RemoteAPIChat) shapedRequest(messages []Message, opts *ChatOptions, isStream bool) openai.ChatCompletionRequest {
	req := c.BuildChatCompletionRequest(messages, opts, isStream)
	req.Messages = c.adapter.TransformMessages(req.Messages)
	c.adapter.ShapeRequest(&req, opts, isStream)
	return req
}

// buildOutbound assembles the final outbound request: the body to send, the
// endpoint override (empty for the standard endpoint), and whether the raw HTTP
// path is required. This is the single place that composes adapter + thinking,
// replacing the former buildRequestCustomizer plumbing.
func (c *RemoteAPIChat) buildOutbound(
	messages []Message, opts *ChatOptions, isStream bool,
) (body any, endpoint string, useRawHTTP bool, err error) {
	req := c.shapedRequest(messages, opts, isStream)

	thinking := c.thinkingOverride
	if thinking == nil {
		thinking = c.adapter.Thinking()
	}
	customBody, useRaw := thinking.Apply(&req, opts, isStream)

	body = &req
	if customBody != nil {
		body = customBody
	}
	body, err = c.shapeProviderRequest(body, req, messages)
	if err != nil {
		return nil, "", false, err
	}
	endpoint = c.adapter.Endpoint(c.baseURL, c.modelID, isStream)
	useRawHTTP = useRaw || c.adapter.ForceRawHTTP() || endpoint != ""
	return body, endpoint, useRawHTTP, nil
}

// logRequest 记录请求日志
func (c *RemoteAPIChat) logRequest(ctx context.Context, req any, isStream bool) {
	if jsonData, err := json.MarshalIndent(req, "", "  "); err == nil {
		logger.Infof(ctx, "[LLM Request] model=%s, stream=%v, request:\n%s",
			c.modelName, isStream, secutils.CompactImageDataURLForLog(string(jsonData)))
	}
}

// Chat 进行非流式聊天
func (c *RemoteAPIChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	// 仅在调用方未设置 deadline 时附加一个兜底超时，防止 hung 请求永久阻塞 worker；
	// 调用方若显式设置了更短或更长的 deadline，都会被原样尊重。
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultChatTimeout)
	defer cancel()

	body, endpoint, useRawHTTP, err := c.buildOutbound(messages, opts, false)
	if err != nil {
		return nil, err
	}
	if useRawHTTP {
		return c.chatWithRawHTTP(timeoutCtx, endpoint, body)
	}

	req := *(body.(*openai.ChatCompletionRequest))
	c.logRequest(timeoutCtx, req, false)
	resp, err := c.client.CreateChatCompletion(timeoutCtx, req)
	if err != nil {
		if isMultimodalNotSupportedError(err) {
			logger.Warnf(timeoutCtx, "[LLM Request] Model %s does not support multimodal, retrying without images", c.modelName)
			cleaned := stripImagesFromMessages(messages)
			req = c.shapedRequest(cleaned, opts, false)
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
	logUsage(timeoutCtx, c.modelName, &result.Usage)
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
	c.adapter.Auth(httpReq, c.authCreds(), jsonData)

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var chatResp openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result, err := c.parseCompletionResponse(&chatResp)
	if err != nil {
		return nil, err
	}
	c.applyCompletionToolCallMetadata(body, result)
	logUsage(ctx, c.modelName, &result.Usage)
	return result, nil
}

// ChatStream 进行流式聊天
func (c *RemoteAPIChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	// 仅在调用方未设置 deadline 时附加兜底超时；流式调用默认超时更长，
	// 因为带思考/推理的模型可能数十秒甚至几分钟才产出首 token。
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultStreamTimeout)

	body, endpoint, useRawHTTP, err := c.buildOutbound(messages, opts, true)
	if err != nil {
		cancel()
		return nil, err
	}
	if useRawHTTP {
		ch, err := c.chatStreamWithRawHTTP(timeoutCtx, endpoint, body)
		return wrapStreamCancel(ch, err, cancel)
	}

	req := *(body.(*openai.ChatCompletionRequest))
	c.logRequest(timeoutCtx, req, true)

	streamDumper := newStreamPacketDumper(c.modelName, &req)
	if streamDumper != nil {
		logger.Infof(timeoutCtx, "[LLM Stream Raw Dump] writing packets to %s", streamDumper.Path())
	}

	streamChan := make(chan types.StreamResponse)

	stream, err := c.client.CreateChatCompletionStream(timeoutCtx, req)
	if err != nil {
		if isMultimodalNotSupportedError(err) {
			logger.Warnf(timeoutCtx, "[LLM Stream] Model %s does not support multimodal, retrying without images", c.modelName)
			cleaned := stripImagesFromMessages(messages)
			req = c.shapedRequest(cleaned, opts, true)
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
		if streamDumper != nil {
			defer streamDumper.Close()
		}
		c.processStream(timeoutCtx, stream, streamChan, streamDumper)
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
	c.adapter.Auth(httpReq, c.authCreds(), jsonData)
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
	streamDumper := newStreamPacketDumper(c.modelName, customReq)
	if streamDumper != nil {
		logger.Infof(ctx, "[LLM Stream Raw Dump] writing packets to %s", streamDumper.Path())
	}

	go func() {
		if streamDumper != nil {
			defer streamDumper.Close()
		}
		c.processRawHTTPStream(ctx, resp, streamChan, streamDumper)
	}()

	return streamChan, nil
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
