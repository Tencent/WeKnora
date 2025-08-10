package chat

import (
	"context"
	"fmt"

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
	ollamaMessages := make([]ollamaapi.Message, len(messages))
	for i, msg := range messages {
		ollamaMessages[i] = ollamaapi.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return ollamaMessages
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
	var promptTokens, completionTokens int

	// 使用 Ollama 客户端发送请求
	err := c.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
		responseContent = resp.Message.Content

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

	// 构建响应
	return &types.ChatResponse{
		Content: responseContent,
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

		err := c.ollamaService.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
			if resp.Message.Content != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      resp.Message.Content,
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
			logger.GetLogger(ctx).Errorf("流式聊天请求失败: %v", err)
			// 发送错误响应
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
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
