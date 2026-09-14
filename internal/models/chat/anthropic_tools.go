package chat

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

func anthropicToolOptions(req *anthropicRequest, opts *ChatOptions) {
	if opts == nil || len(opts.Tools) == 0 {
		return
	}
	for _, tool := range opts.Tools {
		req.Tools = append(
			req.Tools,
			anthropicTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: tool.Function.Parameters,
			},
		)
	}
	choice := &anthropicToolChoice{Type: "auto"}
	switch opts.ToolChoice {
	case "", "auto":
	case "required":
		choice.Type = "any"
	case "none":
		choice.Type = "none"
	default:
		choice.Type, choice.Name = "tool", opts.ToolChoice
	}
	if opts.ParallelToolCalls != nil && choice.Type != "none" {
		disable := !*opts.ParallelToolCalls
		choice.DisableParallelToolUse = &disable
	}
	req.ToolChoice = choice
}

// Tool calls are assistant content blocks; all parallel results belong in the
// immediately following user message. Keep IDs, JSON and empty tool results.
func anthropicMessages(messages []Message) ([]string, []anthropicMessage) {
	var system []string
	var result []anthropicMessage
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = textFromMultiContent(msg.MultiContent)
		}
		switch {
		case msg.Role == "system":
			if content != "" {
				system = append(system, content)
			}
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			var blocks []anthropicContentBlock
			if content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
			}
			for _, call := range msg.ToolCalls {
				input := json.RawMessage(call.Function.Arguments)
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(
					blocks,
					anthropicContentBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input},
				)
			}
			result = append(result, anthropicMessage{Role: "assistant", Content: blocks})
		case msg.Role == "tool":
			block := anthropicContentBlock{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: msg.Content}
			if len(result) > 0 && result[len(result)-1].Role == "user" {
				if blocks, ok := result[len(result)-1].Content.([]anthropicContentBlock); ok && len(blocks) > 0 &&
					blocks[0].Type == "tool_result" {
					result[len(result)-1].Content = append(blocks, block)
					continue
				}
			}
			result = append(result, anthropicMessage{Role: "user", Content: []anthropicContentBlock{block}})
		default:
			if content == "" {
				continue
			}
			role := "user"
			if msg.Role == "assistant" {
				role = "assistant"
			}
			result = append(result, anthropicMessage{Role: role, Content: content})
		}
	}
	return system, result
}

type anthropicToolInput struct {
	call    types.LLMToolCall
	initial string
	json    strings.Builder
	closed  bool
}

type anthropicToolStream map[int]*anthropicToolInput

func (s anthropicToolStream) consume(event anthropicStreamEvent) {
	switch event.Type {
	case "content_block_start":
		if block := event.ContentBlock; block != nil && block.Type == "tool_use" {
			initial := string(block.Input)
			if initial == "" {
				initial = "{}"
			}
			s[event.Index] = &anthropicToolInput{
				initial: initial,
				call: types.LLMToolCall{
					ID:       block.ID,
					Type:     "function",
					Function: types.FunctionCall{Name: block.Name},
				},
			}
		}
	case "content_block_delta":
		if tool := s[event.Index]; tool != nil && event.Delta != nil && event.Delta.Type == "input_json_delta" {
			tool.json.WriteString(event.Delta.PartialJSON)
		}
	case "content_block_stop":
		if tool := s[event.Index]; tool != nil {
			tool.closed = true
		}
	}
}

func (s anthropicToolStream) calls() []types.LLMToolCall {
	indexes := make([]int, 0, len(s))
	for index := range s {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var calls []types.LLMToolCall
	for _, index := range indexes {
		tool := s[index]
		if !tool.closed {
			// A cut-off stream often starts the next tool_use with {}. Executing
			// that empty object is worse than omitting it: the model never
			// asked to run an incomplete call.
			continue
		}
		call := tool.call
		call.Function.Arguments = tool.initial
		if tool.json.Len() > 0 {
			call.Function.Arguments = tool.json.String()
		}
		calls = append(calls, call)
	}
	return calls
}

func (s anthropicToolStream) finishReason(reason string) string {
	if reason == "max_tokens" {
		return "length"
	}
	if reason == "" {
		return types.FinishReasonIncomplete
	}
	for _, tool := range s {
		if !tool.closed {
			return types.FinishReasonIncomplete
		}
	}
	return reason
}
