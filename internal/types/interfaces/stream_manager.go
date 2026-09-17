package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// StreamEvent represents a single event in the stream
type StreamEvent struct {
	ID        string                 `json:"id"`              // Unique event ID
	Type      types.ResponseType     `json:"type"`            // Event type (thinking, tool_call, complete, etc.)
	Content   string                 `json:"content"`         // Event content (chunk for streaming events)
	Done      bool                   `json:"done"`            // Whether this event is done
	Timestamp time.Time              `json:"timestamp"`       // When this event occurred
	Data      map[string]interface{} `json:"data,omitempty"`  // Additional event data (references, metadata, etc.)
	Usage     *types.TokenUsage      `json:"usage,omitempty"` // LLM token usage aggregated over the turn (complete events)
}

// StreamManager stream manager interface - minimal append-only design
// All stream state is managed through events: metadata, references, completion, etc.
type StreamManager interface {
	// AppendEvent appends a single event to the stream
	// Uses Redis RPush for O(1) append performance
	// All event types (thinking, tool_call, references, complete) use this method
	AppendEvent(ctx context.Context, sessionID, messageID string, event StreamEvent) error

	// GetEvents gets events starting from offset
	// Uses Redis LRange for incremental reads
	// Returns: events slice, next offset for subsequent reads, error
	GetEvents(ctx context.Context, sessionID, messageID string, fromOffset int) ([]StreamEvent, int, error)

	// AppendSteerEvents appends control events (steer instructions) to a
	// dedicated per-run sub-list that is never surfaced on the SSE stream.
	// Producers are incoming "append a message to the running turn" HTTP
	// requests; the only consumer is the running turn itself.
	AppendSteerEvents(ctx context.Context, sessionID, messageID string, events []StreamEvent) error

	// GetSteerEvents drains the steer sub-list starting fromOffset, mirroring
	// GetEvents semantics. An absent list is an empty result, not an error.
	GetSteerEvents(ctx context.Context, sessionID, messageID string, fromOffset int) ([]StreamEvent, int, error)

	// UpdateSteerEventData merges keys into a queued steer event's Data map
	// (promote to inject, mark consumed). Returns false when the event is
	// missing. Implementations must apply the change atomically so a
	// concurrent AppendSteerEvents cannot be lost.
	UpdateSteerEventData(
		ctx context.Context, sessionID, messageID, eventID string, data map[string]interface{},
	) (bool, error)

	// DeleteSteerEvent removes a queued steer event by ID. Returns false when
	// the event is missing. Used when the user dismisses an overlay item.
	DeleteSteerEvent(ctx context.Context, sessionID, messageID, eventID string) (bool, error)

	// SetLiveRun records which assistant message is currently generating for
	// a session. It is exclusive: if another assistant is already live, it
	// returns an error so executeQA cannot start a second engine. Calling it
	// again for the same assistant is a no-op. Follow-up handoff that must
	// replace the previous run uses ClaimLiveRun.
	SetLiveRun(ctx context.Context, sessionID, assistantMessageID, requestID string) error

	// ClaimLiveRun overwrites the live-run marker. Used when a finishing
	// turn hands the session to a follow-up before it CAS-clears itself.
	ClaimLiveRun(ctx context.Context, sessionID, assistantMessageID, requestID string) error

	// GetLiveRun returns the session's generating assistant message, or empty
	// strings when no run is marked live.
	GetLiveRun(ctx context.Context, sessionID string) (assistantMessageID, requestID string, err error)

	// ClearLiveRun drops the marker, but only when it still points at
	// assistantMessageID: a follow-up run may already have replaced it.
	ClearLiveRun(ctx context.Context, sessionID, assistantMessageID string) error
}
