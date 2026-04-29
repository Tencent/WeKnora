package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/httpx"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/sashabaranov/go-openai"
)

// Streaming chat path. The SDK-based and raw-HTTP streams share a streamState
// accumulator (defined below) and delta-processing helpers in remote_tools.go.

// ChatStream 进行流式聊天
func (c *RemoteAPIChat) ChatStream(
	ctx context.Context, messages []Message, opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	req := c.BuildChatCompletionRequest(messages, opts, true)

	var customEndpoint string
	if c.endpointCustomizer != nil {
		customEndpoint = c.endpointCustomizer(c.baseURL, c.modelID, true)
	}

	// Custom request body forces raw HTTP so provider-specific fields survive.
	if c.requestCustomizer != nil {
		customReq, useRawHTTP := c.requestCustomizer(&req, opts, true)
		if useRawHTTP && customReq != nil {
			return c.chatStreamWithRawHTTP(ctx, customEndpoint, customReq)
		}
	}
	// Custom endpoint without custom body also goes through raw HTTP.
	if customEndpoint != "" {
		return c.chatStreamWithRawHTTP(ctx, customEndpoint, &req)
	}
	c.logRequest(ctx, req, true)

	streamChan := make(chan types.StreamResponse)

	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		if isMultimodalNotSupportedError(err) {
			logger.Warnf(ctx, "[LLM Stream] Model %s does not support multimodal, retrying without images", c.modelName)
			cleaned := stripImagesFromMessages(messages)
			req = c.BuildChatCompletionRequest(cleaned, opts, true)
			stream, err = c.client.CreateChatCompletionStream(ctx, req)
		}
		if err != nil {
			close(streamChan)
			return nil, fmt.Errorf("create chat completion stream: %w", err)
		}
	}

	go c.processStream(ctx, stream, streamChan)

	return streamChan, nil
}

// chatStreamWithRawHTTP 使用原始 HTTP 请求进行流式聊天
func (c *RemoteAPIChat) chatStreamWithRawHTTP(
	ctx context.Context, endpoint string, customReq any,
) (<-chan types.StreamResponse, error) {
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
	if err := c.applyRawAuth(httpReq, jsonData); err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	// 注入用户自定义 header（保留头会在工具内部自动跳过）
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	resp, err := httpx.StreamingClient.Do(httpReq)
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
func (c *RemoteAPIChat) processStream(
	ctx context.Context, stream *openai.ChatCompletionStream, streamChan chan types.StreamResponse,
) {
	defer close(streamChan)
	defer stream.Close()

	state := newStreamState()

	for {
		response, err := stream.Recv()
		if err != nil {
			c.emitStreamTerminal(ctx, state, err, streamChan)
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
func (c *RemoteAPIChat) processRawHTTPStream(
	ctx context.Context, resp *http.Response, streamChan chan types.StreamResponse,
) {
	defer close(streamChan)
	defer resp.Body.Close()

	state := newStreamState()
	reader := NewSSEReader(resp.Body)

	for {
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				c.emitStreamDone(ctx, state, streamChan, false)
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
			c.emitStreamDone(ctx, state, streamChan, false)
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

// emitStreamTerminal handles the Recv error path for the SDK stream: either
// clean EOF (emit Done) or an actual error (emit Error).
func (c *RemoteAPIChat) emitStreamTerminal(
	ctx context.Context, state *streamState, err error, streamChan chan types.StreamResponse,
) {
	if err == io.EOF {
		c.emitStreamDone(ctx, state, streamChan, true)
		return
	}
	streamChan <- types.StreamResponse{
		ResponseType: types.ResponseTypeError,
		Content:      err.Error(),
		Done:         true,
	}
}

// emitStreamDone writes the final Done event to the stream, logging usage if
// captured. includeFinishReason=true mirrors the SDK-path behavior that
// carries the last finish reason into the terminal event.
func (c *RemoteAPIChat) emitStreamDone(
	ctx context.Context, state *streamState, streamChan chan types.StreamResponse, includeFinishReason bool,
) {
	if state.usage != nil {
		logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
			c.modelName, state.usage.PromptTokens, state.usage.CompletionTokens, state.usage.TotalTokens)
	}
	final := types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "",
		Done:         true,
		ToolCalls:    state.buildOrderedToolCalls(),
		Usage:        state.usage,
	}
	if includeFinishReason {
		final.FinishReason = state.lastFinishReason
	}
	streamChan <- final
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
}

func newStreamState() *streamState {
	return &streamState{
		toolCallMap:      make(map[int]*types.LLMToolCall),
		lastFunctionName: make(map[int]string),
		nameNotified:     make(map[int]bool),
		hasThinking:      false,
		fieldExtractors:  make(map[int]*jsonFieldExtractor),
	}
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
