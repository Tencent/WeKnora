package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
)

// parseCompletionResponse 解析非流式响应
func (c *RemoteAPIChat) parseCompletionResponse(resp *openai.ChatCompletionResponse) (*types.ChatResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}

	choice := resp.Choices[0]

	// 处理思考模型的输出：移除 <think></think> 标签包裹的思考过程
	// 为设置了 Thinking=false 但模型仍返回思考内容的情况和部分不支持Thinking=false的思考模型(例如Miniax-M2.1)提供兜底策略
	content := removeThinkingContent(choice.Message.Content)

	response := &types.ChatResponse{
		Content:      content,
		FinishReason: string(choice.FinishReason),
		Usage: types.TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CachedTokens:     cachedTokens(resp.Usage.PromptTokensDetails),
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

func (c *RemoteAPIChat) applyCompletionToolCallMetadata(body []byte, result *types.ChatResponse) {
	if result == nil || len(result.ToolCalls) == 0 {
		return
	}

	var raw struct {
		Choices []struct {
			Message struct {
				ToolCalls []json.RawMessage `json:"tool_calls,omitempty"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.Choices) == 0 {
		return
	}

	for i, rawToolCall := range raw.Choices[0].Message.ToolCalls {
		var indexed struct {
			Index *int `json:"index,omitempty"`
		}
		_ = json.Unmarshal(rawToolCall, &indexed)
		idx := i
		if indexed.Index != nil {
			idx = *indexed.Index
		}
		if idx >= 0 && idx < len(result.ToolCalls) {
			result.ToolCalls[idx].ProviderMetadata = c.adapter.ExtractToolCallMetadata(rawToolCall)
		}
	}
}

// removeThinkingContent 移除思考模型输出中的 <think></think> 思考过程
// 仅当内容以 <think> 开头时才处理
func removeThinkingContent(content string) string {
	const thinkStartTag = "<think>"
	const thinkEndTag = "</think>"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, thinkStartTag) {
		return content
	}

	// 查找最后一个 </think> 标签（处理嵌套情况）
	if lastEndIdx := strings.LastIndex(trimmed, thinkEndTag); lastEndIdx != -1 {
		if result := strings.TrimSpace(trimmed[lastEndIdx+len(thinkEndTag):]); result != "" {
			return result
		}
		return ""
	}

	return "" // 未找到 </think>，可能思考内容过长被截断，返回空字符串
}

// processStream 处理 OpenAI SDK 流式响应
func (c *RemoteAPIChat) processStream(
	ctx context.Context,
	stream *openai.ChatCompletionStream,
	streamChan chan types.StreamResponse,
	dumper *streamPacketDumper,
) {
	defer close(streamChan)
	defer stream.Close()

	state := newStreamState()

	for {
		response, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				logUsage(ctx, c.modelName, state.usage)
				toolCalls := state.buildOrderedToolCalls()
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "",
					Done:         true,
					ToolCalls:    toolCalls,
					Usage:        state.usage,
					FinishReason: state.lastFinishReason,
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

		if dumper != nil {
			dumper.WritePacket(response)
		}

		if response.Usage != nil {
			state.usage = &types.TokenUsage{
				PromptTokens:     response.Usage.PromptTokens,
				CompletionTokens: response.Usage.CompletionTokens,
				TotalTokens:      response.Usage.TotalTokens,
				CachedTokens:     cachedTokens(response.Usage.PromptTokensDetails),
			}
		}

		if len(response.Choices) > 0 {
			c.processStreamDelta(ctx, &response.Choices[0], state, streamChan, response.Choices[0].Delta.ReasoningContent)
		}
	}
}

// processRawHTTPStream 处理原始 HTTP 流式响应
func (c *RemoteAPIChat) processRawHTTPStream(
	ctx context.Context,
	resp *http.Response,
	streamChan chan types.StreamResponse,
	dumper *streamPacketDumper,
) {
	defer close(streamChan)
	defer resp.Body.Close()

	state := newStreamState()
	reader := NewSSEReader(resp.Body)

	for {
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				logUsage(ctx, c.modelName, state.usage)
				toolCalls := state.buildOrderedToolCalls()
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "",
					Done:         true,
					ToolCalls:    toolCalls,
					Usage:        state.usage,
				}
			} else {
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
			logUsage(ctx, c.modelName, state.usage)
			toolCalls := state.buildOrderedToolCalls()
			streamChan <- types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "",
				Done:         true,
				ToolCalls:    toolCalls,
				Usage:        state.usage,
			}
			return
		}

		if event.Data == nil {
			continue
		}

		if dumper != nil {
			// 保留上游 SSE data 行的原始 JSON，不经过中间结构体裁剪。
			raw := make([]byte, len(event.Data))
			copy(raw, event.Data)
			dumper.WritePacketRaw(raw)
		}

		// 使用局部结构体进行一次性解析，同时捕捉标准字段和 vLLM 的 reasoning 字段，避免性能损失
		var streamResp struct {
			openai.ChatCompletionStreamResponse
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					openai.ChatCompletionStreamChoiceDelta
					Reasoning string `json:"reasoning,omitempty"`
				} `json:"delta"`
				FinishReason openai.FinishReason `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal(event.Data, &streamResp); err != nil {
			logger.Errorf(ctx, "Failed to parse stream response: %v", err)
			continue
		}

		if streamResp.Usage != nil {
			state.usage = &types.TokenUsage{
				PromptTokens:     streamResp.Usage.PromptTokens,
				CompletionTokens: streamResp.Usage.CompletionTokens,
				TotalTokens:      streamResp.Usage.TotalTokens,
				CachedTokens:     cachedTokens(streamResp.Usage.PromptTokensDetails),
			}
		}

		if len(streamResp.Choices) > 0 {
			choice := streamResp.Choices[0]
			// 统一获取逻辑（支持标准和 vLLM 两种路径）
			reasoning := choice.Delta.Reasoning
			if reasoning == "" {
				reasoning = choice.Delta.ReasoningContent
			}

			// 构造一个标准 SDK 兼容的 choice 对象传给下游，保证现有逻辑完全不动
			sdkChoice := openai.ChatCompletionStreamChoice{
				Index:        choice.Index,
				Delta:        choice.Delta.ChatCompletionStreamChoiceDelta,
				FinishReason: choice.FinishReason,
			}
			c.applyStreamToolCallMetadata(event.Data, state)
			c.processStreamDelta(ctx, &sdkChoice, state, streamChan, reasoning)
		}
	}
}

func (c *RemoteAPIChat) applyStreamToolCallMetadata(data []byte, state *streamState) {
	if state == nil {
		return
	}

	var raw struct {
		Choices []struct {
			Delta struct {
				ToolCalls []json.RawMessage `json:"tool_calls,omitempty"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw.Choices) == 0 {
		return
	}

	for _, rawToolCall := range raw.Choices[0].Delta.ToolCalls {
		metadata := c.adapter.ExtractToolCallMetadata(rawToolCall)
		if len(metadata) == 0 {
			continue
		}
		var indexed struct {
			Index *int `json:"index,omitempty"`
		}
		_ = json.Unmarshal(rawToolCall, &indexed)
		idx := 0
		if indexed.Index != nil {
			idx = *indexed.Index
		}
		state.setToolCallProviderMetadata(idx, metadata)
	}
}

// streamState 流式处理状态
type streamState struct {
	thinkingEmitter
	toolCallMap      map[int]*types.LLMToolCall
	lastFunctionName map[int]string
	nameNotified     map[int]bool
	fieldExtractors  map[int]*jsonFieldExtractor // per tool-call-index extractors for streaming field extraction
	usage            *types.TokenUsage           // captured from the final stream chunk when include_usage is enabled
	lastFinishReason string                      // last observed finish_reason for EOF handler fallback

	// Diagnostic flags (fire-once) used to log earliest signals of tool_call
	// presence/absence at the OpenAI-protocol level. These are independent of
	// the higher-level ResponseTypeToolCall marker (which only fires once
	// function name has stabilized) and let us distinguish between
	//   (A) no tool_calls field ever observed (true natural-stop), and
	//   (B) tool_calls field observed but marker not yet emitted.
	firstToolCallSeen    bool // true once any delta carried tool_calls
	noToolCallStopLogged bool // true once we logged "stop without tool_calls"
	firstContentSeen     bool // true once delta.Content first appeared
	firstReasoningSeen   bool // true once reasoning_content first appeared
	streamStartedAt      time.Time
}

func newStreamState() *streamState {
	return &streamState{
		toolCallMap:      make(map[int]*types.LLMToolCall),
		lastFunctionName: make(map[int]string),
		nameNotified:     make(map[int]bool),
		fieldExtractors:  make(map[int]*jsonFieldExtractor),
		streamStartedAt:  time.Now(),
	}
}

// elapsedMs returns the milliseconds elapsed since the stream state was
// initialized. Used to attach time-since-stream-start to fire-once diagnostic
// logs so a single grep can reveal the temporal layout of a single stream
// (TTFC / TTFT / first-tool-call / natural-stop confirmation, etc).
func (s *streamState) elapsedMs() int64 {
	if s.streamStartedAt.IsZero() {
		return 0
	}
	return time.Since(s.streamStartedAt).Milliseconds()
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

func (s *streamState) setToolCallProviderMetadata(index int, metadata types.ToolCallMetadata) {
	if len(metadata) == 0 {
		return
	}
	toolCallEntry, exists := s.toolCallMap[index]
	if !exists || toolCallEntry == nil {
		toolCallEntry = &types.LLMToolCall{
			Type: "function",
			Function: types.FunctionCall{
				Name:      "",
				Arguments: "",
			},
		}
		s.toolCallMap[index] = toolCallEntry
	}
	toolCallEntry.ProviderMetadata = metadata
}

// processStreamDelta 处理流式响应的单个 delta
func (c *RemoteAPIChat) processStreamDelta(
	ctx context.Context,
	choice *openai.ChatCompletionStreamChoice,
	state *streamState,
	streamChan chan types.StreamResponse,
	reasoningContent string,
) {
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

	// Earliest reliable "no tool_calls" signal at the OpenAI-protocol level:
	// finish_reason=stop arrived AND we never observed a tool_calls field on
	// any prior delta. Logged once per stream so callers can grep for the
	// natural-stop entry point without waiting for the higher-level summary.
	if isDone &&
		string(choice.FinishReason) == "stop" &&
		!state.firstToolCallSeen &&
		!state.noToolCallStopLogged {
		logger.Infof(ctx, "[LLM Stream] Natural-stop at OpenAI layer "+
			"(finish=stop, tool_calls field never observed, thinking_seen=%t, "+
			"first_content_seen=%t, elapsed_ms=%d)",
			state.active, state.firstContentSeen, state.elapsedMs())
		state.noToolCallStopLogged = true
	}

	// 发送思考内容（ReasoningContent，支持 DeepSeek 等模型）
	if reasoningContent != "" {
		// Earliest reasoning_content signal at the OpenAI-protocol level. Fired
		// once per stream so we can distinguish "model emitted thinking before
		// answer" vs "model never produced thinking" when triaging logs.
		if !state.firstReasoningSeen {
			state.firstReasoningSeen = true
			logger.Infof(ctx, "[LLM Stream] First reasoning_content at OpenAI layer "+
				"(len=%d, preview=%q, elapsed_ms=%d)",
				len(reasoningContent), truncateForDebug(reasoningContent, 80), state.elapsedMs())
		}
		state.emit(streamChan, reasoningContent)
	}

	// 发送回答内容
	if delta.Content != "" {
		// Earliest delta.Content signal at the OpenAI-protocol level. Fired once
		// per stream so we can measure TTFC (time-to-first-content) and tell
		// "answer started before any tool_call" from "tool_call came first".
		if !state.firstContentSeen {
			state.firstContentSeen = true
			logger.Infof(ctx, "[LLM Stream] First delta.Content at OpenAI layer "+
				"(len=%d, preview=%q, tool_call_seen=%t, thinking_seen=%t, elapsed_ms=%d)",
				len(delta.Content), truncateForDebug(delta.Content, 80),
				state.firstToolCallSeen, state.firstReasoningSeen, state.elapsedMs())
		}
		// If we had thinking content and this is the first answer chunk,
		// send a thinking done event first.
		state.finish(streamChan)
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
	if isDone {
		state.finish(streamChan)
	}

	// Catch-all: isDone but none of the above branches sent a response with
	// FinishReason (empty content, no tool calls, no thinking). This prevents
	// the finish_reason from being lost in the streaming pipeline.
	if isDone && delta.Content == "" && len(state.toolCallMap) == 0 {
		streamChan <- types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			FinishReason: string(choice.FinishReason),
		}
	}
}

// processToolCallsDelta 处理 tool calls 的增量更新
func (c *RemoteAPIChat) processToolCallsDelta(
	ctx context.Context,
	toolCalls []openai.ToolCall,
	state *streamState,
	streamChan chan types.StreamResponse,
) {
	// Earliest signal at the OpenAI-protocol level that this stream will
	// produce at least one tool call. Fires *before* the function name has
	// stabilized, i.e. earlier than the higher-level ResponseTypeToolCall
	// marker downstream consumers see. Useful for distinguishing
	// "tool_calls field arrived but marker not yet emitted" from
	// "tool_calls field truly absent" when triaging stream behavior.
	if !state.firstToolCallSeen && len(toolCalls) > 0 {
		state.firstToolCallSeen = true
		var firstID, firstName string
		for _, tc := range toolCalls {
			if tc.ID != "" {
				firstID = tc.ID
			}
			if tc.Function.Name != "" {
				firstName = tc.Function.Name
			}
			if firstID != "" || firstName != "" {
				break
			}
		}
		logger.Infof(ctx, "[LLM Stream] First tool_calls delta at OpenAI layer "+
			"(count=%d, first_id=%q, first_name=%q, "+
			"first_content_seen=%t, thinking_seen=%t, elapsed_ms=%d)",
			len(toolCalls), firstID, firstName,
			state.firstContentSeen, state.firstReasoningSeen, state.elapsedMs())
	}

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
				Data: map[string]interface{}{
					"tool_name":    currName,
					"tool_call_id": toolCallEntry.ID,
				},
			}
			state.nameNotified[toolCallIndex] = true
		}

		state.lastFunctionName[toolCallIndex] = currName

		// Stream thinking tool's thought field as thinking-type chunks
		if toolCallEntry.Function.Name == "thinking" && argsUpdated {
			extractor, exists := state.fieldExtractors[toolCallIndex]
			if !exists {
				extractor = newJSONFieldExtractor("thought")
				state.fieldExtractors[toolCallIndex] = extractor
			}
			thoughtChunk := extractor.Feed(tc.Function.Arguments)
			if thoughtChunk != "" {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeThinking,
					Content:      thoughtChunk,
					Done:         false,
					Data: map[string]interface{}{
						"source":       "thinking_tool",
						"tool_call_id": toolCallEntry.ID,
					},
				}
			}
		}
	}
}

// cachedTokens returns the cached prompt-token count from an OpenAI-compatible
// usage detail block, or zero when the provider did not report one. Some
// providers omit PromptTokensDetails entirely, so the nil guard is required.
//
// Note on provider semantics:
//   - Implicit-cache providers (OpenAI, Azure OpenAI, DeepSeek, …) populate
//     `cached_tokens` automatically whenever the prompt prefix matches a
//     previous request — no caller opt-in is required.
//   - Explicit-cache providers (Qwen on Aliyun, Anthropic Claude, …) only
//     populate `cached_tokens` after the caller attaches `cache_control:
//     {"type": "ephemeral"}` to the relevant message / content block. This
//     helper still returns zero for those providers until that opt-in is
//     applied upstream of the request.
func cachedTokens(d *openai.PromptTokensDetails) int {
	if d == nil {
		return 0
	}
	return d.CachedTokens
}
