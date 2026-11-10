package event

import (
	"context"
	"sync"
)

var (
	// globalEventBus is the global event bus instance
	globalEventBus *EventBus
	once           sync.Once
)

// GetGlobalEventBus returns the global event bus instance
// It uses singleton pattern to ensure only one instance exists
func GetGlobalEventBus() *EventBus {
	once.Do(func() {
		globalEventBus = NewEventBus()
	})
	return globalEventBus
}

// SetGlobalEventBus sets the global event bus instance
// This is useful for testing or custom configurations
func SetGlobalEventBus(bus *EventBus) {
	globalEventBus = bus
}

// On registers an event handler on the global event bus
func On(eventType EventType, handler EventHandler) {
	GetGlobalEventBus().On(eventType, handler)
}

// Off removes all handlers for a specific event type from the global event bus
func Off(eventType EventType) {
	GetGlobalEventBus().Off(eventType)
}

// Emit publishes an event to the global event bus
func Emit(ctx context.Context, event Event) error {
	return GetGlobalEventBus().Emit(ctx, event)
}

// EmitAndWait publishes an event to the global event bus and waits for all handlers
func EmitAndWait(ctx context.Context, event Event) error {
	return GetGlobalEventBus().EmitAndWait(ctx, event)
}

// HasHandlers checks if there are any handlers registered for an event type
func HasHandlers(eventType EventType) bool {
	return GetGlobalEventBus().HasHandlers(eventType)
}

// Clear removes all event handlers from the global event bus
func Clear() {
	GetGlobalEventBus().Clear()
}
