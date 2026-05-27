package output

import (
	"encoding/json"
	"io"
)

// InitEvent is the CLI-injected lifecycle event written at the head of a
// streaming command's NDJSON output. Carries enough context for an agent to
// thread follow-ups (session id, kb id, model, profile).
//
// Type field is always "init"; the JSON tag fixes the wire shape.
type InitEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	// MessageID anchors a resumed stream (`session continue-stream`) to the
	// specific assistant message whose event buffer is being replayed. Empty
	// for fresh streams (chat / session ask) where the message id is only
	// known after the SDK emits its first agent_query frame.
	MessageID string `json:"message_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	KBID      string `json:"kb_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Profile   string `json:"profile,omitempty"`
}

// WriteNDJSONLine emits one NDJSON event line to w.
func WriteNDJSONLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// EmitInit writes the lifecycle init event to start an NDJSON stream.
// MUST be called before any other writes to w within an NDJSON stream —
// agents key on lines[0].type == "init" to discover session/profile/kb
// context. Sets Type=init regardless of caller-supplied value.
func EmitInit(w io.Writer, ev InitEvent) error {
	ev.Type = "init"
	return WriteNDJSONLine(w, ev)
}

// EmitSDKEvent passes through the raw SDK event as one NDJSON line.
// The SDK is the source of truth for event vocabulary; the CLI does not
// rename or reshape events.
func EmitSDKEvent(w io.Writer, ev any) error {
	return WriteNDJSONLine(w, ev)
}
