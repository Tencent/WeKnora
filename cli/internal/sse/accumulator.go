// Package sse provides helpers for consuming server-sent event streams from
// the WeKnora SDK (KnowledgeQAStream, ContinueStream).
//
// Accumulator is the canonical sink: every callback event appends to a
// buffered Content string and updates terminal-state fields like References
// and SessionID. The non-streaming JSON mode reads .Result() once .Done()
// is true; streaming mode writes Content tokens directly to stdout and
// only consults the accumulator for the final References footer.
package sse

import (
	"strings"

	sdk "github.com/Tencent/WeKnora/client"
)

// Accumulator buffers a KnowledgeQAStream callback sequence.
//
// Zero value is ready to use. Not safe for concurrent Append calls - the SDK
// invokes its callback sequentially on a single goroutine, so this matches
// the only contract that exists today.
//
// Demuxes by ResponseType so the answer string is not polluted by thinking
// or reflection fragments - the model layer (internal/models/chat/
// remote_api.go) emits ResponseTypeThinking events whenever the upstream
// LLM produces reasoning_content (GPT-5 / Claude extended thinking), and
// without demux those would be silently concatenated into Result().
type Accumulator struct {
	answer             strings.Builder
	thinking           strings.Builder
	References         []*sdk.SearchResult
	SessionID          string
	AssistantMessageID string
	finished           bool
}

// Append consumes one StreamResponse event. Safe to call multiple times;
// idempotent post-Done so callers do not need to special-case late events.
func (a *Accumulator) Append(r *sdk.StreamResponse) {
	if r == nil || a.finished {
		return
	}
	switch r.ResponseType {
	case sdk.ResponseTypeAnswer:
		if r.Content != "" {
			a.answer.WriteString(r.Content)
		}
	case sdk.ResponseTypeThinking, sdk.ResponseTypeReflection:
		if r.Content != "" {
			a.thinking.WriteString(r.Content)
		}
	default:
		// Frames without a typed ResponseType (legacy / metadata-only
		// payloads) that still carry an answer fragment fall through here.
		// Preserve the legacy contract by treating untyped Content as
		// answer text.
		if r.ResponseType == "" && r.Content != "" {
			a.answer.WriteString(r.Content)
		}
	}
	if r.SessionID != "" && a.SessionID == "" {
		a.SessionID = r.SessionID
	}
	if r.AssistantMessageID != "" && a.AssistantMessageID == "" {
		a.AssistantMessageID = r.AssistantMessageID
	}
	// Capture references whenever they arrive - they may land on a
	// dedicated `references` event before the terminal `complete`.
	if r.KnowledgeReferences != nil {
		a.References = r.KnowledgeReferences
	}
	// Stream is only truly done on response_type=complete. Other events
	// (notably the leading agent_query metadata frame) carry done=true to
	// mark their own per-event completion, but the answer fragments arrive
	// in subsequent response_type=answer events. Gating on response_type
	// avoids the off-by-one termination that would discard the entire
	// answer.
	if r.ResponseType == sdk.ResponseTypeComplete {
		a.finished = true
	}
}

// Done reports whether a terminal event was observed.
func (a *Accumulator) Done() bool { return a.finished }

// Result returns the accumulated answer-event content.
func (a *Accumulator) Result() string { return a.answer.String() }

// Thinking returns the accumulated reasoning / reflection content surfaced
// by the upstream LLM (only populated when the active model produces
// reasoning_content). Empty for non-reasoning models.
func (a *Accumulator) Thinking() string { return a.thinking.String() }
