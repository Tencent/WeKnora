package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// StreamEvent represents a single event in the stream
type StreamEvent struct {
	ID string `json:"id"` // Unique event ID
	// Event type (thinking, tool_call, tool_result, references, complete, etc.)
	Type      types.ResponseType     `json:"type"`
	Content   string                 `json:"content"`        // Event content (chunk for streaming events)
	Done      bool                   `json:"done"`           // Whether this event is done
	Timestamp time.Time              `json:"timestamp"`      // When this event occurred
	Data      map[string]interface{} `json:"data,omitempty"` // Additional event data (references, metadata, etc.)
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
}
