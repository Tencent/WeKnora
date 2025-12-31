package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
)

// RemoteAPIChat 实现了基于的聊天
type RemoteAPIChat struct {
	modelName string
	client    *openai.Client
	modelID   string
	baseURL   string
	apiKey    string
	provider  provider.ProviderName // Provider identifier for routing
}

// QwenChatCompletionRequest 用于 qwen 模型的自定义请求结构体
type QwenChatCompletionRequest struct {
	openai.ChatCompletionRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"` // qwen 模型专用字段
}

// NewRemoteAPIChat 调用远程API 聊天实例
func NewRemoteAPIChat(chatConfig *ChatConfig) (*RemoteAPIChat, error) {
	apiKey := chatConfig.APIKey
	config := openai.DefaultConfig(apiKey)
	if baseURL := chatConfig.BaseURL; baseURL != "" {
		config.BaseURL = baseURL
	}

	// Detect or use configured provider
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

// convertMessages 转换消息格式为OpenAI格式
func (c *RemoteAPIChat) convertMessages(messages []Message) []openai.ChatCompletionMessage {
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role: msg.Role,
		}

		// 处理内容：对于 assistant 角色，内容可能为空（当有 tool_calls 时）
		if msg.Content != "" {
			openaiMsg.Content = msg.Content
		}

		// 处理 tool calls（assistant 角色）
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

		// 处理 tool 角色消息（工具返回结果）
		if msg.Role == "tool" {
			openaiMsg.ToolCallID = msg.ToolCallID
			openaiMsg.Name = msg.Name
		}

		openaiMessages = append(openaiMessages, openaiMsg)
	}
	return openaiMessages
}

// isQwenModel 检查是否为 qwen 模型
func (c *RemoteAPIChat) isAliyunQwen3Model() bool {
	return c.provider == provider.ProviderAliyun && provider.IsQwen3Model(c.modelName)
}

// isDeepSeekModel 检查是否为 DeepSeek 模型
func (c *RemoteAPIChat) isDeepSeekModel() bool {
	return provider.IsDeepSeekModel(c.modelName)
}

// buildQwenChatCompletionRequest 构建 qwen 模型的聊天请求参数
func (c *RemoteAPIChat) buildQwenChatCompletionRequest(messages []Message,
	opts *ChatOptions, isStream bool,
) QwenChatCompletionRequest {
	req := QwenChatCompletionRequest{
		ChatCompletionRequest: c.buildChatCompletionRequest(messages, opts, isStream),
	}

	// 对于 qwen 模型，在非流式调用中强制设置 enable_thinking: false
	if !isStream {
		enableThinking := false
		req.EnableThinking = &enableThinking
	}
	return req
}

// buildChatCompletionRequest 构建聊天请求参数
func (c *RemoteAPIChat) buildChatCompletionRequest(messages []Message,
	opts *ChatOptions, isStream bool,
) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:    c.modelName,
		Messages: c.convertMessages(messages),
		Stream:   isStream,
	}
	thinking := false

	// 添加可选参数
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
		if opts.Thinking != nil {
			thinking = *opts.Thinking
		}

		// 处理 Tools（函数定义）
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
				// 转换 Parameters (map[string]interface{} -> JSON Schema)
				if tool.Function.Parameters != nil {
					// Parameters 已经是 JSON Schema 格式的 map，直接使用
					openaiTool.Function.Parameters = tool.Function.Parameters
				}
				req.Tools = append(req.Tools, openaiTool)
			}
		}

		// 处理 ToolChoice
		// ToolChoice 可以是字符串或 ToolChoice 对象
		// 对于 "auto", "none", "required" 直接使用字符串
		// 对于特定工具名称，使用 ToolChoice 对象
		// 注意：某些模型（如 DeepSeek）不支持 tool_choice，需要跳过设置
		if opts.ToolChoice != "" {
			// DeepSeek 模型不支持 tool_choice，跳过设置（默认行为会自动使用工具）
			if c.isDeepSeekModel() {
				// 对于 DeepSeek，不设置 tool_choice，让 API 使用默认行为
				// 如果有 tools，DeepSeek 会自动使用
				logger.Infof(context.Background(), "deepseek model, skip tool_choice")
			} else {
				switch opts.ToolChoice {
				case "none", "required", "auto":
					// 直接使用字符串
					req.ToolChoice = opts.ToolChoice
				default:
					// 特定工具名称，使用 ToolChoice 对象
					req.ToolChoice = openai.ToolChoice{
						Type: "function",
						Function: openai.ToolFunction{
							Name: opts.ToolChoice,
						},
					}
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

	// ChatTemplateKwargs is only supported by custom backends like vLLM.
	// Official APIs (OpenAI, Aliyun, Zhipu, etc.) do not support this parameter
	// and will return 400 Bad Request if it's included.
	if c.provider == provider.ProviderGeneric {
		req.ChatTemplateKwargs = map[string]interface{}{
			"enable_thinking": thinking,
		}
	}

	// Log LLM request for debugging
	if jsonData, err := json.MarshalIndent(req, "", "  "); err == nil {
		logger.Infof(context.Background(), "[LLM Request] model=%s, stream=%v, request:\n%s", c.modelName, isStream, string(jsonData))
	}

	// Log tools/functions separately for clarity
	if len(req.Tools) > 0 {
		toolNames := make([]string, 0, len(req.Tools))
		for _, tool := range req.Tools {
			toolNames = append(toolNames, tool.Function.Name)
		}
		logger.Infof(context.Background(), "[LLM Request] tools_count=%d, tool_names=%v", len(req.Tools), toolNames)
	}

	return req
}

// Chat 进行非流式聊天
func (c *RemoteAPIChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	// 如果是 qwen 模型，使用自定义请求
	if c.isAliyunQwen3Model() {
		return c.chatWithQwen(ctx, messages, opts)
	}

	// 构建请求参数
	req := c.buildChatCompletionRequest(messages, opts, false)

	// 发送请求
	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
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

	// 转换 Tool Calls
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

// chatWithQwen 使用自定义请求处理 qwen 模型
func (c *RemoteAPIChat) chatWithQwen(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (*types.ChatResponse, error) {
	// 构建 qwen 请求参数
	req := c.buildQwenChatCompletionRequest(messages, opts, false)

	// 序列化请求
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 构建 URL
	endpoint := c.baseURL + "/chat/completions"

	// 创建 HTTP 请求
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	// 解析响应
	var chatResp openai.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}

	choice := chatResp.Choices[0]
	response := &types.ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: string(choice.FinishReason),
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		},
	}

	// 转换 Tool Calls
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
func (c *RemoteAPIChat) ChatStream(ctx context.Context,
	messages []Message, opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	// 构建请求参数
	req := c.buildChatCompletionRequest(messages, opts, true)

	// 创建流式响应通道
	streamChan := make(chan types.StreamResponse)

	// 启动流式请求
	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		close(streamChan)
		return nil, fmt.Errorf("create chat completion stream: %w", err)
	}

	// 在后台处理流式响应
	go func() {
		defer close(streamChan)
		defer stream.Close()

		toolCallMap := make(map[int]*types.LLMToolCall)
		lastFunctionName := make(map[int]string)
		nameNotified := make(map[int]bool)

		buildOrderedToolCalls := func() []types.LLMToolCall {
			if len(toolCallMap) == 0 {
				return nil
			}
			result := make([]types.LLMToolCall, 0, len(toolCallMap))
			for i := 0; i < len(toolCallMap); i++ {
				if tc, ok := toolCallMap[i]; ok && tc != nil {
					result = append(result, *tc)
				}
			}
			if len(result) == 0 {
				return nil
			}
			return result
		}

		for {
			response, err := stream.Recv()
			if err != nil {
				// Check if it's a normal end of stream (io.EOF)
				if err.Error() == "EOF" {
					// Normal end of stream, send final response with collected tool calls
					streamChan <- types.StreamResponse{
						ResponseType: types.ResponseTypeAnswer,
						Content:      "",
						Done:         true,
						ToolCalls:    buildOrderedToolCalls(),
					}
				} else {
					// Actual error, send error response
					streamChan <- types.StreamResponse{
						ResponseType: types.ResponseTypeError,
						Content:      err.Error(),
						Done:         true,
					}
				}
				return
			}

			if len(response.Choices) > 0 {
				delta := response.Choices[0].Delta
				isDone := string(response.Choices[0].FinishReason) != ""

				// 收集 tool calls（流式响应中 tool calls 可能分多次返回）
				if len(delta.ToolCalls) > 0 {
					for _, tc := range delta.ToolCalls {
						// 检查是否已经存在该 tool call（通过 index）
						var toolCallIndex int
						if tc.Index != nil {
							toolCallIndex = *tc.Index
						}
						toolCallEntry, exists := toolCallMap[toolCallIndex]
						if !exists || toolCallEntry == nil {
							toolCallEntry = &types.LLMToolCall{
								Type: string(tc.Type),
								Function: types.FunctionCall{
									Name:      "",
									Arguments: "",
								},
							}
							toolCallMap[toolCallIndex] = toolCallEntry
						}

						// 更新 ID、类型
						if tc.ID != "" {
							toolCallEntry.ID = tc.ID
						}
						if tc.Type != "" {
							toolCallEntry.Type = string(tc.Type)
						}

						// 累积函数名称（可能分多次返回）
						if tc.Function.Name != "" {
							toolCallEntry.Function.Name += tc.Function.Name
						}

						// 累积参数（可能为部分 JSON）
						argsUpdated := false
						if tc.Function.Arguments != "" {
							toolCallEntry.Function.Arguments += tc.Function.Arguments
							argsUpdated = true
						}

						currName := toolCallEntry.Function.Name
						if currName != "" &&
							currName == lastFunctionName[toolCallIndex] &&
							argsUpdated &&
							!nameNotified[toolCallIndex] &&
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
							nameNotified[toolCallIndex] = true
						}

						lastFunctionName[toolCallIndex] = currName
					}
				}

				// 发送内容块
				if delta.Content != "" {
					streamChan <- types.StreamResponse{
						ResponseType: types.ResponseTypeAnswer,
						Content:      delta.Content,
						Done:         isDone,
						ToolCalls:    buildOrderedToolCalls(),
					}
				}

				// 如果是最后一次响应，确保发送包含所有 tool calls 的响应
				if isDone && len(toolCallMap) > 0 {
					streamChan <- types.StreamResponse{
						ResponseType: types.ResponseTypeAnswer,
						Content:      "",
						Done:         true,
						ToolCalls:    buildOrderedToolCalls(),
					}
				}
			}
		}
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
