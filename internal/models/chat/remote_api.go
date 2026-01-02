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

// RemoteAPIChat 원격 API 기반 채팅 구현
type RemoteAPIChat struct {
	modelName string
	client    *openai.Client
	modelID   string
	baseURL   string
	apiKey    string
	provider  provider.ProviderName // 라우팅을 위한 공급자 식별자
}

// QwenChatCompletionRequest qwen 모델을 위한 사용자 정의 요청 구조체
type QwenChatCompletionRequest struct {
	openai.ChatCompletionRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"` // qwen 모델 전용 필드
}

// NewRemoteAPIChat 원격 API 채팅 인스턴스 생성
func NewRemoteAPIChat(chatConfig *ChatConfig) (*RemoteAPIChat, error) {
	apiKey := chatConfig.APIKey
	config := openai.DefaultConfig(apiKey)
	if baseURL := chatConfig.BaseURL; baseURL != "" {
		config.BaseURL = baseURL
	}

	// 구성된 공급자 감지 또는 사용
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

// convertMessages 메시지 형식을 OpenAI 형식으로 변환
func (c *RemoteAPIChat) convertMessages(messages []Message) []openai.ChatCompletionMessage {
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role: msg.Role,
		}

		// 내용 처리: assistant 역할의 경우 내용이 비어 있을 수 있음(tool_calls가 있을 때)
		if msg.Content != "" {
			openaiMsg.Content = msg.Content
		}

		// tool calls 처리(assistant 역할)
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

		// tool 역할 메시지 처리(도구 반환 결과)
		if msg.Role == "tool" {
			openaiMsg.ToolCallID = msg.ToolCallID
			openaiMsg.Name = msg.Name
		}

		openaiMessages = append(openaiMessages, openaiMsg)
	}
	return openaiMessages
}

// isQwenModel qwen 모델인지 확인
func (c *RemoteAPIChat) isAliyunQwen3Model() bool {
	return c.provider == provider.ProviderAliyun && provider.IsQwen3Model(c.modelName)
}

// isDeepSeekModel DeepSeek 모델인지 확인
func (c *RemoteAPIChat) isDeepSeekModel() bool {
	return provider.IsDeepSeekModel(c.modelName)
}

// buildQwenChatCompletionRequest qwen 모델의 채팅 요청 매개변수 구성
func (c *RemoteAPIChat) buildQwenChatCompletionRequest(messages []Message,
	opts *ChatOptions, isStream bool,
) QwenChatCompletionRequest {
	req := QwenChatCompletionRequest{
		ChatCompletionRequest: c.buildChatCompletionRequest(messages, opts, isStream),
	}

	// qwen 모델의 경우 비스트리밍 호출에서 enable_thinking: false를 강제로 설정
	if !isStream {
		enableThinking := false
		req.EnableThinking = &enableThinking
	}
	return req
}

// buildChatCompletionRequest 채팅 요청 매개변수 구성
func (c *RemoteAPIChat) buildChatCompletionRequest(messages []Message,
	opts *ChatOptions, isStream bool,
) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:    c.modelName,
		Messages: c.convertMessages(messages),
		Stream:   isStream,
	}
	thinking := false

	// 선택적 매개변수 추가
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

		// Tools 처리 (함수 정의)
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
				// Parameters 변환 (map[string]interface{} -> JSON Schema)
				if tool.Function.Parameters != nil {
					// Parameters는 이미 JSON Schema 형식의 맵이므로 직접 사용
					openaiTool.Function.Parameters = tool.Function.Parameters
				}
				req.Tools = append(req.Tools, openaiTool)
			}
		}

		// ToolChoice 처리
		// ToolChoice는 문자열 또는 ToolChoice 객체일 수 있음
		// "auto", "none", "required"의 경우 문자열 직접 사용
		// 특정 도구 이름의 경우 ToolChoice 객체 사용
		// 참고: 일부 모델(예: DeepSeek)은 tool_choice를 지원하지 않으므로 설정을 건너뛰어야 함
		if opts.ToolChoice != "" {
			// DeepSeek 모델은 tool_choice를 지원하지 않음, 설정 건너뜀(기본 동작은 자동으로 도구 사용)
			if c.isDeepSeekModel() {
				// DeepSeek의 경우 tool_choice를 설정하지 않고 API가 기본 동작을 사용하도록 함
				// tools가 있으면 DeepSeek는 자동으로 사용함
				logger.Infof(context.Background(), "deepseek 모델, tool_choice 건너뜀")
			} else {
				switch opts.ToolChoice {
				case "none", "required", "auto":
					// 문자열 직접 사용
					req.ToolChoice = opts.ToolChoice
				default:
					// 특정 도구 이름, ToolChoice 객체 사용
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

// Chat 비스트리밍 채팅 수행
func (c *RemoteAPIChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	// qwen 모델인 경우 사용자 정의 요청 사용
	if c.isAliyunQwen3Model() {
		return c.chatWithQwen(ctx, messages, opts)
	}

	// 요청 매개변수 구성
	req := c.buildChatCompletionRequest(messages, opts, false)

	// 요청 전송
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

	// Tool Calls 변환
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

// chatWithQwen qwen 모델 처리를 위한 사용자 정의 요청 사용
func (c *RemoteAPIChat) chatWithQwen(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (*types.ChatResponse, error) {
	// qwen 요청 매개변수 구성
	req := c.buildQwenChatCompletionRequest(messages, opts, false)

	// 요청 직렬화
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// URL 구성
	endpoint := c.baseURL + "/chat/completions"

	// HTTP 요청 생성
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 요청 헤더 설정
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	// 요청 전송
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 응답 상태 확인
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	// 응답 파싱
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

	// Tool Calls 변환
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

// ChatStream 스트리밍 채팅 수행
func (c *RemoteAPIChat) ChatStream(ctx context.Context,
	messages []Message, opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	// 요청 매개변수 구성
	req := c.buildChatCompletionRequest(messages, opts, true)

	// 스트리밍 응답 채널 생성
	streamChan := make(chan types.StreamResponse)

	// 스트리밍 요청 시작
	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		close(streamChan)
		return nil, fmt.Errorf("create chat completion stream: %w", err)
	}

	// 백그라운드에서 스트리밍 응답 처리
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

				// tool calls 수집 (스트리밍 응답에서 tool calls는 여러 번에 걸쳐 반환될 수 있음)
				if len(delta.ToolCalls) > 0 {
					for _, tc := range delta.ToolCalls {
						// 이미 존재하는 tool call인지 확인 (index를 통해)
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

						// ID, 유형 업데이트
						if tc.ID != "" {
							toolCallEntry.ID = tc.ID
						}
						if tc.Type != "" {
							toolCallEntry.Type = string(tc.Type)
						}

						// 함수 이름 누적 (여러 번에 걸쳐 반환될 수 있음)
						if tc.Function.Name != "" {
							toolCallEntry.Function.Name += tc.Function.Name
						}

						// 인수 누적 (부분 JSON일 수 있음)
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

				// 내용 블록 전송
				if delta.Content != "" {
					streamChan <- types.StreamResponse{
						ResponseType: types.ResponseTypeAnswer,
						Content:      delta.Content,
						Done:         isDone,
						ToolCalls:    buildOrderedToolCalls(),
					}
				}

				// 마지막 응답인 경우 모든 tool calls를 포함하는 응답 전송 확인
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

// GetModelName 모델 이름 가져오기
func (c *RemoteAPIChat) GetModelName() string {
	return c.modelName
}

// GetModelID 모델 ID 가져오기
func (c *RemoteAPIChat) GetModelID() string {
	return c.modelID
}
