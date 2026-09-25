package pluginapi

import "encoding/json"

// EventType is the kind of one NDJSON line in a streaming answer.
type EventType string

const (
	// EventItem carries one result (a FetchedItem for connectors).
	EventItem EventType = "item"
	// EventCheckpoint carries a resumable cursor; WeKnora persists it.
	EventCheckpoint EventType = "checkpoint"
	// EventProgress reports progress for display.
	EventProgress EventType = "progress"
	// EventLog forwards a log line to WeKnora's logs.
	EventLog EventType = "log"
	// EventError ends the stream with an error.
	EventError EventType = "error"
	// EventEnd ends the stream successfully, with the final Data.
	EventEnd EventType = "end"
)

// Event is one line of a streaming answer. A stream that stops without an
// "end" or "error" event was interrupted and is treated as failed.
type Event struct {
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	// Error is set on EventError.
	Error *Error `json:"error,omitempty"`
	// Message is set on EventLog and EventProgress.
	Message string `json:"message,omitempty"`
	// Level is set on EventLog: debug, info, warn, error.
	Level string `json:"level,omitempty"`
}
