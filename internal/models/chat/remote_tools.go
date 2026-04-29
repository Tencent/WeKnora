package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

// Tool-call + delta handling for the streaming chat path. Split out from
// remote_api.go because it carries the nuanced logic that streams
// `final_answer`/`thinking` tool arguments as their respective response types
// — worth isolating in its own file for readability.

// processStreamDelta 处理流式响应的单个 delta
func (c *RemoteAPIChat) processStreamDelta(ctx context.Context, choice *openai.ChatCompletionStreamChoice, state *streamState, streamChan chan types.StreamResponse, reasoningContent string) {
	delta := choice.Delta
	isDone := string(choice.FinishReason) != ""

	// Track finish_reason for EOF handler fallback
	if isDone {
		state.lastFinishReason = string(choice.FinishReason)
	}

	// 处理 tool calls
	if len(delta.ToolCalls) > 0 {
		c.processToolCallsDelta(ctx, delta.ToolCalls, state, streamChan)
	}

	// 发送思考内容（ReasoningContent，支持 DeepSeek 等模型）
	if reasoningContent != "" {
		state.hasThinking = true
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeThinking,
			Content:      reasoningContent,
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
			FinishReason: string(choice.FinishReason),
		}
	}

	if isDone && len(state.toolCallMap) > 0 {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "",
			Done:         true,
			ToolCalls:    state.buildOrderedToolCalls(),
			FinishReason: string(choice.FinishReason),
		}
	}

	// Ensure thinking done is sent when stream finishes without any answer content
	// (e.g., model only produced reasoning then hit finish_reason with empty content).
	if isDone && state.hasThinking {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeThinking,
			Content:      "",
			Done:         true,
		}
		state.hasThinking = false
	}

	// Catch-all: isDone but none of the above branches sent a response with
	// FinishReason (empty content, no tool calls, no thinking). This prevents
	// the finish_reason from being lost in the streaming pipeline.
	if isDone && delta.Content == "" && len(state.toolCallMap) == 0 && !state.hasThinking {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			FinishReason: string(choice.FinishReason),
		}
	}
}

// processToolCallsDelta 处理 tool calls 的增量更新
func (c *RemoteAPIChat) processToolCallsDelta(ctx context.Context, toolCalls []openai.ToolCall, state *streamState, streamChan chan types.StreamResponse) {
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
			// 防御性校验：解决部分供应商（如vLLM Ascend等）在每个流 Chunk 中重复发送完整工具名的问题。
			// 如果当前已存名字与新收到名字一致，则视为冗余重复，不进行叠加。
			if toolCallEntry.Function.Name != tc.Function.Name {
				toolCallEntry.Function.Name += tc.Function.Name
			}
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
				Data: map[string]any{
					"tool_name":    currName,
					"tool_call_id": toolCallEntry.ID,
				},
			}
			state.nameNotified[toolCallIndex] = true
		}

		state.lastFunctionName[toolCallIndex] = currName

		// Stream final_answer tool arguments as answer-type chunks
		if toolCallEntry.Function.Name == "final_answer" && argsUpdated {
			extractor, ok := state.fieldExtractors[toolCallIndex]
			if !ok {
				extractor = newJSONFieldExtractor("answer")
				state.fieldExtractors[toolCallIndex] = extractor
				// Detect non-incremental arrival: if the first args chunk is large,
				// the model likely returned all arguments at once (non-streaming tool call)
				const bigChunkThreshold = 200
				if len(tc.Function.Arguments) > bigChunkThreshold {
					logger.Warnf(ctx, "[LLM Stream] final_answer args arrived in large chunk (%d bytes), "+
						"model may not support incremental tool call streaming", len(tc.Function.Arguments))
				}
			}
			answerChunk := extractor.Feed(tc.Function.Arguments)
			if answerChunk != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      answerChunk,
					Done:         false,
					Data: map[string]any{
						"source": "final_answer_tool",
					},
				}
			}
		}

		// Stream thinking tool's thought field as thinking-type chunks
		if toolCallEntry.Function.Name == "thinking" && argsUpdated {
			extractor, ok := state.fieldExtractors[toolCallIndex]
			if !ok {
				extractor = newJSONFieldExtractor("thought")
				state.fieldExtractors[toolCallIndex] = extractor
			}
			thoughtChunk := extractor.Feed(tc.Function.Arguments)
			if thoughtChunk != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeThinking,
					Content:      thoughtChunk,
					Done:         false,
					Data: map[string]any{
						"source":       "thinking_tool",
						"tool_call_id": toolCallEntry.ID,
					},
				}
			}
		}
	}
}
