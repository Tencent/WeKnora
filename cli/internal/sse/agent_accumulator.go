package sse

import (
	"strings"

	sdk "github.com/Tencent/WeKnora/client"
)

// AgentToolEvent captures one tool_call / tool_result event from an
// agent SSE stream. Kind is the SDK event type (typed, not bare string)
// so consumers can compare against sdk.AgentResponseTypeToolCall /
// sdk.AgentResponseTypeToolResult without retyping the constants.
// NOTE: Kind is the event kind, NOT the function name; the function
// name typically lives in Data for tool_call events.
type AgentToolEvent struct {
	ID     string                `json:"id"`
	Kind   sdk.AgentResponseType `json:"kind,omitempty"`
	Result string                `json:"result,omitempty"`
	Data   map[string]any        `json:"data,omitempty"`
}

// AgentAccumulator buffers an AgentQAStream callback sequence. Distinct
// from Accumulator (KnowledgeQAStream) because the agent event model is
// wider - events include thinking / reflection / tool_call / tool_result
// / answer / references / error, with a flat `Done bool` on each frame
// rather than the ResponseType=complete sentinel KnowledgeQAStream emits.
//
// Zero value is ready to use. Not safe for concurrent Append calls - the
// SDK callback contract is sequential on a single goroutine.
//
// API mirrors sse.Accumulator: private builders + accessor methods so the
// "Append is idempotent post-Done" invariant cannot be broken by external
// mutation. References and ToolEvents stay exported as plain slices since
// they carry no such invariant.
type AgentAccumulator struct {
	answer     strings.Builder
	thinking   strings.Builder
	References []*sdk.SearchResult
	ToolEvents []AgentToolEvent
	done       bool
}

// Answer returns the accumulated `answer`-event content.
func (a *AgentAccumulator) Answer() string { return a.answer.String() }

// Thinking returns the accumulated `thinking` / `reflection` content
// surfaced by the agent during its reasoning pass.
func (a *AgentAccumulator) Thinking() string { return a.thinking.String() }

// Done reports whether the stream emitted a terminal frame.
func (a *AgentAccumulator) Done() bool { return a.done }

// Append consumes one AgentStreamResponse event. Idempotent post-Done so
// callers do not need to special-case late events.
func (a *AgentAccumulator) Append(r *sdk.AgentStreamResponse) {
	if r == nil || a.done {
		return
	}
	switch r.ResponseType {
	case sdk.AgentResponseTypeAnswer:
		if r.Content != "" {
			a.answer.WriteString(r.Content)
		}
	case sdk.AgentResponseTypeThinking, sdk.AgentResponseTypeReflection:
		if r.Content != "" {
			a.thinking.WriteString(r.Content)
		}
	case sdk.AgentResponseTypeToolCall, sdk.AgentResponseTypeToolResult:
		a.ToolEvents = append(a.ToolEvents, AgentToolEvent{
			ID:     r.ID,
			Kind:   r.ResponseType,
			Result: r.Content,
			Data:   r.Data,
		})
	}
	// References can arrive on a dedicated `references` event OR
	// piggyback on another event's KnowledgeReferences field; always
	// capture the latest, matching sse.Accumulator's "always replace"
	// semantic for the parallel KnowledgeQAStream contract.
	if r.KnowledgeReferences != nil {
		a.References = r.KnowledgeReferences
	}
	if r.Done {
		a.done = true
	}
}
