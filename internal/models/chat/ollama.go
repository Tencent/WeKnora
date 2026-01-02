package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	ollamaapi "github.com/ollama/ollama/api"
)

// OllamaChat Ollama 기반 채팅 구현
type OllamaChat struct {
	modelName     string
	modelID       string
	ollamaService *ollama.OllamaService
}

// NewOllamaChat Ollama 채팅 인스턴스 생성
func NewOllamaChat(config *ChatConfig, ollamaService *ollama.OllamaService) (*OllamaChat, error) {
	return &OllamaChat{
		modelName:     config.ModelName,
		modelID:       config.ModelID,
		ollamaService: ollamaService,
	}, nil
}

// convertMessages 메시지 형식을 Ollama API 형식으로 변환
func (c *OllamaChat) convertMessages(messages []Message) []ollamaapi.Message {
	ollamaMessages := make([]ollamaapi.Message, 0, len(messages))
	for _, msg := range messages {
		msgOllama := ollamaapi.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			ToolCalls: c.toolCallFrom(msg.ToolCalls),
		}
		if msg.Role == "tool" {
			msgOllama.ToolName = msg.Name
		}
		ollamaMessages = append(ollamaMessages, msgOllama)
	}
	return ollamaMessages
}

// buildChatRequest 채팅 요청 매개변수 구성
func (c *OllamaChat) buildChatRequest(messages []Message, opts *ChatOptions, isStream bool) *ollamaapi.ChatRequest {
	// 스트림 플래그 설정
	streamFlag := isStream

	// 요청 매개변수 구성
	chatReq := &ollamaapi.ChatRequest{
		Model:    c.modelName,
		Messages: c.convertMessages(messages),
		Stream:   &streamFlag,
		Options:  make(map[string]interface{}),
	}

	// 선택적 매개변수 추가
	if opts != nil {
		if opts.Temperature > 0 {
			chatReq.Options["temperature"] = opts.Temperature
		}
		if opts.TopP > 0 {
			chatReq.Options["top_p"] = opts.TopP
		}
		if opts.MaxTokens > 0 {
			chatReq.Options["num_predict"] = opts.MaxTokens
		}
		if opts.Thinking != nil {
			chatReq.Think = &ollamaapi.ThinkValue{
				Value: *opts.Thinking,
			}
		}
		if len(opts.Format) > 0 {
			chatReq.Format = opts.Format
		}
		if len(opts.Tools) > 0 {
			chatReq.Tools = c.toolFrom(opts.Tools)
		}
	}

	return chatReq
}

// Chat 비스트리밍 채팅 수행
func (c *OllamaChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	// 모델 가용성 확인
	if err := c.ensureModelAvailable(ctx); err != nil {
		return nil, err
	}

	// 요청 매개변수 구성
	chatReq := c.buildChatRequest(messages, opts, false)

	// 요청 로그 기록
	logger.GetLogger(ctx).Infof("모델 %s에 채팅 요청 전송", c.modelName)

	var responseContent string
	var toolCalls []types.LLMToolCall
	var promptTokens, completionTokens int

	// Ollama 클라이언트를 사용하여 요청 전송
	err := c.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
		responseContent = resp.Message.Content
		toolCalls = c.toolCallTo(resp.Message.ToolCalls)

		// 토큰 수 가져오기
		if resp.EvalCount > 0 {
			promptTokens = resp.PromptEvalCount
			completionTokens = resp.EvalCount - promptTokens
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("채팅 요청 실패: %w", err)
	}

	// 응답 구성
	return &types.ChatResponse{
		Content:   responseContent,
		ToolCalls: toolCalls,
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}, nil
}

// ChatStream 스트리밍 채팅 수행
func (c *OllamaChat) ChatStream(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	// 모델 가용성 확인
	if err := c.ensureModelAvailable(ctx); err != nil {
		return nil, err
	}

	// 요청 매개변수 구성
	chatReq := c.buildChatRequest(messages, opts, true)

	// 요청 로그 기록
	logger.GetLogger(ctx).Infof("모델 %s에 스트리밍 채팅 요청 전송", c.modelName)

	// 스트리밍 응답 채널 생성
	streamChan := make(chan types.StreamResponse)

	// 고루틴을 시작하여 스트리밍 응답 처리
	go func() {
		defer close(streamChan)

		err := c.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
			if resp.Message.Content != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      resp.Message.Content,
					Done:         false,
				}
			}

			if len(resp.Message.ToolCalls) > 0 {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeToolCall,
					ToolCalls:    c.toolCallTo(resp.Message.ToolCalls),
					Done:         false,
				}
			}

			if resp.Done {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Done:         true,
				}
			}

			return nil
		})
		if err != nil {
			logger.GetLogger(ctx).Errorf("스트리밍 채팅 요청 실패: %v", err)
			// 오류 응답 전송
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      err.Error(),
				Done:         true,
			}
		}
	}()

	return streamChan, nil
}

// 모델 가용성 확인
func (c *OllamaChat) ensureModelAvailable(ctx context.Context) error {
	logger.GetLogger(ctx).Infof("모델 %s 가용성 확인", c.modelName)
	return c.ollamaService.EnsureModelAvailable(ctx, c.modelName)
}

// GetModelName 모델 이름 가져오기
func (c *OllamaChat) GetModelName() string {
	return c.modelName
}

// GetModelID 모델 ID 가져오기
func (c *OllamaChat) GetModelID() string {
	return c.modelID
}

// toolFrom 이 모듈의 Tool을 Ollama의 Tool로 변환
func (c *OllamaChat) toolFrom(tools []Tool) ollamaapi.Tools {
	if len(tools) == 0 {
		return nil
	}
	ollamaTools := make(ollamaapi.Tools, 0, len(tools))
	for _, tool := range tools {
		function := ollamaapi.ToolFunction{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
		}
		if len(tool.Function.Parameters) > 0 {
			_ = json.Unmarshal(tool.Function.Parameters, &function.Parameters)
		}

		ollamaTools = append(ollamaTools, ollamaapi.Tool{
			Type:     tool.Type,
			Function: function,
		})
	}
	return ollamaTools
}

// toolTo Ollama의 Tool을 이 모듈의 Tool로 변환
func (c *OllamaChat) toolTo(ollamaTools ollamaapi.Tools) []Tool {
	if len(ollamaTools) == 0 {
		return nil
	}
	tools := make([]Tool, 0, len(ollamaTools))
	for _, tool := range ollamaTools {
		paramsBytes, _ := json.Marshal(tool.Function.Parameters)
		tools = append(tools, Tool{
			Type: tool.Type,
			Function: FunctionDef{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  paramsBytes,
			},
		})
	}
	return tools
}

// toolCallFrom 이 모듈의 ToolCall을 Ollama의 ToolCall로 변환
func (c *OllamaChat) toolCallFrom(toolCalls []ToolCall) []ollamaapi.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	ollamaToolCalls := make([]ollamaapi.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		var args map[string]interface{}
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		ollamaToolCalls = append(ollamaToolCalls, ollamaapi.ToolCall{
			Function: ollamaapi.ToolCallFunction{
				Index:     tools2i(tc.ID),
				Name:      tc.Function.Name,
				Arguments: args,
			},
		})
	}
	return ollamaToolCalls
}

// toolCallTo Ollama의 ToolCall을 이 모듈의 ToolCall로 변환
func (c *OllamaChat) toolCallTo(ollamaToolCalls []ollamaapi.ToolCall) []types.LLMToolCall {
	if len(ollamaToolCalls) == 0 {
		return nil
	}
	toolCalls := make([]types.LLMToolCall, 0, len(ollamaToolCalls))
	for _, tc := range ollamaToolCalls {
		argsBytes, _ := json.Marshal(tc.Function.Arguments)
		toolCalls = append(toolCalls, types.LLMToolCall{
			ID:   tooli2s(tc.Function.Index),
			Type: "function",
			Function: types.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: string(argsBytes),
			},
		})
	}
	return toolCalls
}

func tooli2s(i int) string {
	return strconv.Itoa(i)
}

func tools2i(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}
