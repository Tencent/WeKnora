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
	"github.com/Tencent/WeKnora/internal/models/internal/httpx"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/sashabaranov/go-openai"
)

// Non-streaming chat path. Split out from remote_api.go so the RemoteAPIChat
// struct (see remote.go) and the streaming path (see remote_stream.go) live in
// their own files.

// Chat 进行非流式聊天
func (c *RemoteAPIChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	req := c.BuildChatCompletionRequest(messages, opts, false)
	var customEndpoint string
	if c.endpointCustomizer != nil {
		customEndpoint = c.endpointCustomizer(c.baseURL, c.modelID, true)
	}
	// Allow provider customizers to return a bespoke body; when useRawHTTP is
	// true we bypass the SDK entirely so provider-specific fields (e.g.
	// Qwen3's enable_thinking) aren't filtered out.
	if c.requestCustomizer != nil {
		customReq, useRawHTTP := c.requestCustomizer(&req, opts, false)
		if useRawHTTP && customReq != nil {
			return c.chatWithRawHTTP(ctx, customEndpoint, customReq)
		}
	}

	// A custom endpoint without a custom body still goes through raw HTTP so
	// we can hit the override URL.
	if customEndpoint != "" {
		return c.chatWithRawHTTP(ctx, customEndpoint, &req)
	}

	c.logRequest(ctx, req, false)
	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		// Some models don't accept images; retry without images once before
		// giving up. This matches behavior from before the refactor.
		if isMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Request] Model %s does not support multimodal, retrying without images", c.modelName)
			cleaned := stripImagesFromMessages(messages)
			req = c.BuildChatCompletionRequest(cleaned, opts, false)
			resp, err = c.client.CreateChatCompletion(ctx, req)
		}
		if err != nil {
			return nil, fmt.Errorf("create chat completion: %w", err)
		}
	}

	result, err := c.parseCompletionResponse(&resp)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
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

	if err := c.applyRawAuth(httpReq, jsonData); err != nil {
		return nil, err
	}

	// 注入用户自定义 header（保留头会在工具内部自动跳过）
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	logger.Infof(ctx, "[LLM Request] Remote HTTP, endpoint=%s, model=%s", endpoint, c.modelName)

	resp, err := httpx.StreamingClient.Do(httpReq)
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

// applyRawAuth sets Authorization / api-key headers for raw HTTP calls. When
// a headerCustomizer is registered (WeKnoraCloud signer) it takes full control;
// otherwise Azure uses the api-key header and everything else uses Bearer.
func (c *RemoteAPIChat) applyRawAuth(req *http.Request, body []byte) error {
	if c.headerCustomizer != nil {
		if err := c.headerCustomizer(req, body); err != nil {
			return fmt.Errorf("customize headers: %w", err)
		}
		return nil
	}
	if c.provider == provider.ProviderAzureOpenAI {
		req.Header.Set("api-key", c.apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return nil
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

// removeThinkingContent strips <think>...</think> reasoning blocks from model
// output. Only activates when the content begins with <think>, so regular
// responses are not touched.
func removeThinkingContent(content string) string {
	const thinkStartTag = "<think>"
	const thinkEndTag = "</think>"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, thinkStartTag) {
		return content
	}

	// Use the last </think> so nested blocks don't truncate legitimate output.
	if lastEndIdx := strings.LastIndex(trimmed, thinkEndTag); lastEndIdx != -1 {
		if result := strings.TrimSpace(trimmed[lastEndIdx+len(thinkEndTag):]); result != "" {
			return result
		}
		return ""
	}

	// No closing tag means the stream was cut off mid-think; treat as empty.
	return ""
}
