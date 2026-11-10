package types

import (
	"context"
)

// EventHandler is a function that handles events
type EventHandler func(ctx context.Context, evt Event) error

// Event represents an event in the system
// This is a simplified version to avoid import cycle with event package
type Event struct {
	ID        string                 // Event ID
	Type      EventType              // Event type (uses EventType from chat_manage.go)
	SessionID string                 // Session ID
	Data      interface{}            // Event data
	Metadata  map[string]interface{} // Event metadata
	RequestID string                 // Request ID
}

// EventBusInterface defines the interface for event bus operations
// This interface allows types package to use EventBus without importing the concrete type
// and avoids circular dependencies
type EventBusInterface interface {
	// On registers an event handler for a specific event type
	On(eventType EventType, handler EventHandler)

	// Emit publishes an event to all registered handlers
	Emit(ctx context.Context, evt Event) error
}
