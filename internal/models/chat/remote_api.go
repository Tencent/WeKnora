package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	return &RemoteAPIChat{
		modelName: chatConfig.ModelName,
		client:    openai.NewClientWithConfig(config),
		modelID:   chatConfig.ModelID,
		baseURL:   chatConfig.BaseURL,
		apiKey:    apiKey,
	}, nil
}

// convertMessages 转换消息格式为OpenAI格式
func (c *RemoteAPIChat) convertMessages(messages []Message) []openai.ChatCompletionMessage {
	openaiMessages := make([]openai.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		openaiMessages[i] = openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return openaiMessages
}

// isQwenModel 检查是否为 qwen 模型
func (c *RemoteAPIChat) isAliyunQwen3Model() bool {
	return strings.HasPrefix(c.modelName, "qwen3-") && c.baseURL == "https://dashscope.aliyuncs.com/compatible-mode/v1"
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

	// 转换响应格式
	return &types.ChatResponse{
		Content: resp.Choices[0].Message.Content,
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}, nil
}

// chatWithQwen 使用自定义请求处理 qwen 模型
func (c *RemoteAPIChat) chatWithQwen(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
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

	// 转换响应格式
	return &types.ChatResponse{
		Content: chatResp.Choices[0].Message.Content,
		Usage: struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		},
	}, nil
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

		for {
			response, err := stream.Recv()
			if err != nil {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Done:         true,
				}
				return
			}
			if len(response.Choices) > 0 {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      response.Choices[0].Delta.Content,
					Done:         false,
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
