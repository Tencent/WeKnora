package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
)

// RemoteAPIChat 实现了基于 OpenAI 兼容 API 的聊天
// 这是一个通用实现，不包含任何 provider 特定的逻辑
type RemoteAPIChat struct {
	modelName string
	client    *openai.Client
	modelID   string
	baseURL   string
	apiKey    string
	provider  provider.ProviderName

	// requestCustomizer 允许子类自定义请求
	// 返回自定义请求体（如果为 nil 则使用标准请求）和是否需要使用原始 HTTP 请求
	requestCustomizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (customReq any, useRawHTTP bool)
}

// NewRemoteAPIChat 创建远程 API 聊天实例
func NewRemoteAPIChat(chatConfig *ChatConfig) (*RemoteAPIChat, error) {
	apiKey := chatConfig.APIKey
	config := openai.DefaultConfig(apiKey)
	if baseURL := chatConfig.BaseURL; baseURL != "" {
		config.BaseURL = baseURL
	}

	providerName := provider.ProviderName(chatConfig.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(chatConfig.BaseURL)
	}

	return &RemoteAPIChat{
		modelName: chatConfig.ModelName,
		client:    openai.NewClientWithConfig(config),
		modelID:   chatConfig.ModelID,
		baseURL:   chatConfig.BaseURL,
		apiKey:    apiKey,
		provider:  providerName,
	}, nil
}

// SetRequestCustomizer 设置请求自定义器
func (c *RemoteAPIChat) SetRequestCustomizer(customizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool)) {
	c.requestCustomizer = customizer
}

// ConvertMessages 转换消息格式为 OpenAI 格式（导出供子类使用）
func (c *RemoteAPIChat) ConvertMessages(messages []Message) []openai.ChatCompletionMessage {
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role: msg.Role,
		}

		if msg.Content != "" {
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

	if opts != nil {
		if opts.Temperature > 0 {
			req.Temperature = float32(opts.Temperature)
		}
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
		logger.Infof(ctx, "[LLM Request] model=%s, stream=%v, request:\n%s", c.modelName, isStream, string(jsonData))
	}
}

// Chat 进行非流式聊天
func (c *RemoteAPIChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	req := c.BuildChatCompletionRequest(messages, opts, false)

	// 检查是否需要自定义请求
	if c.requestCustomizer != nil {
		customReq, useRawHTTP := c.requestCustomizer(&req, opts, false)
		if useRawHTTP && customReq != nil {
			return c.chatWithRawHTTP(ctx, customReq)
		}
	}

	c.logRequest(ctx, req, false)

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create chat completion: %w", err)
	}

	return c.parseCompletionResponse(&resp)
}

// chatWithRawHTTP 使用原始 HTTP 请求进行聊天（供自定义请求使用）
func (c *RemoteAPIChat) chatWithRawHTTP(ctx context.Context, customReq any) (*types.ChatResponse, error) {
	jsonData, err := json.Marshal(customReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logger.Infof(ctx, "[LLM Request] model=%s, raw HTTP request:\n%s", c.modelName, string(jsonData))

	endpoint := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
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

	return c.parseCompletionResponse(&chatResp)
}

// parseCompletionResponse 解析非流式响应
func (c *RemoteAPIChat) parseCompletionResponse(resp *openai.ChatCompletionResponse) (*types.ChatResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}

	choice := resp.Choices[0]
	response := &types.ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: string(choice.FinishReason),
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
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

// ChatStream 进行流式聊天
func (c *RemoteAPIChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	req := c.BuildChatCompletionRequest(messages, opts, true)

	// 检查是否需要自定义请求
	if c.requestCustomizer != nil {
		customReq, useRawHTTP := c.requestCustomizer(&req, opts, true)
		if useRawHTTP && customReq != nil {
			return c.chatStreamWithRawHTTP(ctx, customReq)
		}
	}

	c.logRequest(ctx, req, true)

	streamChan := make(chan types.StreamResponse)

	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		close(streamChan)
		return nil, fmt.Errorf("create chat completion stream: %w", err)
	}

	go c.processStream(ctx, stream, streamChan)

	return streamChan, nil
}

// chatStreamWithRawHTTP 使用原始 HTTP 请求进行流式聊天
func (c *RemoteAPIChat) chatStreamWithRawHTTP(ctx context.Context, customReq any) (<-chan types.StreamResponse, error) {
	jsonData, err := json.Marshal(customReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logger.Infof(ctx, "[LLM Stream] model=%s, raw HTTP request:\n%s", c.modelName, string(jsonData))

	endpoint := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{}
	resp, err := client.Do(httpReq)
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
			if err.Error() == "EOF" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "",
					Done:         true,
					ToolCalls:    state.buildOrderedToolCalls(),
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

		if len(response.Choices) > 0 {
			c.processStreamDelta(ctx, &response.Choices[0], state, streamChan)
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
			if err.Error() != "EOF" {
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
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "",
				Done:         true,
				ToolCalls:    state.buildOrderedToolCalls(),
			}
			return
		}

		if event.Data == nil {
			continue
		}

		var streamResp openai.ChatCompletionStreamResponse
		if err := json.Unmarshal(event.Data, &streamResp); err != nil {
			logger.Errorf(ctx, "Failed to parse stream response: %v", err)
			continue
		}

		if len(streamResp.Choices) > 0 {
			c.processStreamDelta(ctx, &streamResp.Choices[0], state, streamChan)
		}
	}
}

// streamState 流式处理状态
type streamState struct {
	toolCallMap      map[int]*types.LLMToolCall
	lastFunctionName map[int]string
	nameNotified     map[int]bool
	hasThinking      bool
}

func newStreamState() *streamState {
	return &streamState{
		toolCallMap:      make(map[int]*types.LLMToolCall),
		lastFunctionName: make(map[int]string),
		nameNotified:     make(map[int]bool),
		hasThinking:      false,
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

// processStreamDelta 处理流式响应的单个 delta
func (c *RemoteAPIChat) processStreamDelta(ctx context.Context, choice *openai.ChatCompletionStreamChoice, state *streamState, streamChan chan types.StreamResponse) {
	delta := choice.Delta
	isDone := string(choice.FinishReason) != ""

	// 处理 tool calls
	if len(delta.ToolCalls) > 0 {
		c.processToolCallsDelta(delta.ToolCalls, state, streamChan)
	}

	// 发送思考内容（ReasoningContent，支持 DeepSeek 等模型）
	if delta.ReasoningContent != "" {
		state.hasThinking = true
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeThinking,
			Content:      delta.ReasoningContent,
			Done:         false,
		}
	}

	// 发送回答内容
	if delta.Content != "" {
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
		}
	}

	if isDone && len(state.toolCallMap) > 0 {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "",
			Done:         true,
			ToolCalls:    state.buildOrderedToolCalls(),
		}
	}
}

// processToolCallsDelta 处理 tool calls 的增量更新
func (c *RemoteAPIChat) processToolCallsDelta(toolCalls []openai.ToolCall, state *streamState, streamChan chan types.StreamResponse) {
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
			toolCallEntry.Function.Name += tc.Function.Name
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
