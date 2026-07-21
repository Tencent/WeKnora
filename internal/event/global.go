package event

import (
	"context"
	"sync"
)

var (
	// globalBus is the global event bus instance
	globalBus *Bus
	once      sync.Once
)

// GetGlobalBus returns the global event bus instance
// It uses singleton pattern to ensure only one instance exists
func GetGlobalBus() *Bus {
	once.Do(func() {
		globalBus = NewBus()
	})
	return globalBus
}

// SetGlobalBus sets the global event bus instance
// This is useful for testing or custom configurations
func SetGlobalBus(bus *Bus) {
	globalBus = bus
}

// On registers an event handler on the global event bus
func On(eventType Type, handler Handler) {
	GetGlobalBus().On(eventType, handler)
}

// Off removes all handlers for a specific event type from the global event bus
func Off(eventType Type) {
	GetGlobalBus().Off(eventType)
}

// Emit publishes an event to the global event bus
func Emit(ctx context.Context, event Event) error {
	return GetGlobalBus().Emit(ctx, event)
}

// EmitAndWait publishes an event to the global event bus and waits for all handlers
func EmitAndWait(ctx context.Context, event Event) error {
	return GetGlobalBus().EmitAndWait(ctx, event)
}

// HasHandlers checks if there are any handlers registered for an event type
func HasHandlers(eventType Type) bool {
	return GetGlobalBus().HasHandlers(eventType)
}

// Clear removes all event handlers from the global event bus
func Clear() {
	GetGlobalBus().Clear()
}
