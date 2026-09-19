package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// pluginChat adapts an external model plugin (gRPC ModelPlugin) to the host
// chat.Chat interface. It is constructed by NewChat when the model declares
// source=plugin and a matching plugin is registered under the model provider.
type pluginChat struct {
	providerName string
	modelName    string
	modelID      string
}

func newPluginChat(config *ChatConfig) Chat {
	return &pluginChat{providerName: config.Provider, modelName: config.ModelName, modelID: config.ModelID}
}

func (p *pluginChat) GetModelName() string { return p.modelName }
func (p *pluginChat) GetModelID() string   { return p.modelID }

func (p *pluginChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return nil, err
	}
	defer release()
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	resp, err := client.Chat(callCtx, encodeChatRequest(messages, opts))
	if err != nil {
		return nil, err
	}
	if resp.GetError() != "" {
		return nil, fmt.Errorf("plugin: %s", resp.GetError())
	}
	return decodeChatResponse(resp), nil
}

func (p *pluginChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	client, callCtx, release, err := provider.ResolveExternalModelCall(ctx, p.providerName)
	if err != nil {
		return nil, err
	}
	callCtx = pluginapi.WithModelContext(callCtx, pluginapi.ModelContext{ModelID: p.modelID, ModelName: p.modelName})
	stream, err := client.ChatStream(callCtx, encodeChatRequest(messages, opts))
	if err != nil {
		release()
		return nil, err
	}
	ch := make(chan types.StreamResponse, 8)
	go func() {
		defer release()
		defer close(ch)
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				select {
				case ch <- types.StreamResponse{ResponseType: types.ResponseTypeError, Content: err.Error(), Done: true}:
				case <-ctx.Done():
				}
				return
			}
			resp := decodeStreamChunk(chunk)
			select {
			case ch <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func encodeChatRequest(messages []Message, opts *ChatOptions) *pluginproto.ModelChatRequest {
	req := &pluginproto.ModelChatRequest{}
	for _, m := range messages {
		req.Messages = append(req.Messages, encodeChatMessage(m))
	}
	if opts != nil {
		req.Options = encodeChatOptions(opts)
	}
	return req
}

func encodeChatMessage(m Message) *pluginproto.ModelChatMessage {
	out := &pluginproto.ModelChatMessage{
		Role:             m.Role,
		Content:          m.Content,
		Name:             m.Name,
		ToolCallId:       m.ToolCallID,
		Images:           m.Images,
		ReasoningContent: m.ReasoningContent,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, &pluginproto.ModelChatToolCall{
			Id: tc.ID, Type: tc.Type, FunctionName: tc.Function.Name, FunctionArguments: tc.Function.Arguments,
		})
	}
	for _, part := range m.MultiContent {
		imageURL := ""
		if part.ImageURL != nil {
			imageURL = part.ImageURL.URL
		}
		out.MultiContent = append(out.MultiContent, &pluginproto.ModelChatContentPart{Type: part.Type, Text: part.Text, ImageUrl: imageURL})
	}
	return out
}

func encodeChatOptions(o *ChatOptions) *pluginproto.ModelChatOptions {
	out := &pluginproto.ModelChatOptions{
		Temperature:         o.Temperature,
		TopP:                o.TopP,
		Seed:                int32(o.Seed),
		MaxTokens:           int32(o.MaxTokens),
		MaxCompletionTokens: int32(o.MaxCompletionTokens),
		FrequencyPenalty:    o.FrequencyPenalty,
		PresencePenalty:     o.PresencePenalty,
		ToolChoice:          o.ToolChoice,
		Format:              string(o.Format),
	}
	if o.Thinking != nil {
		out.Thinking = *o.Thinking
	}
	if o.ParallelToolCalls != nil {
		out.ParallelToolCalls = *o.ParallelToolCalls
	}
	for _, t := range o.Tools {
		out.Tools = append(out.Tools, &pluginproto.ModelChatTool{
			Type: t.Type, FunctionName: t.Function.Name, FunctionDescription: t.Function.Description, FunctionParameters: string(t.Function.Parameters),
		})
	}
	return out
}

func decodeChatResponse(resp *pluginproto.ModelChatResponse) *types.ChatResponse {
	out := &types.ChatResponse{
		Content:          resp.GetContent(),
		ReasoningContent: resp.GetReasoningContent(),
		FinishReason:     resp.GetFinishReason(),
	}
	if usage := resp.GetUsage(); usage != nil {
		out.Usage = types.TokenUsage{
			PromptTokens:     int(usage.GetPromptTokens()),
			CompletionTokens: int(usage.GetCompletionTokens()),
			TotalTokens:      int(usage.GetTotalTokens()),
		}
	}
	for _, tc := range resp.GetToolCalls() {
		out.ToolCalls = append(out.ToolCalls, types.LLMToolCall{
			ID:       tc.GetId(),
			Type:     tc.GetType(),
			Function: types.FunctionCall{Name: tc.GetFunctionName(), Arguments: tc.GetFunctionArguments()},
		})
	}
	return out
}

func decodeStreamChunk(chunk *pluginproto.ModelStreamResponse) types.StreamResponse {
	out := types.StreamResponse{
		ResponseType:      types.ResponseTypeAnswer,
		Content:           chunk.GetContent(),
		Done:              chunk.GetDone(),
		FinishReason:      chunk.GetFinishReason(),
		ReasoningContent:  chunk.GetReasoningContent(),
	}
	if usage := chunk.GetUsage(); usage != nil {
		out.Usage = &types.TokenUsage{
			PromptTokens:     int(usage.GetPromptTokens()),
			CompletionTokens: int(usage.GetCompletionTokens()),
			TotalTokens:      int(usage.GetTotalTokens()),
		}
	}
	for _, tc := range chunk.GetToolCalls() {
		out.ToolCalls = append(out.ToolCalls, types.LLMToolCall{
			ID:       tc.GetId(),
			Type:     tc.GetType(),
			Function: types.FunctionCall{Name: tc.GetFunctionName(), Arguments: tc.GetFunctionArguments()},
		})
	}
	return out
}
