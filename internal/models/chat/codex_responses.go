package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type codexResponsesRequest struct {
	Model             string               `json:"model"`
	Instructions      string               `json:"instructions,omitempty"`
	Input             []codexResponsesItem `json:"input"`
	Tools             []codexResponsesTool `json:"tools,omitempty"`
	ToolChoice        any                  `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                `json:"parallel_tool_calls,omitempty"`
	Stream            bool                 `json:"stream"`
	Store             bool                 `json:"store"`
	Reasoning         *codexReasoning      `json:"reasoning,omitempty"`
	Text              *codexTextConfig     `json:"text,omitempty"`
}

type codexResponsesItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type codexResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type codexResponsesOutputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexTextConfig struct {
	Format any `json:"format,omitempty"`
}

type codexResponsesEvent struct {
	Type           string                    `json:"type"`
	Delta          string                    `json:"delta,omitempty"`
	Text           string                    `json:"text,omitempty"`
	OutputText     string                    `json:"output_text,omitempty"`
	SequenceNumber int                       `json:"sequence_number,omitempty"`
	OutputIndex    *int                      `json:"output_index,omitempty"`
	ItemID         string                    `json:"item_id,omitempty"`
	CallID         string                    `json:"call_id,omitempty"`
	Name           string                    `json:"name,omitempty"`
	Arguments      string                    `json:"arguments,omitempty"`
	Item           *codexResponsesOutputItem `json:"item,omitempty"`
	Usage          *codexResponsesUsage      `json:"usage,omitempty"`
	Response       *struct {
		Status     string               `json:"status,omitempty"`
		Usage      *codexResponsesUsage `json:"usage,omitempty"`
		Error      *codexError          `json:"error,omitempty"`
		OutputText string               `json:"output_text,omitempty"`
		Incomplete map[string]any       `json:"incomplete_details,omitempty"`
	} `json:"response,omitempty"`
	Error *codexError `json:"error,omitempty"`
}

type codexError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
}

type codexResponsesUsage struct {
	InputTokens        int `json:"input_tokens,omitempty"`
	OutputTokens       int `json:"output_tokens,omitempty"`
	TotalTokens        int `json:"total_tokens,omitempty"`
	PromptTokens       int `json:"prompt_tokens,omitempty"`
	CompletionTokens   int `json:"completion_tokens,omitempty"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
}

func buildCodexResponsesRequest(model string, messages []Message, opts *ChatOptions, stream bool) codexResponsesRequest {
	req := codexResponsesRequest{
		Model:  model,
		Input:  make([]codexResponsesItem, 0, len(messages)),
		Stream: stream,
		Store:  false,
	}
	var systemParts []string
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = textFromMultiContent(msg.MultiContent)
		}
		switch msg.Role {
		case "system":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "assistant":
			if content != "" {
				req.Input = append(req.Input, codexResponsesItem{Role: "assistant", Content: content})
			}
			for _, tc := range msg.ToolCalls {
				req.Input = append(req.Input, codexResponsesItem{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		case "tool":
			req.Input = append(req.Input, codexResponsesItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: content,
			})
		default:
			if content == "" {
				continue
			}
			req.Input = append(req.Input, codexResponsesItem{Role: "user", Content: content})
		}
	}
	req.Instructions = strings.Join(systemParts, "\n\n")
	if strings.TrimSpace(req.Instructions) == "" {
		req.Instructions = "You are a helpful assistant."
	}
	if opts == nil {
		return req
	}
	if opts.Thinking != nil {
		if *opts.Thinking {
			req.Reasoning = &codexReasoning{Effort: "medium", Summary: "auto"}
		} else {
			req.Reasoning = &codexReasoning{Effort: "low", Summary: "auto"}
		}
	}
	if len(opts.Format) > 0 {
		req.Text = &codexTextConfig{Format: codexTextFormat(opts.Format)}
	}
	if len(opts.Tools) > 0 {
		req.Tools = make([]codexResponsesTool, 0, len(opts.Tools))
		for _, tool := range opts.Tools {
			if tool.Function.Name == "" {
				continue
			}
			toolType := tool.Type
			if toolType == "" {
				toolType = "function"
			}
			req.Tools = append(req.Tools, codexResponsesTool{
				Type:        toolType,
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			})
		}
	}
	if opts.ToolChoice != "" {
		switch opts.ToolChoice {
		case "none", "auto", "required":
			req.ToolChoice = opts.ToolChoice
		default:
			req.ToolChoice = map[string]any{"type": "function", "name": opts.ToolChoice}
		}
	}
	if opts.ParallelToolCalls != nil {
		req.ParallelToolCalls = opts.ParallelToolCalls
	}
	return req
}

func codexTextFormat(raw json.RawMessage) any {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if typ, ok := obj["type"].(string); ok && (typ == "json_schema" || typ == "json_object") {
			return obj
		}
		return map[string]any{
			"type":   "json_schema",
			"name":   "response_format",
			"schema": obj,
		}
	}
	return map[string]any{"type": "json_object"}
}

func parseCodexResponsesSSE(reader io.Reader) (*types.ChatResponse, error) {
	sseReader := NewSSEReader(reader)
	var content strings.Builder
	var reasoning strings.Builder
	var usage *types.TokenUsage
	var finishReason string
	state := newCodexResponsesToolState()

	for {
		event, err := sseReader.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read codex SSE response: %w", err)
		}
		if event.Done {
			break
		}
		if len(event.Data) == 0 {
			continue
		}
		streamEvent, err := decodeCodexResponsesEvent(event.Data)
		if err != nil {
			return nil, err
		}
		if err := codexEventError(streamEvent); err != nil {
			return nil, err
		}
		switch streamEvent.Type {
		case "response.output_text.delta":
			content.WriteString(streamEvent.deltaText())
		case "response.reasoning_summary_text.delta":
			reasoning.WriteString(streamEvent.deltaText())
		case "response.output_item.added", "response.output_item.done",
			"response.function_call_arguments.delta", "response.function_call_arguments.done":
			state.apply(streamEvent)
		case "response.completed":
			finishReason = "stop"
			usage = mergeCodexUsage(usage, usageFromCodexEvent(streamEvent))
		case "response.incomplete":
			finishReason = "incomplete"
			usage = mergeCodexUsage(usage, usageFromCodexEvent(streamEvent))
		}
		usage = mergeCodexUsage(usage, usageFromCodexEvent(streamEvent))
	}

	resultUsage := types.TokenUsage{}
	if usage != nil {
		resultUsage = *usage
	}
	toolCalls := state.buildOrderedToolCalls()
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return &types.ChatResponse{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		Usage:            resultUsage,
	}, nil
}

func processCodexResponsesStream(model string, reader io.Reader, streamChan chan types.StreamResponse) {
	defer close(streamChan)
	sseReader := NewSSEReader(reader)
	var usage *types.TokenUsage
	var finishReason string
	var thinking thinkingEmitter
	state := newCodexResponsesToolState()

	for {
		event, err := sseReader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				thinking.finish(streamChan)
				toolCalls := state.buildOrderedToolCalls()
				streamChan <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, ToolCalls: toolCalls, Usage: usage, FinishReason: codexResponsesFinishReason(finishReason, toolCalls)}
			} else {
				streamChan <- types.StreamResponse{ResponseType: types.ResponseTypeError, Content: err.Error(), Done: true}
			}
			return
		}
		if event.Done {
			thinking.finish(streamChan)
			toolCalls := state.buildOrderedToolCalls()
			streamChan <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, ToolCalls: toolCalls, Usage: usage, FinishReason: codexResponsesFinishReason(finishReason, toolCalls)}
			return
		}
		if len(event.Data) == 0 {
			continue
		}
		streamEvent, err := decodeCodexResponsesEvent(event.Data)
		if err != nil {
			streamChan <- types.StreamResponse{ResponseType: types.ResponseTypeError, Content: err.Error(), Done: true}
			return
		}
		if err := codexEventError(streamEvent); err != nil {
			streamChan <- types.StreamResponse{ResponseType: types.ResponseTypeError, Content: err.Error(), Done: true}
			return
		}
		usage = mergeCodexUsage(usage, usageFromCodexEvent(streamEvent))
		switch streamEvent.Type {
		case "response.reasoning_summary_text.delta":
			if text := streamEvent.deltaText(); text != "" {
				thinking.emit(streamChan, text)
			}
		case "response.output_text.delta":
			if text := streamEvent.deltaText(); text != "" {
				thinking.finish(streamChan)
				streamChan <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: text}
			}
		case "response.output_item.added", "response.output_item.done",
			"response.function_call_arguments.delta", "response.function_call_arguments.done":
			if tc, notify := state.apply(streamEvent); notify {
				streamChan <- types.StreamResponse{
					ResponseType: types.ResponseTypeToolCall,
					ToolCalls:    state.buildOrderedToolCalls(),
					Data: map[string]interface{}{
						"tool_name":    tc.Function.Name,
						"tool_call_id": tc.ID,
					},
				}
			}
		case "response.completed":
			finishReason = "stop"
		case "response.incomplete":
			finishReason = "incomplete"
		}
		_ = model
	}
}

type codexResponsesToolState struct {
	toolCallMap map[int]*types.LLMToolCall
	notified    map[int]bool
}

func newCodexResponsesToolState() *codexResponsesToolState {
	return &codexResponsesToolState{
		toolCallMap: make(map[int]*types.LLMToolCall),
		notified:    make(map[int]bool),
	}
}

func (s *codexResponsesToolState) apply(event *codexResponsesEvent) (*types.LLMToolCall, bool) {
	if event == nil {
		return nil, false
	}
	idx := 0
	if event.OutputIndex != nil {
		idx = *event.OutputIndex
	}
	item := event.Item
	if item != nil && item.Type != "function_call" {
		return nil, false
	}
	tc := s.toolCallMap[idx]
	if tc == nil {
		tc = &types.LLMToolCall{Type: "function"}
		s.toolCallMap[idx] = tc
	}
	if item != nil {
		if item.CallID != "" {
			tc.ID = item.CallID
		}
		if item.Name != "" {
			tc.Function.Name = item.Name
		}
		if item.Arguments != "" {
			tc.Function.Arguments = item.Arguments
		}
	}
	if event.CallID != "" {
		tc.ID = event.CallID
	}
	if event.Name != "" {
		tc.Function.Name = event.Name
	}
	if event.Arguments != "" {
		tc.Function.Arguments = event.Arguments
	}
	if event.Type == "response.function_call_arguments.delta" && event.Delta != "" {
		tc.Function.Arguments += event.Delta
	}
	if event.Type == "response.function_call_arguments.done" && event.Arguments != "" {
		tc.Function.Arguments = event.Arguments
	}
	if tc.ID == "" && event.ItemID != "" {
		tc.ID = event.ItemID
	}
	notify := tc.ID != "" && tc.Function.Name != "" && !s.notified[idx]
	if notify {
		s.notified[idx] = true
	}
	return tc, notify
}

func (s *codexResponsesToolState) buildOrderedToolCalls() []types.LLMToolCall {
	if len(s.toolCallMap) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(s.toolCallMap))
	for idx := range s.toolCallMap {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	result := make([]types.LLMToolCall, 0, len(s.toolCallMap))
	for _, idx := range indexes {
		tc := s.toolCallMap[idx]
		if tc == nil || tc.ID == "" || tc.Function.Name == "" {
			continue
		}
		if tc.Type == "" {
			tc.Type = "function"
		}
		result = append(result, *tc)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func codexResponsesFinishReason(finishReason string, toolCalls []types.LLMToolCall) string {
	if len(toolCalls) > 0 {
		return "tool_calls"
	}
	return finishReason
}

func decodeCodexResponsesEvent(data []byte) (*codexResponsesEvent, error) {
	var streamEvent codexResponsesEvent
	if err := json.Unmarshal(data, &streamEvent); err != nil {
		return nil, fmt.Errorf("decode codex SSE response: %w", err)
	}
	return &streamEvent, nil
}

func codexEventError(event *codexResponsesEvent) error {
	if event == nil {
		return nil
	}
	if event.Error != nil && event.Error.Message != "" {
		return fmt.Errorf("codex API stream error: %s", event.Error.Message)
	}
	if event.Response != nil && event.Response.Error != nil && event.Response.Error.Message != "" {
		return fmt.Errorf("codex API response error: %s", event.Response.Error.Message)
	}
	if event.Type == "response.failed" {
		return fmt.Errorf("codex API response failed")
	}
	return nil
}

func (e *codexResponsesEvent) deltaText() string {
	if e == nil {
		return ""
	}
	if e.Delta != "" {
		return e.Delta
	}
	if e.Text != "" {
		return e.Text
	}
	return e.OutputText
}

func usageFromCodexEvent(event *codexResponsesEvent) *types.TokenUsage {
	if event == nil {
		return nil
	}
	if event.Response != nil && event.Response.Usage != nil {
		return event.Response.Usage.toTokenUsage()
	}
	if event.Usage != nil {
		return event.Usage.toTokenUsage()
	}
	return nil
}

func (u *codexResponsesUsage) toTokenUsage() *types.TokenUsage {
	if u == nil {
		return nil
	}
	prompt := u.InputTokens
	if prompt == 0 {
		prompt = u.PromptTokens
	}
	completion := u.OutputTokens
	if completion == 0 {
		completion = u.CompletionTokens
	}
	total := u.TotalTokens
	if total == 0 {
		total = prompt + completion
	}
	cached := u.InputTokensDetails.CachedTokens
	if cached == 0 {
		cached = u.PromptTokensDetails.CachedTokens
	}
	return &types.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		CachedTokens:     cached,
	}
}

func mergeCodexUsage(current, next *types.TokenUsage) *types.TokenUsage {
	if next == nil {
		return current
	}
	if current == nil {
		copy := *next
		return &copy
	}
	current.PromptTokens = max(current.PromptTokens, next.PromptTokens)
	current.CompletionTokens = max(current.CompletionTokens, next.CompletionTokens)
	current.TotalTokens = max(current.TotalTokens, next.TotalTokens)
	current.CachedTokens = max(current.CachedTokens, next.CachedTokens)
	if current.TotalTokens == 0 {
		current.TotalTokens = current.PromptTokens + current.CompletionTokens
	}
	return current
}
