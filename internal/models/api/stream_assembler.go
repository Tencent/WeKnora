package api

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ToolCallDelta is one streamed fragment of a tool call, already lifted out
// of whatever envelope the protocol uses (OpenAI delta.tool_calls, Anthropic
// input_json_delta, Gemini functionCall parts, Responses function_call_arguments.delta).
type ToolCallDelta struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}

// Delta is one protocol-neutral streaming event. A single wire chunk may
// carry several of these fields at once.
type Delta struct {
	Reasoning    string
	Content      string
	ToolCalls    []ToolCallDelta
	FinishReason string
}

// StreamAssembler turns protocol deltas into the types.StreamResponse
// sequence the agent engine and the chat pipeline consume. It owns the
// "reasoning then answer" hand-off, tool-call fragment assembly, the early
// tool-call notification (ResponseTypeToolCall) once a function name has
// stabilised, streaming extraction of the thinking tool's `thought` field
// and live sandbox file-edit progress.
type StreamAssembler struct {
	ThinkingEmitter
	ctx              context.Context
	model            string
	toolCallMap      map[int]*types.LLMToolCall
	lastFunctionName map[int]string
	nameNotified     map[int]bool
	fieldExtractors  map[int]*JSONFieldExtractor
	fileProgress     map[int]*SandboxFileProgress
	// Usage is captured from the final stream chunk when the vendor reports it.
	Usage *types.TokenUsage
	// LastFinishReason is the last observed finish_reason for the EOF fallback.
	LastFinishReason string
	// ReasoningSignature / ReasoningMetadata accumulate provider artifacts that
	// the final Done chunk hands back to the caller through Data.
	ReasoningSignature string
	ReasoningMetadata  types.ProviderMetadata

	firstToolCallSeen    bool
	noToolCallStopLogged bool
	firstContentSeen     bool
	firstReasoningSeen   bool
	startedAt            time.Time
	aborted              bool
}

// emit hands one chunk to the consumer, giving up as soon as the call context
// is done. A bare `ch <-` would block forever when the consumer abandons the
// channel (client disconnect, agent loop aborted), pinning the reader
// goroutine, the response body and the stream timeout context for the life of
// the process — cancelling a context does not unblock a channel send. Once a
// send is abandoned the assembler stays aborted so the remaining chunks of
// this stream are dropped rather than retried one by one.
func (a *StreamAssembler) emit(ch chan<- types.StreamResponse, resp types.StreamResponse) bool {
	if a.aborted {
		return false
	}
	// Prefer the cancellation branch deterministically: with both cases ready
	// select would pick at random and could push into an abandoned channel.
	select {
	case <-a.ctx.Done():
		a.aborted = true
		return false
	default:
	}
	select {
	case ch <- resp:
		return true
	case <-a.ctx.Done():
		a.aborted = true
		return false
	}
}

// Aborted reports whether the consumer went away mid-stream. Protocol loops
// check it to stop reading instead of decoding a stream nobody listens to.
func (a *StreamAssembler) Aborted() bool { return a.aborted }

// NewStreamAssembler creates an assembler for one stream.
func NewStreamAssembler(ctx context.Context, model string) *StreamAssembler {
	return &StreamAssembler{
		ctx:              ctx,
		model:            model,
		toolCallMap:      make(map[int]*types.LLMToolCall),
		lastFunctionName: make(map[int]string),
		nameNotified:     make(map[int]bool),
		fieldExtractors:  make(map[int]*JSONFieldExtractor),
		fileProgress:     make(map[int]*SandboxFileProgress),
		startedAt:        time.Now(),
	}
}

func (a *StreamAssembler) elapsedMs() int64 {
	if a.startedAt.IsZero() {
		return 0
	}
	return time.Since(a.startedAt).Milliseconds()
}

// OrderedToolCalls returns the assembled calls in index order, or nil.
//
// The indices are the vendor's, not ours: gateways number calls from 1, and
// parallel calls can arrive with gaps. Walking 0..len(map) would silently drop
// every call outside that range — and a round that loses its calls reaches the
// agent as a plain answer — so the keys are sorted instead.
func (a *StreamAssembler) OrderedToolCalls() []types.LLMToolCall {
	if len(a.toolCallMap) == 0 {
		return nil
	}
	indices := make([]int, 0, len(a.toolCallMap))
	for idx := range a.toolCallMap {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	result := make([]types.LLMToolCall, 0, len(a.toolCallMap))
	for _, idx := range indices {
		if tc := a.toolCallMap[idx]; tc != nil {
			result = append(result, *tc)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ToolCallCount reports how many distinct tool calls have been seen.
func (a *StreamAssembler) ToolCallCount() int { return len(a.toolCallMap) }

// SetToolCallMetadata attaches provider state to the call at index, creating
// a placeholder entry when the metadata arrives before the call itself.
func (a *StreamAssembler) SetToolCallMetadata(index int, metadata types.ToolCallMetadata) {
	if len(metadata) == 0 {
		return
	}
	entry, exists := a.toolCallMap[index]
	if !exists || entry == nil {
		entry = &types.LLMToolCall{Type: "function"}
		a.toolCallMap[index] = entry
	}
	entry.ProviderMetadata = metadata
}

// SetUsage records usage from the vendor's final chunk.
func (a *StreamAssembler) SetUsage(u types.TokenUsage) { a.Usage = &u }

// doneData renders the reasoning artifacts for the closing chunk.
func (a *StreamAssembler) doneData() map[string]interface{} {
	if a.ReasoningSignature == "" && len(a.ReasoningMetadata) == 0 {
		return nil
	}
	data := map[string]interface{}{}
	if a.ReasoningSignature != "" {
		data["reasoning_signature"] = a.ReasoningSignature
	}
	if len(a.ReasoningMetadata) > 0 {
		data["reasoning_metadata"] = a.ReasoningMetadata
	}
	return data
}

// Process consumes one delta and emits the resulting stream responses. It
// mirrors the event sequence of the original OpenAI stream loop so every
// downstream consumer sees exactly what it saw before the protocol split.
func (a *StreamAssembler) Process(ch chan<- types.StreamResponse, d Delta) {
	isDone := d.FinishReason != ""
	if isDone {
		a.LastFinishReason = d.FinishReason
	}

	if len(d.ToolCalls) > 0 {
		a.processToolCalls(ch, d.ToolCalls)
	}

	if isDone && d.FinishReason == "stop" && !a.firstToolCallSeen && !a.noToolCallStopLogged {
		logger.Infof(a.ctx, "[LLM Stream] Natural-stop at protocol layer "+
			"(finish=stop, tool_calls never observed, thinking_seen=%t, first_content_seen=%t, elapsed_ms=%d)",
			a.active, a.firstContentSeen, a.elapsedMs())
		a.noToolCallStopLogged = true
	}

	if d.Reasoning != "" {
		if !a.firstReasoningSeen {
			a.firstReasoningSeen = true
			logger.Infof(a.ctx, "[LLM Stream] First reasoning chunk at protocol layer "+
				"(len=%d, preview=%q, elapsed_ms=%d)",
				len(d.Reasoning), TruncateForDebug(d.Reasoning, 80), a.elapsedMs())
		}
		a.Emit(ch, d.Reasoning)
	}

	if d.Content != "" {
		if !a.firstContentSeen {
			a.firstContentSeen = true
			logger.Infof(a.ctx, "[LLM Stream] First content chunk at protocol layer "+
				"(len=%d, preview=%q, tool_call_seen=%t, thinking_seen=%t, elapsed_ms=%d)",
				len(d.Content), TruncateForDebug(d.Content, 80),
				a.firstToolCallSeen, a.firstReasoningSeen, a.elapsedMs())
		}
		a.Finish(ch)
		a.emit(ch, types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      d.Content,
			Done:         isDone,
			ToolCalls:    a.OrderedToolCalls(),
			FinishReason: d.FinishReason,
		})
	}

	if isDone && len(a.toolCallMap) > 0 {
		a.emit(ch, types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			ToolCalls:    a.OrderedToolCalls(),
			FinishReason: d.FinishReason,
		})
	}

	if isDone {
		a.Finish(ch)
	}

	if isDone && d.Content == "" && len(a.toolCallMap) == 0 {
		a.emit(ch, types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			FinishReason: d.FinishReason,
		})
	}
}

// Emit forwards a reasoning chunk through the cancellation-aware send,
// shadowing the embedded ThinkingEmitter's blocking version.
func (a *StreamAssembler) Emit(ch chan<- types.StreamResponse, content string) {
	a.active = true
	a.emit(ch, types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: content})
}

// Finish emits the single thinking-done marker if one is owed, through the
// cancellation-aware send. Shadows the embedded ThinkingEmitter's version.
func (a *StreamAssembler) Finish(ch chan<- types.StreamResponse) {
	if !a.active {
		return
	}
	a.active = false
	a.emit(ch, types.StreamResponse{ResponseType: types.ResponseTypeThinking, Done: true})
}

// End emits the closing chunk once the vendor stream is exhausted. It logs
// usage and carries the assembled tool calls, usage and reasoning artifacts.
func (a *StreamAssembler) End(ch chan<- types.StreamResponse) {
	a.Finish(ch)
	LogUsage(a.ctx, a.model, a.Usage)
	a.emit(ch, types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Done:         true,
		ToolCalls:    a.OrderedToolCalls(),
		Usage:        a.Usage,
		FinishReason: a.LastFinishReason,
		Data:         a.doneData(),
	})
}

// Fail emits the error chunk for a broken stream.
func (a *StreamAssembler) Fail(ch chan<- types.StreamResponse, err error) {
	logger.Errorf(a.ctx, "Stream read error: %v (tool_calls_assembled=%d)", err, len(a.toolCallMap))
	a.Finish(ch)
	a.emit(ch, types.StreamResponse{
		ResponseType: types.ResponseTypeError,
		Content:      err.Error(),
		Done:         true,
		ToolCalls:    a.OrderedToolCalls(),
		Usage:        a.Usage,
		FinishReason: types.FinishReasonIncomplete,
	})
}

func (a *StreamAssembler) processToolCalls(ch chan<- types.StreamResponse, deltas []ToolCallDelta) {
	if !a.firstToolCallSeen && len(deltas) > 0 {
		a.firstToolCallSeen = true
		var firstID, firstName string
		for _, tc := range deltas {
			if tc.ID != "" {
				firstID = tc.ID
			}
			if tc.Name != "" {
				firstName = tc.Name
			}
			if firstID != "" || firstName != "" {
				break
			}
		}
		logger.Infof(a.ctx, "[LLM Stream] First tool_calls delta at protocol layer "+
			"(count=%d, first_id=%q, first_name=%q, first_content_seen=%t, thinking_seen=%t, elapsed_ms=%d)",
			len(deltas), firstID, firstName, a.firstContentSeen, a.firstReasoningSeen, a.elapsedMs())
	}

	for _, tc := range deltas {
		index := tc.Index
		entry, exists := a.toolCallMap[index]
		if !exists || entry == nil {
			entry = &types.LLMToolCall{Type: tc.Type}
			a.toolCallMap[index] = entry
		}
		if tc.ID != "" {
			entry.ID = tc.ID
		}
		if tc.Type != "" {
			entry.Type = tc.Type
		}
		if entry.Type == "" {
			entry.Type = "function"
		}
		if tc.Name != "" {
			// Some runtimes (vLLM Ascend) resend the full name on every chunk;
			// treat an identical name as a repeat rather than a suffix.
			if entry.Function.Name != tc.Name {
				entry.Function.Name += tc.Name
			}
		}

		argsUpdated := false
		if tc.Arguments != "" {
			entry.Function.Arguments += tc.Arguments
			argsUpdated = true
		}

		currName := entry.Function.Name
		var progressArgs map[string]any
		if IsSandboxMutationTool(currName) && argsUpdated {
			prog := a.fileProgress[index]
			if prog == nil {
				prog = NewSandboxFileProgress(currName)
				a.fileProgress[index] = prog
			}
			if payload, ok := prog.Feed(tc.Arguments); ok {
				progressArgs = payload
			}
		}

		if currName != "" &&
			currName == a.lastFunctionName[index] &&
			argsUpdated &&
			!a.nameNotified[index] &&
			entry.ID != "" {
			data := map[string]interface{}{
				"tool_name":    currName,
				"tool_call_id": entry.ID,
			}
			if progressArgs != nil {
				data["arguments"] = progressArgs
			}
			a.emit(ch, types.StreamResponse{ResponseType: types.ResponseTypeToolCall, Data: data})
			a.nameNotified[index] = true
			progressArgs = nil
		} else if progressArgs != nil && entry.ID != "" && currName != "" {
			a.emit(ch, types.StreamResponse{
				ResponseType: types.ResponseTypeToolCall,
				Data: map[string]interface{}{
					"tool_name":    currName,
					"tool_call_id": entry.ID,
					"arguments":    progressArgs,
				},
			})
		}

		a.lastFunctionName[index] = currName

		if entry.Function.Name == "thinking" && argsUpdated {
			extractor, exists := a.fieldExtractors[index]
			if !exists {
				extractor = NewJSONFieldExtractor("thought")
				a.fieldExtractors[index] = extractor
			}
			if chunk := extractor.Feed(tc.Arguments); chunk != "" {
				a.emit(ch, types.StreamResponse{
					ResponseType: types.ResponseTypeThinking,
					Content:      chunk,
					Data: map[string]interface{}{
						"source":       "thinking_tool",
						"tool_call_id": entry.ID,
					},
				})
			}
		}
	}
}

// TruncateForDebug shortens a string for log previews.
func TruncateForDebug(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + fmt.Sprintf("...(%d chars)", len(runes))
}

// ReplaceToolCalls swaps the assembled tool calls for a protocol's own final
// view (for example, one that drops calls the stream never closed).
func (a *StreamAssembler) ReplaceToolCalls(calls []types.LLMToolCall) {
	a.toolCallMap = make(map[int]*types.LLMToolCall, len(calls))
	for i := range calls {
		call := calls[i]
		a.toolCallMap[i] = &call
	}
}
