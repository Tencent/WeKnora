package adapters

// anthropic_tools.go: Anthropic Messages-API tool-use wire shapes, ported
// from v1 chat/anthropic_tools.go (upstream 2026-09). Tools arrive as
// invoke.ToolDef (flat fields); responses carry invoke.ToolCall so the
// engine's tool loop stays vendor-agnostic. A cut-off stream often starts
// the next tool_use with {} — executing that fabricated empty call is worse
// than omitting it, so unclosed blocks are dropped (finishReason reports
// "incomplete" to say why).

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
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

// anthropicToolOptions maps the neutral tool vocabulary onto the Messages API
// (openai_wire.go is the openai-family counterpart). ToolChoice "required"
// becomes "any"; a tool name pins tool_choice.type; parallel-tool disable
// rides disable_parallel_tool_use except for the degenerate "none" choice.
func anthropicToolOptions(req *anthropicRequest, opts *invoke.ChatOptions) {
	if opts == nil || len(opts.Tools) == 0 {
		return
	}
	for _, tool := range opts.Tools {
		req.Tools = append(req.Tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		})
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

// anthropicToolResultBlock builds the tool_result content block for a
// invoke.RoleTool message. IDs, JSON and empty tool results are all kept
// verbatim (no trimming — tool output is payload): Anthropic requires a
// result for every tool_use of the previous turn.
func anthropicToolResultBlock(msg invoke.Message) anthropicContentBlock {
	var text strings.Builder
	for _, part := range msg.Content {
		text.WriteString(part.Text)
	}
	return anthropicContentBlock{
		Type:      "tool_result",
		ToolUseID: msg.ToolCallID,
		Content:   text.String(),
	}
}

// anthropicToolUseBlocks builds the assistant-side blocks: the preceding text
// (if any) stays a text block, then one tool_use block per call. Empty
// arguments still need a JSON object on the wire.
func anthropicToolUseBlocks(content string, calls []invoke.ToolCall) []anthropicContentBlock {
	var blocks []anthropicContentBlock
	if content != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
	}
	for _, call := range calls {
		input := json.RawMessage(call.Function.Arguments)
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		blocks = append(blocks, anthropicContentBlock{
			Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input,
		})
	}
	return blocks
}

// anthropicToolInput accumulates one in-flight tool_use block across its SSE
// frames (content_block_start → input_json_delta* → content_block_stop).
type anthropicToolInput struct {
	call    invoke.ToolCall
	initial string
	json    strings.Builder
	closed  bool
}

// anthropicToolStream assembles tool_use blocks keyed by content-block index.
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
				call: invoke.ToolCall{
					ID:   block.ID,
					Type: "function",
					Function: invoke.FunctionCall{
						Name: block.Name,
					},
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

func (s anthropicToolStream) calls() []invoke.ToolCall {
	indexes := make([]int, 0, len(s))
	for index := range s {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var calls []invoke.ToolCall
	for _, index := range indexes {
		tool := s[index]
		if !tool.closed {
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

// finishReason ports v1's vocabulary fold onto the neutral one: "max_tokens"
// becomes "length" (openai-family parity, agent stop-reasoning keys on it);
// an empty reason or an unclosed tool block means the stream was cut short.
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

// anthropicToolState returns the per-stream tool assembler stored in the
// bridge state (map header copy still shares storage).
func anthropicToolState(state *invoke.StreamBridgeState) anthropicToolStream {
	if v, ok := state.Get("anthropicToolStream"); ok {
		return v.(anthropicToolStream)
	}
	tools := anthropicToolStream{}
	state.Set("anthropicToolStream", tools)
	return tools
}
