package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	ollamaapi "github.com/ollama/ollama/api"
)

// OllamaChat 实现了基于 Ollama 的聊天
type OllamaChat struct {
	modelName     string
	modelID       string
	ollamaService *ollama.OllamaService
}

// NewOllamaChat 创建 Ollama 聊天实例
func NewOllamaChat(config *ChatConfig, ollamaService *ollama.OllamaService) (*OllamaChat, error) {
	return &OllamaChat{
		modelName:     config.ModelName,
		modelID:       config.ModelID,
		ollamaService: ollamaService,
	}, nil
}

// convertMessages 转换消息格式为Ollama API格式
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
		if len(msg.Images) > 0 && msg.Role == "user" {
			for _, imgURL := range msg.Images {
				if imgData := resolveImageForOllama(imgURL); imgData != nil {
					msgOllama.Images = append(msgOllama.Images, imgData)
				}
			}
		}
		ollamaMessages = append(ollamaMessages, msgOllama)
	}
	return ollamaMessages
}

// resolveImageForOllama resolves an image URL into raw bytes for Ollama.
// Handles local serving paths (/files/...), data URIs, and remote HTTP URLs.
func resolveImageForOllama(imageURL string) ollamaapi.ImageData {
	if data := resolveImageURLForOllama(imageURL); data != nil {
		return data
	}
	if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(imageURL)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
		if err != nil {
			return nil
		}
		return data
	}
	return nil
}

// buildChatRequest 构建聊天请求参数
func (c *OllamaChat) buildChatRequest(messages []Message, opts *ChatOptions, isStream bool) *ollamaapi.ChatRequest {
	// 设置流式标志
	streamFlag := isStream

	// 构建请求参数
	chatReq := &ollamaapi.ChatRequest{
		Model:    c.modelName,
		Messages: c.convertMessages(messages),
		Stream:   &streamFlag,
		Options:  make(map[string]interface{}),
	}

	// 添加可选参数
	if opts != nil {
		chatReq.Options["temperature"] = opts.Temperature
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

// Chat 进行非流式聊天
func (c *OllamaChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	// 确保模型可用
	if err := c.ensureModelAvailable(ctx); err != nil {
		return nil, err
	}

	// 构建请求参数
	chatReq := c.buildChatRequest(messages, opts, false)

	// 记录请求日志
	logger.GetLogger(ctx).Infof("发送聊天请求到模型 %s", c.modelName)

	var responseContent string
	var toolCalls []types.LLMToolCall
	var promptTokens, completionTokens int

	// 使用 Ollama 客户端发送请求
	err := c.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
		responseContent = resp.Message.Content
		// 当 Content 为空但 Thinking 有内容时（如推理模型未正确配置 thinking 参数），使用 Thinking 作为兜底
		if responseContent == "" && resp.Message.Thinking != "" {
			responseContent = resp.Message.Thinking
		}
		toolCalls = c.toolCallTo(resp.Message.ToolCalls)

		// 获取token计数
		if resp.EvalCount > 0 {
			promptTokens = resp.PromptEvalCount
			completionTokens = resp.EvalCount - promptTokens
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("聊天请求失败: %w", err)
	}

	totalTokens := promptTokens + completionTokens
	logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
		c.modelName, promptTokens, completionTokens, totalTokens)

	return &types.ChatResponse{
		Content:   responseContent,
		ToolCalls: toolCalls,
		Usage: types.TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		},
	}, nil
}

// ChatStream 进行流式聊天
func (c *OllamaChat) ChatStream(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	// 确保模型可用
	if err := c.ensureModelAvailable(ctx); err != nil {
		return nil, err
	}

	// 构建请求参数
	chatReq := c.buildChatRequest(messages, opts, true)

	// 记录请求日志
	logger.GetLogger(ctx).Infof("发送流式聊天请求到模型 %s", c.modelName)

	// 创建流式响应通道
	streamChan := make(chan types.StreamResponse)

	// 启动goroutine处理流式响应
	go func() {
		defer close(streamChan)

		hasThinking := false
		err := c.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
			// 发送思考内容（支持 Qwen3、DeepSeek 等推理模型）
			if resp.Message.Thinking != "" {
				hasThinking = true
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeThinking,
					Content:      resp.Message.Thinking,
					Done:         false,
				}
			}

			if resp.Message.Content != "" {
				// 思考阶段结束后，发送思考完成事件
				if hasThinking {
					streamChan <- types.StreamResponse{
						ResponseType: types.ResponseTypeThinking,
						Done:         true,
					}
					hasThinking = false
				}
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

				// Ollama returns tool calls as complete objects (not incremental deltas).
				// Log this so we can trace non-streaming answer delivery.
				for _, tc := range resp.Message.ToolCalls {
					if tc.Function.Name == "final_answer" || tc.Function.Name == "thinking" {
						argsBytes, _ := json.Marshal(tc.Function.Arguments)
						logger.Warnf(ctx, "[Ollama Stream] Tool %q arrived non-incrementally (%d bytes args), "+
							"answer will not be token-streamed to frontend",
							tc.Function.Name, len(argsBytes))
					}
				}

				for _, tc := range resp.Message.ToolCalls {
					switch tc.Function.Name {
					case "final_answer":
						if answer, ok := tc.Function.Arguments["answer"].(string); ok && answer != "" {
							streamChan <- types.StreamResponse{
								ResponseType: types.ResponseTypeAnswer,
								Content:      answer,
								Done:         false,
								Data: map[string]interface{}{
									"source": "final_answer_tool",
								},
							}
						}
					case "thinking":
						if thought, ok := tc.Function.Arguments["thought"].(string); ok && thought != "" {
							streamChan <- types.StreamResponse{
								ResponseType: types.ResponseTypeThinking,
								Content:      thought,
								Done:         false,
								Data: map[string]interface{}{
									"source":       "thinking_tool",
									"tool_call_id": tooli2s(tc.Function.Index),
								},
							}
						}
					}
				}
			}

			if resp.Done {
				var usage *types.TokenUsage
				if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
					usage = &types.TokenUsage{
						PromptTokens:     resp.PromptEvalCount,
						CompletionTokens: resp.EvalCount,
						TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
					}
					logger.Infof(ctx, "[LLM Usage] model=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
						c.modelName, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
				}
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Done:         true,
					Usage:        usage,
				}
			}

			return nil
		})
		if err != nil {
			logger.GetLogger(ctx).Errorf("流式聊天请求失败: %v", err)
			// 发送错误响应
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      err.Error(),
				Done:         true,
			}
		}
	}()

	return streamChan, nil
}

// 确保模型可用
func (c *OllamaChat) ensureModelAvailable(ctx context.Context) error {
	logger.GetLogger(ctx).Infof("确保模型 %s 可用", c.modelName)
	return c.ollamaService.EnsureModelAvailable(ctx, c.modelName)
}

// GetModelName 获取模型名称
func (c *OllamaChat) GetModelName() string {
	return c.modelName
}

// GetModelID 获取模型ID
func (c *OllamaChat) GetModelID() string {
	return c.modelID
}

// toolFrom 将本模块的 Tool 转换为 Ollama 的 Tool
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

// toolTo 将 Ollama 的 Tool 转换为本模块的 Tool
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

// toolCallFrom 将本模块的 ToolCall 转换为 Ollama 的 ToolCall
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

// toolCallTo 将 Ollama 的 ToolCall 转换为本模块的 ToolCall
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
