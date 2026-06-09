package chat

import (
	"encoding/json"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

// ConvertMessages 转换消息格式为 OpenAI 格式（导出供子类使用）
func (c *RemoteAPIChat) ConvertMessages(messages []Message) []openai.ChatCompletionMessage {
	openaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role: msg.Role,
		}

		// 优先处理多内容消息（包含图片等）
		if len(msg.MultiContent) > 0 {
			openaiMsg.MultiContent = make([]openai.ChatMessagePart, 0, len(msg.MultiContent))
			for _, part := range msg.MultiContent {
				switch part.Type {
				case "text":
					openaiMsg.MultiContent = append(openaiMsg.MultiContent, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: part.Text,
					})
				case "image_url":
					if part.ImageURL != nil {
						openaiMsg.MultiContent = append(openaiMsg.MultiContent, openai.ChatMessagePart{
							Type: openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{
								URL:    part.ImageURL.URL,
								Detail: openai.ImageURLDetail(part.ImageURL.Detail),
							},
						})
					}
				}
			}
		} else if len(msg.Images) > 0 && msg.Role == "user" {
			parts := make([]openai.ChatMessagePart, 0, len(msg.Images)+1)
			for _, imgURL := range msg.Images {
				resolved := resolveImageURLForLLM(imgURL)
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    resolved,
						Detail: openai.ImageURLDetailAuto,
					},
				})
			}
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: msg.Content,
			})
			openaiMsg.MultiContent = parts
		} else if msg.Content != "" {
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

		// Round-trip reasoning_content on assistant turns. MiMo and DeepSeek V3.2+
		// thinking mode reject multi-turn requests where the prior assistant
		// message lacks its reasoning_content with HTTP 400 ("The reasoning_content
		// in the thinking mode must be passed back to the API."). Providers that
		// don't recognize the field ignore it harmlessly.
		if msg.Role == "assistant" && msg.ReasoningContent != "" {
			openaiMsg.ReasoningContent = msg.ReasoningContent
		}

		openaiMessages = append(openaiMessages, openaiMsg)
	}
	return openaiMessages
}

// BuildChatCompletionRequest 构建标准聊天请求参数（导出供子类使用）。
//
// 这是一个不含任何 provider 特定逻辑的通用实现：所有采样参数（temperature /
// top_p / penalties）与 max_tokens 都按 opts 直接映射。供应商相关的特判
// （OpenAI o-series / GPT-5 改用 max_completion_tokens、Moonshot 固定温度等）
// 由对应的 providerAdapter.ShapeRequest 在事后施加，见 provider.go。
func (c *RemoteAPIChat) BuildChatCompletionRequest(
	messages []Message, opts *ChatOptions, isStream bool,
) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:    c.modelName,
		Messages: c.ConvertMessages(messages),
		Stream:   isStream,
	}

	if isStream {
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}

	if opts == nil {
		return req
	}

	req.Temperature = float32(opts.Temperature)
	if opts.TopP > 0 {
		req.TopP = float32(opts.TopP)
	}
	if opts.FrequencyPenalty > 0 {
		req.FrequencyPenalty = float32(opts.FrequencyPenalty)
	}
	if opts.PresencePenalty > 0 {
		req.PresencePenalty = float32(opts.PresencePenalty)
	}

	if opts.MaxTokens > 0 {
		req.MaxTokens = opts.MaxTokens
	}
	if opts.MaxCompletionTokens > 0 {
		req.MaxCompletionTokens = opts.MaxCompletionTokens
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

	// 处理 ParallelToolCalls
	if opts.ParallelToolCalls != nil {
		val := *opts.ParallelToolCalls
		req.ParallelToolCalls = val
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

	return req
}

func (c *RemoteAPIChat) buildProviderOpenAIRequest(
	body any,
	openAIMessages []openai.ChatCompletionMessage,
	messages []Message,
) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal provider request: %w", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal provider request: %w", err)
	}

	providerMessages := make([]map[string]any, 0, len(openAIMessages))
	for i, msg := range openAIMessages {
		msgData, err := json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("marshal provider message: %w", err)
		}
		var msgMap map[string]any
		if err := json.Unmarshal(msgData, &msgMap); err != nil {
			return nil, fmt.Errorf("unmarshal provider message: %w", err)
		}

		if i < len(messages) && len(messages[i].ToolCalls) > 0 && len(msg.ToolCalls) > 0 {
			toolCalls := make([]map[string]any, 0, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				tcData, err := json.Marshal(tc)
				if err != nil {
					return nil, fmt.Errorf("marshal provider tool call: %w", err)
				}
				var tcMap map[string]any
				if err := json.Unmarshal(tcData, &tcMap); err != nil {
					return nil, fmt.Errorf("unmarshal provider tool call: %w", err)
				}
				if j < len(messages[i].ToolCalls) {
					c.adapter.InjectToolCallMetadata(tcMap, messages[i].ToolCalls[j].ProviderMetadata)
				}
				toolCalls = append(toolCalls, tcMap)
			}
			msgMap["tool_calls"] = toolCalls
		}

		providerMessages = append(providerMessages, msgMap)
	}
	out["messages"] = providerMessages
	return out, nil
}

func (c *RemoteAPIChat) shapeProviderRequest(body any, req openai.ChatCompletionRequest, messages []Message) (any, error) {
	if !c.adapter.ForceRawHTTP() {
		return body, nil
	}
	return c.buildProviderOpenAIRequest(body, req.Messages, messages)
}
